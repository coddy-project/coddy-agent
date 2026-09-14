package remote

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

type controlUpdate struct {
	sid  string
	body interface{}
}

type controlSender struct {
	collectSender
	ch chan controlUpdate
}

func (s *controlSender) SendSessionUpdate(sid string, body interface{}) error {
	s.ch <- controlUpdate{sid: sid, body: body}
	return s.collectSender.SendSessionUpdate(sid, body)
}

func (s *controlSender) SendControlUpdate(sid string, body any) error {
	s.ch <- controlUpdate{sid: sid, body: body}
	return nil
}

func controlHandler(t *testing.T, handler http.Handler) (*Handler, *controlSender) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	h, err := NewHandler(Options{BaseURL: srv.URL, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	s := &controlSender{ch: make(chan controlUpdate, 64)}
	h.SetServer(s)
	t.Cleanup(h.Close)
	return h, s
}

func awaitControl(t *testing.T, s *controlSender, match func(controlUpdate) bool) controlUpdate {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case u := <-s.ch:
			if match(u) {
				return u
			}
		case <-timer.C:
			t.Fatal("the remote backend did not publish the expected control update")
		}
	}
}

// Inspect the public sender boundary rather than depending on an ACP extension.
func activityValue(u interface{}) (bool, bool) {
	raw, _ := json.Marshal(u)
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false, false
	}
	value, ok := fields["turnActive"]
	var active bool
	if !ok || json.Unmarshal(value, &active) != nil {
		return false, false
	}
	return active, true
}

func TestRemoteActivityEventsNameOnlyTheAffectedSession(t *testing.T) {
	h, s := controlHandler(t, http.NotFoundHandler())
	h.session("sess_shared")
	h.session("sess_other")
	for _, event := range []string{"turn_started", "turn_ended"} {
		h.applyEventFrame(sseFrame{event: event, data: `{"sessionId":"sess_shared"}`})
		select {
		case u := <-s.ch:
			active, ok := activityValue(u.body)
			if !ok || u.sid != "sess_shared" || active != (event == "turn_started") {
				t.Fatalf("%s delivered %+v", event, u)
			}
		default:
			t.Fatalf("%s did not reach the console", event)
		}
	}
}

func TestRemoteReadyHydratesActivityAndQueue(t *testing.T) {
	h, s := controlHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/activity"):
			_, _ = io.WriteString(w, `{"sessionId":"sess_shared","turnActive":true}`)
		case strings.HasSuffix(r.URL.Path, "/queue"):
			_, _ = io.WriteString(w, `{"messages":[{"id":"q_7","text":"from browser"}],"version":7}`)
		default:
			_, _ = io.WriteString(w, `{"items":[]}`)
		}
	}))
	h.HandleSessionReady("sess_shared")
	seenActive, seenQueue := false, false
	awaitControl(t, s, func(u controlUpdate) bool {
		if active, ok := activityValue(u.body); ok && u.sid == "sess_shared" {
			seenActive = active
		}
		if q, ok := u.body.(QueueControlUpdate); ok {
			seenQueue = q.Version == 7 && len(q.Messages) == 1 && q.Messages[0].Text == "from browser"
		}
		return seenActive && seenQueue
	})
}

func TestRemoteActivityHydrationCannotOverwriteANewerEvent(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h, s := controlHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/activity"):
			once.Do(func() { close(started) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, `{"sessionId":"sess_shared","turnActive":true}`)
		case strings.HasSuffix(r.URL.Path, "/queue"):
			_, _ = io.WriteString(w, `{"messages":[],"version":9}`)
		default:
			_, _ = io.WriteString(w, `{"items":[]}`)
		}
	}))
	t.Cleanup(func() { close(release) })
	h.HandleSessionReady("sess_shared")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("session ready did not read activity")
	}
	h.applyEventFrame(sseFrame{event: "turn_ended", data: `{"sessionId":"sess_shared"}`})
	awaitControl(t, s, func(u controlUpdate) bool { active, ok := activityValue(u.body); return ok && !active })
	release <- struct{}{}
	awaitControl(t, s, func(u controlUpdate) bool {
		if active, ok := activityValue(u.body); ok && active {
			t.Fatal("stale activity REST answer resurrected the ended turn")
		}
		_, ok := u.body.(QueueControlUpdate)
		return ok
	})
}

func TestRemoteReconnectReadyReconcilesAMissedEnd(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	h, s := controlHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/activity"):
			_, _ = fmt.Fprintf(w, `{"sessionId":"sess_shared","turnActive":%t}`, active.Load())
		case strings.HasSuffix(r.URL.Path, "/queue"):
			_, _ = io.WriteString(w, `{"messages":[],"version":8}`)
		default:
			_, _ = io.WriteString(w, `{"items":[]}`)
		}
	}))
	h.HandleSessionReady("sess_shared")
	awaitControl(t, s, func(u controlUpdate) bool { v, ok := activityValue(u.body); return ok && v })
	active.Store(false)
	h.applyEventFrame(sseFrame{event: "ready", data: `{}`})
	awaitControl(t, s, func(u controlUpdate) bool { v, ok := activityValue(u.body); return ok && !v })
}

