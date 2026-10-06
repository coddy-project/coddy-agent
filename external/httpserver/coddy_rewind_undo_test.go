//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// newRewindUndoServer serves a session with two stored turns behind a runner
// that records the prompt and then waits until the turn is cancelled, so a
// test can undo while the edited turn is still running.
func newRewindUndoServer(t *testing.T) (*httptest.Server, *session.Manager, string, chan struct{}) {
	t.Helper()
	root := t.TempDir()
	started := make(chan struct{}, 4)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		var text string
		for _, b := range prompt {
			text += b.Text
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text})
		started <- struct{}{}
		<-ctx.Done()
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "partial"})
		return string(acp.StopReasonCancelled), nil
	}
	cfg := &config.Config{
		Paths:  config.Paths{Home: filepath.Join(root, "home"), CWD: root},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, store)
	srv := New(cfg, mgr, slog.Default(), root)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Drain()
	})
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatalf("session new: %v", err)
	}
	// A turn a failing test left running would keep the server from closing.
	t.Cleanup(func() { mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: res.SessionID}) })
	st := mgr.SessionByID(res.SessionID)
	for i := 0; i < 2; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("ask %d", i)})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: fmt.Sprintf("answer %d", i)})
	}
	if err := store.Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	return ts, mgr, res.SessionID, started
}

func postJSON(t *testing.T, url string, payload interface{}) (int, map[string]interface{}) {
	t.Helper()
	var buf []byte
	if payload != nil {
		buf, _ = json.Marshal(payload)
	}
	res, err := http.Post(url, "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	var body map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res.StatusCode, body
}

func TestRewindUndoStopsTheEditedTurn(t *testing.T) {
	ts, mgr, sid, started := newRewindUndoServer(t)
	if status, body := postJSON(t, ts.URL+"/coddy/sessions/"+sid+"/rewind", map[string]int{"userMessageIndex": 1}); status != http.StatusOK {
		t.Fatalf("rewind: %d %v", status, body)
	}

	// The edited prompt starts a turn that only ends when it is cancelled.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf, _ := json.Marshal(map[string]interface{}{"model": "agent", "input": "edited", "stream": false})
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", bytes.NewReader(buf))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", sid)
		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the edited turn did not start")
	}

	status, body := postJSON(t, ts.URL+"/coddy/sessions/"+sid+"/rewind/undo", nil)
	if status != http.StatusOK {
		t.Fatalf("undo: %d %v", status, body)
	}
	if body["object"] != "coddy.session_rewind_undone" || body["sessionId"] != sid {
		t.Fatalf("unexpected undo body: %v", body)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the edited turn kept running after the undo")
	}
	msgs := mgr.SessionByID(sid).GetMessages()
	want := []string{"ask 0", "answer 0", "ask 1", "answer 1"}
	if len(msgs) != len(want) {
		t.Fatalf("expected the original history, got %+v", msgs)
	}
	for i, m := range msgs {
		if m.Content != want[i] {
			t.Fatalf("history[%d] = %q, want %q (all: %+v)", i, m.Content, want[i], msgs)
		}
	}
}

func TestRewindUndoWithNothingToUndo(t *testing.T) {
	ts, _, sid, _ := newRewindUndoServer(t)
	status, body := postJSON(t, ts.URL+"/coddy/sessions/"+sid+"/rewind/undo", nil)
	if status != http.StatusConflict {
		t.Fatalf("undo without a rewind: want 409, got %d %v", status, body)
	}
}

func TestRewindUndoRejectsBadSessionID(t *testing.T) {
	ts, _, _, _ := newRewindUndoServer(t)
	status, _ := postJSON(t, ts.URL+"/coddy/sessions/..%2Fetc/rewind/undo", nil)
	if status != http.StatusBadRequest && status != http.StatusNotFound {
		t.Fatalf("want 400 or 404 for a traversal id, got %d", status)
	}
}
