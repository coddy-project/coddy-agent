//go:build cli

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const sharedControlSession = "sess_shared"

type controlRequest struct{ path, text string }

// This stand speaks only existing REST/SSE routes. The test goroutine alone
// drives App keys and applies updates, so ordering never depends on a render tick.
type remoteControlStand struct {
	app         *App
	h           *remote.Handler
	events      chan string
	requests    chan controlRequest
	mu          sync.Mutex
	active      bool
	rows        []session.QueuedMessage
	version     uint64
	promptPosts int
	queueError  string
	cancelError bool
	postGate    chan struct{}
	postEntered chan struct{}
}

func newRemoteControlStand(t *testing.T) *remoteControlStand {
	t.Helper()
	return newRemoteControlStandOver(t, nil)
}

// newRemoteControlStandOver puts wrap(handler) between the app and the server
// when wrap is set. The wrapper has to be in place before Start: Start launches
// workers that read the backend (the task list read), so swapping it afterwards
// races with them.
func newRemoteControlStandOver(t *testing.T, wrap func(backend) backend) *remoteControlStand {
	t.Helper()
	f := &remoteControlStand{events: make(chan string, 32), requests: make(chan controlRequest, 32), version: 1}
	srv := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(srv.Close)
	var err error
	f.app, err = buildRemoteApp(&config.Config{Paths: config.Paths{CWD: t.TempDir()}}, &remote.Options{
		BaseURL: srv.URL, Log: slog.New(slog.DiscardHandler),
	}, slog.New(slog.DiscardHandler), &bddTerminal{cols: 100, rows: 30}, "dark", true)
	if err != nil {
		t.Fatal(err)
	}
	f.h = f.app.mgr.(*remote.Handler)
	if wrap != nil {
		f.app.mgr = wrap(f.h)
	}
	t.Cleanup(func() {
		f.app.Close()
		f.h.Close()
		f.app.JoinWorkers(3 * time.Second)
		f.h.WaitCancels(3 * time.Second)
		f.app.stopSpinner()
	})
	if err := f.app.Start(context.Background(), sharedControlSession, false); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *remoteControlStand) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/coddy/events" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		for {
			select {
			case frame := <-f.events:
				_, _ = io.WriteString(w, frame)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/v1/models":
		_, _ = io.WriteString(w, `{"data":[{"id":"test","default":true,"owned_by":"stub"}]}`)
	case strings.HasSuffix(r.URL.Path, "/messages"):
		_, _ = io.WriteString(w, `{"messages":[]}`)
	case strings.HasSuffix(r.URL.Path, "/activity"):
		f.mu.Lock()
		active := f.active
		f.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"sessionId":%q,"turnActive":%t}`, sharedControlSession, active)
	case strings.Contains(r.URL.Path, "/queue"):
		f.serveQueue(w, r)
	case strings.HasSuffix(r.URL.Path, "/cancel"):
		f.mu.Lock()
		failed := f.cancelError
		if !failed {
			f.active = false
			f.rows = nil
			f.version++
		}
		version := f.version
		f.mu.Unlock()
		if failed {
			http.Error(w, "stop unavailable", http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
			f.events <- controlFrame("turn_ended", `{"sessionId":"sess_shared"}`) + controlQueueFrame(sharedControlSession, nil, version)
		}
		f.requests <- controlRequest{path: "cancel"}
	case r.URL.Path == "/v1/responses":
		f.mu.Lock()
		f.promptPosts++
		active := f.active
		f.mu.Unlock()
		if active {
			http.Error(w, `{"error":{"message":"session busy"}}`, http.StatusConflict)
		} else {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}
		f.requests <- controlRequest{path: "prompt"}
	default:
		_, _ = io.WriteString(w, `{"items":[]}`)
	}
}

func (f *remoteControlStand) serveQueue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	f.mu.Lock()
	code := f.queueError
	gate, entered := f.postGate, f.postEntered
	var added *session.QueuedMessage
	if r.Method == http.MethodPost && code == "" {
		row := session.QueuedMessage{ID: fmt.Sprintf("q_%d", f.version+1), Text: body.Text}
		added = &row
		f.rows = append(f.rows, row)
		f.version++
	}
	if r.Method == http.MethodDelete {
		f.rows = nil
		f.version++
	}
	rows, version := append([]session.QueuedMessage(nil), f.rows...), f.version
	f.mu.Unlock()
	if r.Method == http.MethodPost {
		f.requests <- controlRequest{path: "queue", text: body.Text}
		if entered != nil {
			close(entered)
		}
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if code != "" {
			w.WriteHeader(http.StatusConflict)
			_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, code, code)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"messages": rows, "version": version, "message": added})
}

func controlFrame(event, body string) string { return "event: " + event + "\ndata: " + body + "\n\n" }
func controlQueueFrame(sid string, rows []session.QueuedMessage, version uint64) string {
	body, _ := json.Marshal(map[string]interface{}{"sessionId": sid, "messages": rows, "version": version})
	return controlFrame("message_queue", string(body))
}

func pumpControls(t *testing.T, a *App, done func(updateMsg) bool) {
	t.Helper()
	if err := awaitControls(a, done); err != nil {
		t.Fatal(err)
	}
}

func awaitControls(a *App, done func(updateMsg) bool) error {
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
			if done(msg) {
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("expected control update did not arrive; transcript:\n%s", transcriptText(a))
		}
	}
}

func (f *remoteControlStand) syncEvents(t *testing.T, frames string) {
	t.Helper()
	f.events <- frames + controlQueueFrame("sess_barrier", nil, 1)
	pumpControls(t, f.app, func(msg updateMsg) bool { return msg.sessionID == "sess_barrier" })
}

func (f *remoteControlStand) turnEvent(t *testing.T, sid string, active bool) {
	t.Helper()
	event := "turn_ended"
	if active {
		event = "turn_started"
	}
	f.syncEvents(t, controlFrame(event, fmt.Sprintf(`{"sessionId":%q}`, sid)))
}

func (f *remoteControlStand) request(t *testing.T, want string) controlRequest {
	t.Helper()
	got, err := f.awaitRequest(want)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (f *remoteControlStand) awaitRequest(want string) (controlRequest, error) {
	select {
	case got := <-f.requests:
		if got.path != want {
			return got, fmt.Errorf("console sent %q, want %q", got.path, want)
		}
		return got, nil
	case <-time.After(3 * time.Second):
		return controlRequest{}, fmt.Errorf("console never sent %q", want)
	}
}

func drainControls(a *App) {
	for {
		select {
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
		default:
			return
		}
	}
}

func TestRemoteControlsExternalTurnQueuesNormalInput(t *testing.T) {
	f := newRemoteControlStand(t)
	f.mu.Lock()
	f.active = true
	f.mu.Unlock()
	f.turnEvent(t, sharedControlSession, true)
	f.app.editor.SetText("check Windows too")
	f.app.dispatchInput([]byte("\r"))
	if got := f.request(t, "queue"); got.text != "check Windows too" {
		t.Fatalf("queued text = %q", got.text)
	}
	pumpControls(t, f.app, func(updateMsg) bool { return len(f.app.queue.Rows()) == 1 })
	if f.app.turnActive {
		t.Fatal("observing a browser turn acquired a local prompt lifecycle")
	}
}

func TestRemoteControlsHydratedTurnAcceptsEscape(t *testing.T) {
	f := newRemoteControlStand(t)
	f.mu.Lock()
	f.active = true
	f.rows = []session.QueuedMessage{{ID: "q_browser", Text: "browser follow-up"}}
	f.version = 8
	f.mu.Unlock()
	f.h.HandleSessionReady(sharedControlSession)
	pumpControls(t, f.app, func(updateMsg) bool { return len(f.app.queue.Rows()) == 1 })
	if f.app.turnActive {
		t.Fatal("hydration must not claim a local request")
	}
	if !f.app.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("Escape ignored the browser-owned turn")
	}
	f.request(t, "cancel")
}

type recordingControlBackend struct {
	backend
	cancelled []string
}

// over puts b in front of inner, in the shape newRemoteControlStandOver takes.
func (b *recordingControlBackend) over(inner backend) backend {
	b.backend = inner
	return b
}

func (b *recordingControlBackend) HandleSessionCancel(p acp.SessionCancelParams) {
	b.cancelled = append(b.cancelled, p.SessionID)
}

func TestRemoteControlsEndedEventsPreserveOwnRequestAndSessionScope(t *testing.T) {
	b := &recordingControlBackend{}
	f := newRemoteControlStandOver(t, b.over)
	f.turnEvent(t, sharedControlSession, true)
	f.turnEvent(t, "sess_other", false)
	if !f.app.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("another session's end disabled Escape")
	}
	f.app.turnActive, f.app.turnSessionID = true, "sess_owned"
	f.turnEvent(t, sharedControlSession, false)
	if !f.app.turnActive || f.app.turnSessionID != "sess_owned" {
		t.Fatal("an observed end released a different owned request")
	}
	if f.app.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("Escape cancelled an owned request in a different session")
	}
	f.app.turnSessionID = sharedControlSession
	f.turnEvent(t, sharedControlSession, false)
	if !f.app.turnActive {
		t.Fatal("an observed end released the local request before turnDone")
	}
	f.app.applyLoopMessage(updateMsg{sessionID: sharedControlSession, update: turnDone{sessionID: sharedControlSession}})
	if f.app.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("ended activity left Escape enabled")
	}
}

func TestRemoteControlsQueueReplyDoesNotBlockInputOrRegressVersion(t *testing.T) {
	f := newRemoteControlStand(t)
	gate, entered := make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate) }) }
	t.Cleanup(unblock)
	f.mu.Lock()
	f.postGate, f.postEntered = gate, entered
	f.mu.Unlock()
	f.app.turnActive, f.app.turnSessionID = true, sharedControlSession
	f.app.editor.SetText("older REST result")
	submitted := make(chan struct{})
	go func() { f.app.dispatchInput([]byte("\r")); close(submitted) }()
	select {
	case <-submitted:
	case <-time.After(3 * time.Second):
		unblock()
		<-submitted
		t.Fatal("queue submission blocked the UI goroutine on HTTP")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("queue request never started")
	}
	f.syncEvents(t, controlQueueFrame(sharedControlSession, []session.QueuedMessage{{ID: "q_new", Text: "newer SSE result"}}, 100))
	unblock()
	f.app.workers.Wait()
	drainControls(f.app)
	rows := f.app.queue.Rows()
	if len(rows) != 1 || rows[0].ID != "q_new" || f.app.queue.version != 100 {
		t.Fatalf("stale queue REST overwrote SSE: version=%d rows=%+v", f.app.queue.version, rows)
	}
}

func TestRemoteControlsQueueRefusalPreservesDraftAndAdmission(t *testing.T) {
	for _, code := range []string{"no_active_turn", "queue_full"} {
		t.Run(code, func(t *testing.T) {
			f := newRemoteControlStand(t)
			f.mu.Lock()
			f.queueError = code
			f.mu.Unlock()
			f.app.turnActive, f.app.turnSessionID = true, sharedControlSession
			f.app.editor.SetText("keep this draft")
			f.app.dispatchInput([]byte("\r"))
			f.app.workers.Wait()
			drainControls(f.app)
			if !f.app.turnActive {
				t.Fatal("queue refusal released the owned request before admission finished")
			}
			if got := f.app.editor.PendingText(); got != "keep this draft" {
				t.Fatalf("queue refusal lost the draft: %q", got)
			}
			f.mu.Lock()
			posts := f.promptPosts
			f.mu.Unlock()
			if posts != 0 {
				t.Fatalf("queue refusal started %d duplicate prompts before turnDone", posts)
			}
		})
	}
}

func TestRemoteControlsQueueVersionsResetOnlyForAnotherSession(t *testing.T) {
	a := newRemoteControlStand(t).app
	a.sessionID = sharedControlSession
	a.queue.Apply([]acp.QueuedMessage{{ID: "old"}}, 100)
	a.queue.Apply(nil, 0)
	if len(a.queue.Rows()) != 1 {
		t.Fatal("version-zero REST snapshot erased a newer event")
	}
	a.adoptSession("sess_other", nil, nil)
	a.queue.Apply([]acp.QueuedMessage{{ID: "new"}}, 2)
	if rows := a.queue.Rows(); len(rows) != 1 || rows[0].ID != "new" {
		t.Fatalf("new session inherited old queue version: %+v", rows)
	}
}

func TestRemoteControlsFailedCancelIsVisibleAndRetryable(t *testing.T) {
	f := newRemoteControlStand(t)
	f.mu.Lock()
	f.active = true
	f.cancelError = true
	f.mu.Unlock()
	f.turnEvent(t, sharedControlSession, true)
	if !f.app.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("Escape ignored external activity")
	}
	f.request(t, "cancel")
	f.h.WaitCancels(3 * time.Second)
	drainControls(f.app)
	if text := transcriptText(f.app); !strings.Contains(text, "stop unavailable") || strings.Contains(text, "Interrupted") {
		t.Fatalf("cancel failure was hidden or reported as stopped:\n%s", text)
	}
	f.mu.Lock()
	f.cancelError = false
	f.mu.Unlock()
	if !f.app.handleGlobalKey([]byte("\x1b")) {
		t.Fatal("failed cancel disabled retry")
	}
	f.request(t, "cancel")
}
