package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// announceProvider streams one read call the way a provider with early names
// does (the name first, the complete call later), or only the complete call.
type announceProvider struct {
	path  string
	named bool
	done  bool
}

func (p *announceProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("stream required")
}

func (p *announceProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, emit func(llm.StreamChunk)) (*llm.Response, error) {
	if p.done {
		return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
	}
	p.done = true
	path, _ := json.Marshal(p.path)
	if p.named {
		emit(llm.StreamChunk{ToolCallNamed: &llm.ToolCall{ID: "read1", Name: "read"}})
	}
	tc := llm.ToolCall{ID: "read1", Name: "read", InputJSON: `{"path":` + string(path) + `}`}
	emit(llm.StreamChunk{ToolCall: &tc})
	return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
}

// An ACP client draws a row for every tool_call it receives, so a call is
// announced once and every later change of it is a tool_call_update.
func TestToolCallIsAnnouncedOnce(t *testing.T) {
	for _, named := range []bool{true, false} {
		t.Run(fmt.Sprintf("named=%v", named), func(t *testing.T) {
			cwd := t.TempDir()
			path := filepath.Join(cwd, "notes.txt")
			if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			sender := &draftPermissionSender{progressSender: &progressSender{}}
			cfg := &config.Config{
				Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
				Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
				Agent:     config.Agent{Model: "fake/model"},
			}
			cfg.Agent.ApplyDefaults()
			cfg.Prompts.ApplyDefaults()
			state := &session.State{ID: "announce", CWD: cwd, Mode: session.ModeAgent, SessionDir: t.TempDir()}
			ag := NewAgent(cfg, state, sender, nil)
			provider := &announceProvider{path: path, named: named}
			ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
			if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "read the notes"}}); err != nil {
				t.Fatalf("run: %v", err)
			}

			sender.mu.Lock()
			defer sender.mu.Unlock()
			announced := 0
			var later []string
			for _, u := range sender.updates {
				switch v := u.(type) {
				case acp.ToolCallUpdate:
					if v.ToolCallID != "read1" {
						continue
					}
					if v.SessionUpdate != acp.UpdateTypeToolCall {
						t.Errorf("announcement sessionUpdate = %q", v.SessionUpdate)
					}
					if announced > 0 || len(later) > 0 {
						t.Errorf("tool_call for read1 after it was announced: %+v", v)
					}
					announced++
					if v.Title != "read" || v.Kind != "read" || v.Status != "pending" {
						t.Errorf("announcement = %+v, want title/kind read, status pending", v)
					}
				case acp.ToolCallStatusUpdate:
					if v.ToolCallID != "read1" {
						continue
					}
					if v.SessionUpdate != acp.UpdateTypeToolCallUpdate {
						t.Errorf("update sessionUpdate = %q", v.SessionUpdate)
					}
					later = append(later, v.Status)
				}
			}
			if announced != 1 {
				t.Fatalf("tool_call for read1 sent %d times, want once", announced)
			}
			if len(later) == 0 || later[len(later)-1] != "completed" {
				t.Fatalf("tool_call_update statuses = %v, want ending in completed", later)
			}
		})
	}
}
