package session_test

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// ToolKind is the one mapping behind both the live turn (internal/agent
// announces each call with it) and the replay of a loaded session. It moved
// here from internal/agent with its cases.
func TestToolKind(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"read", "read"},
		{"keep_result", "read"},
		{"glob", "read"},
		{"grep", "read"},
		{"print_tree", "read"},
		{"websearch", "read"},
		{"webfetch", "read"},
		{"config_get", "read"},
		{"config_changes", "read"},
		{"coddy_docs_search", "read"},
		{"coddy_docs_read", "read"},
		{"write", "write"},
		{"edit", "write"},
		{"apply_patch", "write"},
		{"mkdir", "write"},
		{"rmdir", "write"},
		{"touch", "write"},
		{"rm", "write"},
		{"mv", "write"},
		{"config_commit", "write"},
		{"config_rollback", "write"},
		{"run_command", "run_command"},
		{"compact_context", "other"},
		{"spawn_agent", "other"},
		{"mcp_server__tool", "other"},
		{"", "other"},
	}
	for _, tc := range cases {
		if g := session.ToolKind(tc.name); g != tc.want {
			t.Errorf("ToolKind(%q) = %q, want %q", tc.name, g, tc.want)
		}
	}
}

// A session loaded again replays each tool call with the kind the live turn
// announced it with: a tool that only looks, print_tree and the web tools among
// them, replays as "read", not as "other". This holds the replay side of the
// shared mapping, end to end through a session load.
func TestReloadedSessionReplaysToolCallsWithTheLiveKind(t *testing.T) {
	m, store, root := newSubagentTestManagerWithRunner(t, func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	})
	parent := newParent(t, m, root)

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
	// The replayed kind of every call is the one the live turn announces it
	// with, and for the tools that only look that is "read".
	for _, tc := range calls {
		if live := session.ToolKind(tc.Name); got[tc.ID] != live {
			t.Errorf("replayed %s (%s) with kind %q, the live turn announces %q", tc.ID, tc.Name, got[tc.ID], live)
		}
	}
	for _, id := range []string{"call_tree", "call_search", "call_fetch", "call_keep", "call_cfg"} {
		if got[id] != "read" {
			t.Errorf("replayed %s with kind %q, want \"read\"", id, got[id])
		}
	}
	if got["call_edit"] != "write" || got["call_cmd"] != "run_command" || got["call_other"] != "other" {
		t.Errorf("replayed kinds = %v", got)
	}
	if len(got) != len(calls) {
		t.Fatalf("replayed %d tool calls, want %d: %v", len(got), len(calls), got)
	}
}
