package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type generationTimingProvider func(context.Context, func(llm.StreamChunk)) (*llm.Response, error)

func (p generationTimingProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("the generation timing harness uses Stream")
}

func (p generationTimingProvider) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	return p(ctx, onChunk)
}

func newGenerationTimingAgent(t *testing.T, providerType string, streaming bool) (*Agent, *session.State, *progressSender) {
	t.Helper()
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fixture", Type: providerType, APIKey: "fixture-only", Proxy: "none"}},
		Models:    []config.ModelEntry{{Model: "fixture/model", MaxTokens: 1000, MaxContextTokens: 128000, Stream: &streaming}},
		Agent:     config.Agent{Model: "fixture/model"},
	}
	if providerType == "acp" {
		cfg.Providers[0].ACP = &config.ACPProviderConfig{Command: "fixture-never-executed"}
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	state := &session.State{ID: "sess_generation_timing", CWD: t.TempDir(), SessionDir: t.TempDir(), Mode: session.ModeAgent}
	sender := &progressSender{}
	return NewAgent(cfg, state, sender, nil), state, sender
}

func requireUnmeasuredGeneration(t *testing.T, a *Agent, state *session.State, sender *progressSender, tokens int) {
	t.Helper()
	if _, err := a.Run(context.Background(), []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "hello"}}); err != nil {
		t.Fatal(err)
	}
	updates, _ := sender.progress()
	if len(updates) == 0 {
		t.Fatal("no turn progress was published")
	}
	last := updates[len(updates)-1]
	if !last.Completed || last.OutputTokens != tokens {
		t.Fatalf("output count was lost: %+v, want %d tokens", last, tokens)
	}
	for _, update := range updates {
		if update.TokensPerSecond != nil {
			t.Errorf("unmeasurable output acquired speed %g tok/s", *update.TokensPerSecond)
		}
	}
	for _, row := range state.GetUILog() {
		if row.Generation != nil {
			t.Errorf("unmeasurable output persisted a generation caption: %+v", row.Generation)
		}
	}
}

func TestBlockingModelHasNoGenerationSpeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "reply", "object": "chat.completion", "choices": []interface{}{map[string]interface{}{
				"index": 0, "finish_reason": "stop", "message": map[string]interface{}{
					"role": "assistant", "content": strings.Repeat("b", 100), "reasoning_content": strings.Repeat("a", 100),
				},
			}}, "usage": map[string]interface{}{"prompt_tokens": 10, "completion_tokens": 50, "total_tokens": 60},
		})
	}))
	defer srv.Close()
	a, state, sender := newGenerationTimingAgent(t, "openai", false)
	a.cfg.Providers[0].APIBase = srv.URL
	requireUnmeasuredGeneration(t, a, state, sender, 50)
}

func TestBlockingModelDoesNotMeasureReplayedDeltas(t *testing.T) {
	a, state, sender := newGenerationTimingAgent(t, "openai", false)
	a.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return generationTimingProvider(func(ctx context.Context, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
			text := strings.Repeat("a", 100)
			onChunk(llm.StreamChunk{ReasoningDelta: text})
			if !sleepCtx(ctx, time.Millisecond) {
				return nil, ctx.Err()
			}
			onChunk(llm.StreamChunk{TextDelta: text})
			return &llm.Response{Content: text, Reasoning: text, StopReason: "end_turn", OutputTokens: 50}, nil
		}), nil
	}
	requireUnmeasuredGeneration(t, a, state, sender, 50)
}

func TestSingleProviderChunkHasNoGenerationSpeed(t *testing.T) {
	text := strings.Repeat("a", 100)
	for _, tc := range []struct {
		name   string
		chunk  llm.StreamChunk
		tokens int
	}{
		{name: "reasoning and text", chunk: llm.StreamChunk{ReasoningDelta: text, TextDelta: text}, tokens: 50},
		{name: "text and argument delta", chunk: llm.StreamChunk{TextDelta: text, ToolCallDelta: &llm.ToolCall{ID: "c1", Name: "read", InputJSON: text}}, tokens: 50},
		{name: "text and complete tool call", chunk: llm.StreamChunk{TextDelta: text, ToolCall: &llm.ToolCall{ID: "c1", Name: "read", InputJSON: text}}, tokens: 51},
		{name: "reconciled arguments", chunk: llm.StreamChunk{TextDelta: text,
			ToolCallDelta: &llm.ToolCall{ID: "c1", Name: "read", InputJSON: "a"},
			ToolCall:      &llm.ToolCall{ID: "c1", Name: "read", InputJSON: text}}, tokens: 51},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, state, sender := newGenerationTimingAgent(t, "openai", true)
			a.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
				return generationTimingProvider(func(_ context.Context, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
					onChunk(tc.chunk)
					return &llm.Response{Content: text, StopReason: "end_turn"}, nil
				}), nil
			}
			requireUnmeasuredGeneration(t, a, state, sender, tc.tokens)
		})
	}
}

func TestACPAgentToolWaitHasNoGenerationSpeed(t *testing.T) {
	a, state, sender := newGenerationTimingAgent(t, "acp", true)
	a.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return generationTimingProvider(func(ctx context.Context, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
			text := strings.Repeat("a", 400)
			onChunk(llm.StreamChunk{TextDelta: text})
			onChunk(llm.StreamChunk{AgentTool: &llm.AgentToolCall{ID: "tool1", Title: "run a command", Kind: "execute", Status: "in_progress"}})
			if !sleepCtx(ctx, 10*time.Millisecond) {
				return nil, ctx.Err()
			}
			onChunk(llm.StreamChunk{AgentTool: &llm.AgentToolCall{ID: "tool1", Title: "run a command", Kind: "execute", Status: "completed"}})
			onChunk(llm.StreamChunk{TextDelta: text})
			return &llm.Response{Content: text + text, StopReason: "end_turn", OutputTokens: 200}, nil
		}), nil
	}
	requireUnmeasuredGeneration(t, a, state, sender, 200)
}
