package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type resumePermissionSender struct{}

func (resumePermissionSender) SendSessionUpdate(string, interface{}) error { return nil }

func (resumePermissionSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (resumePermissionSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

type resumePermissionProvider struct {
	t    *testing.T
	seen []llm.Message
}

func (p *resumePermissionProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	p.t.Fatal("Complete must not be used by ResumeAfterPermission")
	return nil, nil
}

func (p *resumePermissionProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append([]llm.Message(nil), messages...)
	onChunk(llm.StreamChunk{TextDelta: "continued"})
	return &llm.Response{Content: "continued", StopReason: "end_turn"}, nil
}

func TestResumeAfterPermissionRejectContinuesWithoutExecutingTool(t *testing.T) {
	sessionDir := t.TempDir()
	st := &session.State{
		ID:         "sess_resume_reject",
		CWD:        t.TempDir(),
		Mode:       session.ModeAgent,
		SessionDir: sessionDir,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "run blocked command then continue"},
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID:        "call_blocked",
					Name:      "run_command",
					InputJSON: `{"command":"printf SHOULD_NOT_RUN"}`,
				}},
			},
		},
	}
	provider := &resumePermissionProvider{t: t}
	ag := NewAgent(&config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return provider, nil
	}

	stop, err := ag.ResumeAfterPermission(context.Background(), "call_blocked", &acp.PermissionResult{
		Outcome:  "cancelled",
		OptionID: "reject",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop reason %q", stop)
	}
	var toolMsg *llm.Message
	for i := range st.GetMessages() {
		m := st.GetMessages()[i]
		if m.Role == llm.RoleTool && m.ToolCallID == "call_blocked" {
			toolMsg = &m
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("missing resumed tool result message")
	}
	if strings.Contains(toolMsg.Content, "SHOULD_NOT_RUN") {
		t.Fatalf("rejected permission executed the tool: %q", toolMsg.Content)
	}
	if toolMsg.Content != "permission denied by user" {
		t.Fatalf("tool result %q", toolMsg.Content)
	}
	if len(provider.seen) == 0 {
		t.Fatal("provider was not called to continue after rejected permission")
	}
	last := provider.seen[len(provider.seen)-1]
	if last.Role != llm.RoleTool || last.ToolCallID != "call_blocked" || last.Content != "permission denied by user" {
		t.Fatalf("provider did not receive denied tool result as latest message: %+v", last)
	}
	if got := st.GetMessages()[len(st.GetMessages())-1]; got.Role != llm.RoleAssistant || got.Content != "continued" {
		t.Fatalf("missing continuation assistant message: %+v", got)
	}
}