func TestRemoteQueueRESTRepliesPublishTheirVersions(t *testing.T) {
	for _, op := range []string{"enqueue", "list", "drop", "clear"} {
		t.Run(op, func(t *testing.T) {
			h, s := controlHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"message":{"id":"q_42","text":"follow-up"},"messages":[],"version":42}`)
			}))
			var err error
			switch op {
			case "enqueue":
				_, _, err = h.EnqueueTurnMessage("sess_shared", "follow-up")
			case "list":
				_, err = h.QueuedTurnMessages("sess_shared")
			case "drop":
				_, err = h.CancelQueuedTurnMessage("sess_shared", "q_42")
			case "clear":
				err = h.ClearQueuedTurnMessages("sess_shared")
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case u := <-s.ch:
				q, ok := u.body.(QueueControlUpdate)
				if !ok || u.sid != "sess_shared" || q.Version != 42 || q.Messages == nil {
					t.Fatalf("queue reply lost its version/empty snapshot: %+v", u)
				}
			default:
				t.Fatal("the REST queue snapshot never reached the versioned update boundary")
			}
		})
	}
}

func TestRemoteCompetingPromptKeepsFirstCancellation(t *testing.T) {
	var posts atomic.Int32
	h, _ := controlHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if posts.Add(1) > 1 {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"error":{"message":"session busy"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: permission\ndata: {\"sessionId\":\"sess_shared\",\"toolCall\":{\"toolCallId\":\"p1\"}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	s := &blockingPermissionSender{started: make(chan struct{})}
	h.SetServer(s)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan *acp.SessionPromptResult, 1)
	params := acp.SessionPromptParams{SessionID: "sess_shared", Prompt: []acp.ContentBlock{{Type: "text", Text: "one"}}}
	go func() { res, _ := h.HandleSessionPromptWithSender(ctx, params, s, nil); done <- res }()
	select {
	case <-s.started:
	case <-time.After(3 * time.Second):
		t.Fatal("first prompt did not reach its permission wait")
	}
	if _, err := h.HandleSessionPromptWithSender(ctx, params, s, nil); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("second prompt should be refused as busy, got %v", err)
	}
	h.HandleSessionCancel(acp.SessionCancelParams{SessionID: params.SessionID})
	h.WaitCancels(3 * time.Second)
	select {
	case res := <-done:
		if res == nil || res.StopReason != acp.StopReasonCancelled {
			t.Fatalf("first prompt result = %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the rejected second prompt cleared the first prompt's cancellation hook")
	}
	if posts.Load() != 1 {
		t.Fatalf("competing local prompt reached the server: %d posts", posts.Load())
	}
}

type cancelWaitSender struct {
	controlSender
	permissionCtx chan context.Context
}

func (s *cancelWaitSender) RequestPermission(ctx context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.permissionCtx <- ctx
	<-ctx.Done()
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

func TestRemoteFailedCancelKeepsTheTurnRetryable(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	h, _ := controlHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			if fail.Load() {
				http.Error(w, "stop unavailable", http.StatusServiceUnavailable)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
			return
		}
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: permission\ndata: {\"sessionId\":\"sess_shared\",\"toolCall\":{\"toolCallId\":\"p1\"}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	s := &cancelWaitSender{controlSender: controlSender{ch: make(chan controlUpdate, 8)}, permissionCtx: make(chan context.Context, 1)}
	h.SetServer(s)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan *acp.SessionPromptResult, 1)
	go func() {
		res, _ := h.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{SessionID: "sess_shared"}, s, nil)
		done <- res
	}()
	var turnCtx context.Context
	select {
	case turnCtx = <-s.permissionCtx:
	case <-time.After(3 * time.Second):
		t.Fatal("prompt did not start")
	}
	h.HandleSessionCancel(acp.SessionCancelParams{SessionID: "sess_shared"})
	h.WaitCancels(3 * time.Second)
	if turnCtx.Err() != nil {
		t.Fatal("a failed server cancel already aborted the local turn")
	}
	awaitControl(t, &s.controlSender, func(u controlUpdate) bool { return strings.Contains(fmt.Sprintf("%+v", u.body), "stop unavailable") })
	fail.Store(false)
	h.HandleSessionCancel(acp.SessionCancelParams{SessionID: "sess_shared"})
	h.WaitCancels(3 * time.Second)
	select {
	case res := <-done:
		if res == nil || res.StopReason != acp.StopReasonCancelled {
			t.Fatalf("retry result = %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("successful retry did not cancel the original turn")
	}
}
