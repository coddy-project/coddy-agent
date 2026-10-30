//go:build http

package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func runningQueueInterruptStand(t *testing.T) *queueInterruptHTTPState {
	t.Helper()
	s := &queueInterruptHTTPState{}
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	if err := s.haveSession(); err != nil {
		t.Fatal(err)
	}
	if err := s.turnIsRunning(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestQueueInterruptHTTPRejectsInvalidBodyWithoutStopping(t *testing.T) {
	for _, body := range []string{
		`{`, `{"continue_queue":"true"}`, `{"continue_queue":1}`, `{"continue_queue":null}`,
		`[]`, `true`, `{} {}`, `{"continue_queue":true} trailing`,
		`{"ignored":"` + strings.Repeat("x", 1024) + `"}`, strings.Repeat(" ", 1025),
	} {
		t.Run(body[:min(len(body), 40)], func(t *testing.T) {
			s := runningQueueInterruptStand(t)
			if err := s.enqueueBatch(); err != nil {
				t.Fatal(err)
			}
			if err := s.cancel(body); err != nil {
				t.Fatal(err)
			}
			if s.lastStatus != http.StatusBadRequest || !strings.Contains(s.lastRaw, `"error"`) {
				t.Fatalf("invalid body answered %d: %s", s.lastStatus, s.lastRaw)
			}
			if s.child.Err() != nil || !s.srv.mgr.SessionTurnActiveInProcess(s.sessionID) {
				t.Fatal("invalid request cancelled the active turn")
			}
			rows, err := s.srv.mgr.QueuedTurnMessages(s.sessionID)
			if err != nil || len(rows) != 3 {
				t.Fatalf("invalid request changed the queue: %+v, %v", rows, err)
			}
			if exists, err := session.CancelRequestExists(s.srv.mgr.SessionByID(s.sessionID).GetPersistedSessionDir()); err != nil || exists {
				t.Fatalf("invalid request wrote cancel marker: %v, %v", exists, err)
			}
		})
	}
}

func TestQueueInterruptHTTPLegacyBodiesKeepFullStopSemantics(t *testing.T) {
	for _, body := range []string{"", " \n", "null", "{}", `{"continue_queue":false}`} {
		t.Run(body, func(t *testing.T) {
			s := runningQueueInterruptStand(t)
			if err := s.enqueueBatch(); err != nil {
				t.Fatal(err)
			}
			if err := s.cancel(body); err != nil {
				t.Fatal(err)
			}
			if s.lastStatus != http.StatusOK || len(s.lastBody) != 2 || s.lastBody["object"] != "coddy.session_cancelled" || s.lastBody["id"] != s.sessionID {
				t.Fatalf("legacy response changed: %d %s", s.lastStatus, s.lastRaw)
			}
			if err := queueInterruptAwait(s.unwinding, "full cancellation"); err != nil {
				t.Fatal(err)
			}
			if !s.srv.mgr.SessionByID(s.sessionID).IsUserCancelledTurn() {
				t.Fatal("legacy cancellation did not mark the full turn")
			}
			close(s.exitFirst)
			if err := queueInterruptAwait(s.turnDone, "full turn completion"); err != nil {
				t.Fatal(err)
			}
			rows, err := s.srv.mgr.QueuedTurnMessages(s.sessionID)
			if err != nil || len(rows) != 2 || rows[0].Text != "first" || rows[1].Text != "duplicate" || s.runs.Load() != 1 {
				t.Fatalf("legacy Stop must discard steer and retain after_turn without starting it: %+v, %v, runs=%d", rows, err, s.runs.Load())
			}
		})
	}
}

func TestQueueInterruptHTTPEmptyQueueFallsBackToFullStop(t *testing.T) {
	s := runningQueueInterruptStand(t)
	if err := s.cancel(`{"continue_queue":true}`); err != nil {
		t.Fatal(err)
	}
	if s.lastStatus != http.StatusOK || len(s.lastBody) != 2 {
		t.Fatalf("empty queue must use legacy response: %d %s", s.lastStatus, s.lastRaw)
	}
	if err := queueInterruptAwait(s.unwinding, "empty queue full cancellation"); err != nil {
		t.Fatal(err)
	}
	if !s.srv.mgr.SessionByID(s.sessionID).IsUserCancelledTurn() {
		t.Fatal("empty queue did not fall back to full cancellation")
	}
}

func TestQueueInterruptHTTPUnsupportedAdmissionKeepsLegacyQueueHandling(t *testing.T) {
	s := &queueInterruptHTTPState{}
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(s.turnDone) // This test admits directly, without a POST reader.
		s.close()
	})
	if err := s.haveSession(); err != nil {
		t.Fatal(err)
	}
	srv, id := s.srv, s.sessionID
	ctx, finish, err := srv.mgr.BeginTurn(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	for _, row := range []session.QueuedMessage{
		{Text: "first", Mode: session.QueueModeAfterTurn},
		{Text: "steer", Mode: session.QueueModeSteer},
		{Text: "last", Mode: session.QueueModeAfterTurn},
	} {
		if _, _, err := srv.mgr.EnqueueTurnMessageWithMode(id, row.Text, row.Mode, nil); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/coddy/sessions/"+id+"/cancel",
		strings.NewReader(`{"continue_queue":true}`)))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "queueContinued") || ctx.Err() != context.Canceled {
		t.Fatalf("unsupported admission must fully cancel, not promise continuation: %d %s, ctx=%v", w.Code, w.Body.String(), ctx.Err())
	}
	finish()
	rows, err := srv.mgr.QueuedTurnMessages(id)
	if err != nil || len(rows) != 2 || rows[0].Text != "first" || rows[1].Text != "last" {
		t.Fatalf("unsupported fallback must drop steer and retain after_turn: %+v, %v", rows, err)
	}
}

