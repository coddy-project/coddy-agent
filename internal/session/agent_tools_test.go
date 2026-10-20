package session_test

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestAgentToolKind(t *testing.T) {
	for kind, want := range map[string]string{
		"read": "read", "search": "read", "fetch": "read",
		"edit": "write", "delete": "write", "move": "write",
		"execute": "run_command",
		"think":   "other", "other": "other", "": "other",
	} {
		if got := session.AgentToolKind(kind); got != want {
			t.Errorf("AgentToolKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

// A session loaded again draws the tool calls the agent behind an acp model
// ran as the rows they were live: before the answer, titled as they were,
// their input, and their outcome with the first lines of their output.
func TestReloadedSessionReplaysTheAgentsToolRows(t *testing.T) {
	m, store, root := newSubagentTestManagerWithRunner(t, func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	})
	parent := newParent(t, m, root)
	parent.AddMessage(llm.Message{Role: llm.RoleUser, Content: "read the notes"})
	parent.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "banana", AgentTools: []llm.AgentToolCall{
		{ID: "acp_t1", Title: "read notes.txt", Kind: "read", Status: "completed", Input: `{"filePath":"notes.txt"}`, Output: "banana split", OutputLines: 40},
		{ID: "acp_t2", Title: "bash go test", Kind: "execute", Status: "failed"},
	}})
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
	var seq []string
	for _, u := range rec.updates {
		switch v := u.(type) {
		case acp.ToolCallUpdate:
			seq = append(seq, "row "+v.ToolCallID+" "+v.Title+" "+v.Kind)
		case acp.ToolCallStatusUpdate:
			text := ""
			if len(v.Content) > 0 {
				text = v.Content[0].Content.Text
			}
			seq = append(seq, v.Status+" "+v.ToolCallID+" "+text)
			if v.ToolCallID == "acp_t1" && v.Status == "completed" {
				preview, _ := v.Meta["coddy"].(map[string]interface{})["toolResultPreview"].(map[string]interface{})
				if preview["totalLines"] != 40 {
					t.Errorf("outcome meta = %+v", v.Meta)
				}
			}
		case acp.MessageChunkUpdate:
			if v.SessionUpdate == acp.UpdateTypeAgentMessageChunk {
				seq = append(seq, "answer "+v.Content.Text)
			}
		}
	}
	want := []string{
		"row acp_t1 read notes.txt read",
		`in_progress acp_t1 {"filePath":"notes.txt"}`,
		"completed acp_t1 banana split",
		"row acp_t2 bash go test run_command",
		"failed acp_t2 ",
		"answer banana",
	}
	if len(seq) != len(want) {
		t.Fatalf("replay = %q, want %q", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("replay = %q, want %q", seq, want)
		}
	}
}
