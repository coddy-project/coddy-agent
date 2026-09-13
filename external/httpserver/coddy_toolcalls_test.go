//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestToolCallListReturnsTodoPlanSnapshot(t *testing.T) {
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())

	created, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	if st == nil {
		t.Fatal("session missing")
	}
	st.AddMessage(llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID:        "todo-update-1",
			Name:      "coddy_todo_item_update",
			InputJSON: `{"index":1,"status":"completed"}`,
		}},
	})
	if err := session.MarkToolCallFinished(st.GetPersistedSessionDir(), "todo-update-1", "coddy_todo_item_update", "todo", "completed"); err != nil {
		t.Fatal(err)
	}
	want := []acp.PlanEntry{
		{Content: "Inspect cards", Status: "completed"},
		{Content: "Render preview", Status: "completed"},
	}
	if err := session.WriteToolCallPlanSnapshot(st.GetPersistedSessionDir(), "todo-update-1", want); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+created.SessionID+"/tool-calls", nil)
	req.SetPathValue("id", created.SessionID)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ToolCalls []struct {
			ToolCallID   string          `json:"toolCallId"`
			PlanSnapshot []acp.PlanEntry `json:"planSnapshot"`
		} `json:"toolCalls"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.ToolCalls) != 1 || body.ToolCalls[0].ToolCallID != "todo-update-1" {
		t.Fatalf("tool calls = %+v", body.ToolCalls)
	}
	if len(body.ToolCalls[0].PlanSnapshot) != len(want) || body.ToolCalls[0].PlanSnapshot[1].Content != "Render preview" {
		t.Fatalf("planSnapshot = %+v, want %+v", body.ToolCalls[0].PlanSnapshot, want)
	}
}

// The toolCallId of GET /coddy/sessions/{id}/tool-calls/{toolCallId} is whatever
// the caller puts in the path, percent-encoded separators included, and it is
// resolved inside the session bundle. A traversal must not read the tool call of
// another session.
func TestToolCallGetCannotReachAnotherSessionsBundle(t *testing.T) {
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())

	victim, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	victimState := mgr.SessionByID(victim.SessionID)
	if victimState == nil {
		t.Fatal("victim session missing")
	}
	const secret = "SECRET_OF_ANOTHER_SESSION"
	if err := session.MarkToolCallStarted(victimState.GetPersistedSessionDir(), "call_secret", "read", "tool", "in_progress"); err != nil {
		t.Fatal(err)
	}
	if err := session.WriteToolCallResult(victimState.GetPersistedSessionDir(), "call_secret", secret); err != nil {
		t.Fatal(err)
	}

	caller, err := mgr.HandleSessionNew(t.Context(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	// tool_calls/ is one segment deep inside the bundle, so two levels up land on
	// the sessions root and the next name is another bundle. The separators are
	// percent-encoded, which is how a traversal survives the mux: the pattern
	// matches one segment and the handler is handed the decoded value.
	traversal := "..%2F..%2F" + victim.SessionID + "%2Ftool_calls%2Fcall_secret"
	req := httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+caller.SessionID+"/tool-calls/"+traversal, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("a traversal id read another session's tool call: %s", rec.Body.String())
	}
}
