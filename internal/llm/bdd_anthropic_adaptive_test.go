package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"
)

type anthropicAdaptiveState struct {
	t        *testing.T
	mu       sync.Mutex
	requests []string
	server   *httptest.Server
	provider *anthropicProvider
}

func (s *anthropicAdaptiveState) givenModel(model, effort string) error {
	return s.serve(model, effort, anthropicSSE(s.t, anthropicEvents(
		anthropicThinkingBlock(0, "exact reasoning\n", "signed"),
		anthropicToolUseBlock(1, "call_1", "read", "{}"),
		anthropicMessageEnd("tool_use"),
	)...))
}

// givenSeveralBlocks answers with a reasoning block, a line of text, a signed
// progress update with the empty text of the omitted display, and the call.
func (s *anthropicAdaptiveState) givenSeveralBlocks(model string) error {
	return s.serve(model, "high", anthropicSSE(s.t, anthropicEvents(
		anthropicThinkingBlock(0, "plan the read", "sigA"),
		anthropicTextBlock(1, "Reading the file."),
		anthropicThinkingBlock(2, "", "sigB"),
		anthropicToolUseBlock(3, "call_1", "read", "{}"),
		anthropicMessageEnd("tool_use"),
	)...))
}

func (s *anthropicAdaptiveState) serve(model, effort, first string) error {
	s.requests = nil
	last := anthropicSSE(s.t, anthropicEvents(anthropicTextBlock(0, "done"), anthropicMessageEnd("end_turn"))...)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, string(body))
		count := len(s.requests)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if count == 1 {
			_, _ = w.Write([]byte(first))
		} else {
			_, _ = w.Write([]byte(last))
		}
	}))
	s.provider = newAnthropicProvider(model, "test", s.server.URL, s.server.Client(), 8192, 0, effort)
	return nil
}

func (s *anthropicAdaptiveState) continueToolCall(ctx context.Context) error {
	messages := []Message{{Role: RoleUser, Content: "read"}}
	tools := []ToolDefinition{{Name: "read", InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}}}
	resp, err := s.provider.Stream(ctx, messages, tools, func(StreamChunk) {})
	if err != nil {
		return err
	}
	if resp.ReasoningSignature == "" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_1" {
		return fmt.Errorf("stream lost signed thinking or tool call: %+v", resp)
	}
	messages = append(messages,
		Message{Role: RoleAssistant, Content: resp.Content, Reasoning: resp.Reasoning, ReasoningSignature: resp.ReasoningSignature, ToolCalls: resp.ToolCalls},
		Message{Role: RoleTool, ToolCallID: resp.ToolCalls[0].ID, Content: "result"},
	)
	resp, err = s.provider.Stream(ctx, messages, tools, func(StreamChunk) {})
	if err != nil {
		return err
	}
	if resp.Content != "done" || resp.StopReason != "end_turn" {
		return fmt.Errorf("continuation did not finish: %+v", resp)
	}
	return nil
}

func (s *anthropicAdaptiveState) requestsUse(thinking, effort string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) != 2 {
		return fmt.Errorf("provider requests = %d, want 2", len(s.requests))
	}
	for _, raw := range s.requests {
		if gjson.Get(raw, "thinking.type").String() != thinking || gjson.Get(raw, "output_config.effort").String() != effort {
			return fmt.Errorf("unexpected thinking configuration: %s", raw)
		}
		if thinking == "adaptive" && gjson.Get(raw, "thinking.display").String() != "summarized" {
			return fmt.Errorf("adaptive thinking without the summarized display: %s", raw)
		}
		budget := gjson.Get(raw, "thinking.budget_tokens")
		if thinking == "adaptive" && budget.Exists() || thinking == "enabled" && budget.Int() != 6144 {
			return fmt.Errorf("unexpected thinking budget: %s", raw)
		}
	}
	return nil
}

func (s *anthropicAdaptiveState) signedReplay() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) != 2 {
		return fmt.Errorf("provider requests = %d, want 2", len(s.requests))
	}
	for path, want := range map[string]string{
		"messages.1.content.0.type":        "thinking",
		"messages.1.content.0.thinking":    "exact reasoning\n",
		"messages.1.content.0.signature":   "signed",
		"messages.1.content.1.type":        "tool_use",
		"messages.1.content.1.id":          "call_1",
		"messages.2.content.0.type":        "tool_result",
		"messages.2.content.0.tool_use_id": "call_1",
	} {
		if got := gjson.Get(s.requests[1], path).String(); got != want {
			return fmt.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	return nil
}

func (s *anthropicAdaptiveState) everyBlockInOrder() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) != 2 {
		return fmt.Errorf("provider requests = %d, want 2", len(s.requests))
	}
	for path, want := range map[string]string{
		"messages.1.content.#":           "4",
		"messages.1.content.0.type":      "thinking",
		"messages.1.content.0.thinking":  "plan the read",
		"messages.1.content.0.signature": "sigA",
		"messages.1.content.1.type":      "text",
		"messages.1.content.1.text":      "Reading the file.",
		"messages.1.content.2.type":      "thinking",
		"messages.1.content.2.thinking":  "",
		"messages.1.content.2.signature": "sigB",
		"messages.1.content.3.type":      "tool_use",
		"messages.1.content.3.id":        "call_1",
	} {
		if got := gjson.Get(s.requests[1], path).String(); got != want {
			return fmt.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	return nil
}

func TestAnthropicAdaptiveThinkingFeature(t *testing.T) {
	s := &anthropicAdaptiveState{t: t}
	suite := godog.TestSuite{
		Name: "anthropic-adaptive-thinking",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				if s.server != nil {
					s.server.Close()
				}
				return ctx, nil
			})
			sc.Step(`^Anthropic model "([^"]*)" with reasoning effort "([^"]*)"$`, s.givenModel)
			sc.Step(`^Coddy streams a tool call and continues with its result$`, s.continueToolCall)
			sc.Step(`^both requests use "([^"]*)" thinking and output effort "([^"]*)"$`, s.requestsUse)
			sc.Step(`^signed thinking is replayed unchanged before tool use$`, s.signedReplay)
			sc.Step(`^Anthropic model "([^"]*)" answering with a note, a progress update and a tool call$`, s.givenSeveralBlocks)
			sc.Step(`^every thinking block is replayed with its own signature in the order the model wrote it$`, s.everyBlockInOrder)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/anthropic_adaptive_thinking.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("Anthropic adaptive thinking feature suite failed")
	}
}
