//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
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

// pagedSession starts a server over a session holding turns prompts, each
// answered by one tool step and a closing answer, with a notice logged at the
// end of every turn.
func pagedSession(t *testing.T, turns int) (*Server, string) {
	t.Helper()
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
	for i := 1; i <= turns; i++ {
		id := fmt.Sprintf("call_%d", i)
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("prompt %d", i)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read_file", InputJSON: `{"path":"a.go"}`}}})
		st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: fmt.Sprintf("result %d", i)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
		st.AppendUILogNotice(i, fmt.Sprintf("notice %d", i))
	}
	return srv, created.SessionID
}

type pagedMessagesBody struct {
	Messages []struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
	} `json:"messages"`
	Window struct {
		Offset         int `json:"offset"`
		Total          int `json:"total"`
		TurnsBefore    int `json:"turnsBefore"`
		UserRowsBefore int `json:"userRowsBefore"`
	} `json:"window"`
	UILog []struct {
		Message string `json:"message"`
	} `json:"uiLog"`
}

func getPaged(t *testing.T, srv *Server, path string) (*httptest.ResponseRecorder, pagedMessagesBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	var body pagedMessagesBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return rec, body
}

func TestSessionMessagesReadsAPage(t *testing.T) {
	srv, sid := pagedSession(t, 5)
	base := "/coddy/sessions/" + sid + "/messages"

	// The whole history, as before, now with the window it covers.
	rec, full := getPaged(t, srv, base)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(full.Messages) != 20 || full.Window.Offset != 0 || full.Window.Total != 20 {
		t.Fatalf("full read: %d messages, window %+v", len(full.Messages), full.Window)
	}
	if len(full.UILog) != 5 {
		t.Fatalf("full read uiLog = %+v", full.UILog)
	}

	// The newest page opens with the prompt of the last turn.
	rec, tail := getPaged(t, srv, base+"?limit=4")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(tail.Messages) != 4 || tail.Messages[0].Content != "prompt 5" {
		t.Fatalf("tail page = %+v", tail.Messages)
	}
	if tail.Window.Offset != 16 || tail.Window.Total != 20 || tail.Window.TurnsBefore != 4 || tail.Window.UserRowsBefore != 4 {
		t.Fatalf("tail window = %+v", tail.Window)
	}
	// Notice 4 ended the turn before the page, right above its first prompt.
	if len(tail.UILog) != 2 || tail.UILog[0].Message != "notice 4" || tail.UILog[1].Message != "notice 5" {
		t.Fatalf("tail uiLog = %+v", tail.UILog)
	}

	// The page before it ends where it starts and leaves notice 4 to it.
	rec, older := getPaged(t, srv, base+"?limit=4&before=16")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if older.Window.Offset != 12 || len(older.Messages) != 4 || older.Messages[0].Content != "prompt 4" {
		t.Fatalf("older page = %+v, window %+v", older.Messages, older.Window)
	}
	if len(older.UILog) != 1 || older.UILog[0].Message != "notice 3" {
		t.Fatalf("older uiLog = %+v", older.UILog)
	}

	// The same window again, from its start to the end.
	rec, again := getPaged(t, srv, base+"?from=12")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if again.Window.Offset != 12 || len(again.Messages) != 8 || again.Window.TurnsBefore != 3 {
		t.Fatalf("from read: %d messages, window %+v", len(again.Messages), again.Window)
	}
}

func TestSessionMessagesRejectsABadWindow(t *testing.T) {
	srv, sid := pagedSession(t, 2)
	base := "/coddy/sessions/" + sid + "/messages"
	for _, q := range []string{
		"?limit=0",
		"?limit=-3",
		"?limit=abc",
		"?limit=1001",
		"?before=-1&limit=2",
		"?before=x&limit=2",
		"?from=-1",
		"?from=1.5",
		"?from=2&limit=4",
		"?before=4",
	} {
		rec, _ := getPaged(t, srv, base+q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", q, rec.Code, rec.Body.String())
		}
	}
	// Positions past the history are clamped, not refused.
	rec, page := getPaged(t, srv, base+"?limit=3&before=999")
	if rec.Code != http.StatusOK || page.Window.Total != 8 || page.Window.Offset != 4 {
		t.Fatalf("clamped read: status %d, window %+v", rec.Code, page.Window)
	}
}

func TestToolCallListReadsTheCallsOfAPage(t *testing.T) {
	srv, sid := pagedSession(t, 4)
	get := func(q string) []string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+sid+"/tool-calls"+q, nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", q, rec.Code, rec.Body.String())
		}
		var body struct {
			ToolCalls []struct {
				ToolCallID string `json:"toolCallId"`
				Status     string `json:"status"`
			} `json:"toolCalls"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, c := range body.ToolCalls {
			ids = append(ids, c.ToolCallID+":"+c.Status)
		}
		return ids
	}
	if got := strings.Join(get(""), " "); got != "call_1:completed call_2:completed call_3:completed call_4:completed" {
		t.Fatalf("every call = %q", got)
	}
	if got := strings.Join(get("?from=8&to=16"), " "); got != "call_3:completed call_4:completed" {
		t.Fatalf("calls of [8,16) = %q", got)
	}
	if got := strings.Join(get("?from=12"), " "); got != "call_4:completed" {
		t.Fatalf("calls from 12 = %q", got)
	}
	for _, q := range []string{"?from=-1", "?to=x", "?from=a"} {
		req := httptest.NewRequest(http.MethodGet, "/coddy/sessions/"+sid+"/tool-calls"+q, nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, rec.Code)
		}
	}
}
