package session_test

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A session loaded again replays each tool call with the kind the live turn
// announced it with: a tool that only looks, print_tree and the web tools among
// them, replays as "read", not as "other". Both paths share one mapping
// (session.ToolKind); this holds the replay side of it.
func TestReloadedSessionReplaysToolCallsWithTheLiveKind(t *testing.T) {
	m, store, root := newSubagentTestManagerWithRunner(t, func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	})
	parent := newParent(t, m, root)

	want := map[string]string{
		"call_tree":   "read",
		"call_search": "read",
		"call_fetch":  "read",
		"call_keep":   "read",
		"call_cfg":    "read",
		"call_edit":   "write",
		"call_cmd":    "run_command",
		"call_other":  "other",
	}
	calls := []llm.ToolCall{
		{ID: "call_tree", Name: "print_tree", InputJSON: `{"path":"."}`},
		{ID: "call_search", Name: "websearch", InputJSON: `{"query":"coddy"}`},
		{ID: "call_fetch", Name: "webfetch", InputJSON: `{"url":"https://example.com"}`},
		{ID: "call_keep", Name: "keep_result", InputJSON: `{"path":"a.go"}`},
		{ID: "call_cfg", Name: "config_get", InputJSON: `{}`},
		{ID: "call_edit", Name: "edit", InputJSON: `{"path":"a.go"}`},
		{ID: "call_cmd", Name: "run_command", InputJSON: `{"command":"true"}`},
		{ID: "call_other", Name: "mcp__files__list", InputJSON: `{}`},
	}
	parent.AddMessage(llm.Message{Role: llm.RoleUser, Content: "look around"})
	parent.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: calls})
	for _, tc := range calls {
		parent.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: tc.ID, Content: "ok"})
	}
	parent.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})
	if err := store.Save(parent); err != nil {
		t.Fatal(err)
	}

	rec := &updateRecorder{}
	m.SetServer(rec)
	m.ForgetLiveSession(parent.ID)
	if _, err := m.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: parent.ID, CWD: root}); err != nil {
		t.Fatal(err)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	got := map[string]string{}
	for _, u := range rec.updates {
		if v, ok := u.(acp.ToolCallUpdate); ok {
			got[v.ToolCallID] = v.Kind
		}
	}
	for id, kind := range want {
		if got[id] != kind {
			t.Errorf("replayed %s with kind %q, want %q", id, got[id], kind)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("replayed %d tool calls, want %d: %v", len(got), len(want), got)
	}
}
