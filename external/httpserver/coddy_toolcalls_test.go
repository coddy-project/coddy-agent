//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