func TestQueueInterruptHTTPCrossProcessFallbackLoadsPersistedSession(t *testing.T) {
	s := runningQueueInterruptStand(t)
	if err := s.enqueueBatch(); err != nil {
		t.Fatal(err)
	}
	cfg := s.srv.activeCfg()
	other := session.NewManager(cfg, noopSender{}, nil, slog.Default(), s.root, s.srv.mgr.FileStore())
	srv := New(cfg, other, slog.Default(), s.root)
	t.Cleanup(srv.Drain)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/coddy/sessions/"+s.sessionID+"/cancel",
		strings.NewReader(`{"continue_queue":true}`)))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "queueContinued") {
		t.Fatalf("non-owning server must use legacy cancellation: %d %s", w.Code, w.Body.String())
	}
	if other.SessionByID(s.sessionID) == nil {
		t.Fatal("cancellation no longer loads a persisted session")
	}
	if err := queueInterruptAwait(s.unwinding, "cross-process cancellation"); err != nil {
		t.Fatal(err)
	}
	if !s.srv.mgr.SessionByID(s.sessionID).IsUserCancelledTurn() {
		t.Fatal("owning process did not observe the full-cancel marker")
	}
	close(s.exitFirst)
	if err := queueInterruptAwait(s.turnDone, "cross-process turn completion"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.srv.mgr.QueuedTurnMessages(s.sessionID)
	if err != nil || len(rows) != 2 || rows[0].Text != "first" || rows[1].Text != "duplicate" || s.runs.Load() != 1 {
		t.Fatalf("unsupported cross-process interruption changed legacy queue handling: %+v, %v, runs=%d", rows, err, s.runs.Load())
	}
}

func TestQueueInterruptHTTPOpenAPI(t *testing.T) {
	doc := openAPISpec()
	paths := doc["paths"].(map[string]interface{})
	op := paths["/coddy/sessions/{id}/cancel"].(map[string]interface{})["post"].(map[string]interface{})
	body, ok := op["requestBody"].(map[string]interface{})
	if !ok || body["required"] != false {
		t.Fatal("cancel must declare an optional request body")
	}
	schema := body["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	field := schema["properties"].(map[string]interface{})["continue_queue"].(map[string]interface{})
	if field["type"] != "boolean" || field["default"] != false {
		t.Fatalf("continue_queue schema = %+v", field)
	}
	response := op["responses"].(map[string]interface{})["200"].(map[string]interface{})
	schema = response["content"].(map[string]interface{})["application/json"].(map[string]interface{})["schema"].(map[string]interface{})
	field = schema["properties"].(map[string]interface{})["queueContinued"].(map[string]interface{})
	if field["type"] != "boolean" {
		t.Fatalf("queueContinued schema = %+v", field)
	}
	for _, required := range schema["required"].([]string) {
		if required == "queueContinued" {
			t.Fatal("queueContinued must be optional, absent on legacy cancellation")
		}
	}
	for _, text := range []string{"accepted", "ordinary", "direct", "plan", "permission", "legacy"} {
		if !strings.Contains(op["description"].(string), text) {
			t.Errorf("cancel description omits %q", text)
		}
	}
}

func TestQueueInterruptHTTPSecondStopCancelsUnreadBatch(t *testing.T) {
	s := runningQueueInterruptStand(t)
	if err := s.enqueueBatch(); err != nil {
		t.Fatal(err)
	}
	if err := s.requestContinuation(); err != nil {
		t.Fatal(err)
	}
	if err := s.onlyChildInterrupted(); err != nil {
		t.Fatal(err)
	}
	if err := s.cancel(`{"continue_queue":true}`); err != nil {
		t.Fatal(err)
	}
	if s.lastStatus != http.StatusOK || len(s.lastBody) != 2 {
		t.Fatalf("second Stop must use legacy response: %d %s", s.lastStatus, s.lastRaw)
	}
	if !s.srv.mgr.SessionByID(s.sessionID).IsUserCancelledTurn() {
		t.Fatal("second Stop did not fully cancel the root")
	}
	close(s.exitFirst)
	if err := queueInterruptAwait(s.turnDone, "second Stop completion"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.srv.mgr.QueuedTurnMessages(s.sessionID)
	if err != nil || len(rows) != 3 || rows[0].Text != "first" || rows[1].Text != "duplicate" || rows[2].Text != "duplicate" || len(rows[1].ImageParts) != 1 || s.runs.Load() != 1 {
		t.Fatalf("second Stop lost reserved rows or restarted the turn: %+v, %v, runs=%d", rows, err, s.runs.Load())
	}
}
