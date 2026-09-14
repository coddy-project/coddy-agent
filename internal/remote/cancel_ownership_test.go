package remote

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func TestRemoteCancelAcknowledgementCannotAbortALaterLocalTurn(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	h, sender := controlHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(func() { close(release) })
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	st := h.session("sess_shared")
	first, err := h.beginTurn(st, firstCancel, sender)
	if err != nil {
		t.Fatal(err)
	}
	h.HandleSessionCancel(acp.SessionCancelParams{SessionID: "sess_shared"})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel request did not reach the server")
	}
	if firstCtx.Err() != nil {
		t.Fatal("local stream aborted before the server acknowledged Stop")
	}
	// The old request finishes naturally while its Stop is still in flight.
	h.endTurn(st, first)
	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	second, err := h.beginTurn(st, secondCancel, sender)
	if err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	h.WaitCancels(3 * time.Second)
	if secondCtx.Err() != nil {
		t.Fatal("old acknowledgement aborted the later request")
	}
	// Even a deferred second cleanup of the old request must leave the new
	// admission intact, so a third same-session prompt still gets refused.
	h.endTurn(st, first)
	if _, err := h.beginTurn(st, func() {}, sender); err == nil {
		t.Fatal("old cleanup released the newer admission")
	}
	h.endTurn(st, second)
}

type controlTransport func(*http.Request) (*http.Response, error)

func (f controlTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRemoteCancelRequestHasAShortDeadline(t *testing.T) {
	deadlines := make(chan time.Duration, 1)
	h, err := NewHandler(Options{BaseURL: "http://remote.invalid", HTTPClient: &http.Client{Transport: controlTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			deadlines <- -1
		} else {
			deadlines <- time.Until(deadline)
		}
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("try again")), Header: make(http.Header)}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.HandleSessionCancel(acp.SessionCancelParams{SessionID: "sess_shared"})
	h.WaitCancels(3 * time.Second)
	select {
	case duration := <-deadlines:
		if duration <= 0 || duration > 5*time.Second {
			t.Fatalf("cancel request deadline = %v, want at most 5s", duration)
		}
	default:
		t.Fatal("cancel request did not run")
	}
}

func TestRemoteCloseStopsQueueRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*Handler) error
	}{
		{"enqueue", func(h *Handler) error { _, _, err := h.EnqueueTurnMessage("sess_shared", "follow-up"); return err }},
		{"list", func(h *Handler) error { _, err := h.QueuedTurnMessages("sess_shared"); return err }},
		{"drop", func(h *Handler) error { _, err := h.CancelQueuedTurnMessage("sess_shared", "q_1"); return err }},
		{"clear", func(h *Handler) error { return h.ClearQueuedTurnMessages("sess_shared") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			h, err := NewHandler(Options{BaseURL: "http://remote.invalid", HTTPClient: &http.Client{Transport: controlTransport(func(r *http.Request) (*http.Response, error) {
				close(started)
				select {
				case <-r.Context().Done():
				case <-release:
				}
				return nil, context.Canceled
			})}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(h.Close)
			done := make(chan error, 1)
			exited := make(chan struct{})
			go func() { defer close(exited); done <- tc.run(h) }()
			t.Cleanup(func() { close(release); <-exited })
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("queue request did not start")
			}
			h.Close()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled queue request returned %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Close left the queue HTTP request running")
			}
		})
	}
}

func TestRemoteCloseStopsControlHydration(t *testing.T) {
	for _, path := range []string{"/activity", "/queue"} {
		t.Run(path, func(t *testing.T) {
			started, stopped, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			t.Cleanup(func() { close(release) })
			h, err := NewHandler(Options{BaseURL: "http://remote.invalid", HTTPClient: &http.Client{Transport: controlTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, path) {
					close(started)
					defer close(stopped)
					select {
					case <-r.Context().Done():
					case <-release:
					}
					return nil, context.Canceled
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"turnActive":true,"messages":[],"version":1}`)), Header: make(http.Header)}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(h.Close)
			h.session("sess_shared")
			h.RefreshSessionState("sess_shared")
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("hydration request did not start")
			}
			h.Close()
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("Close left the hydration HTTP request running")
			}
		})
	}
}

func TestRemoteActivityDoesNotExtendACPWireUpdates(t *testing.T) {
	h, _ := controlHandler(t, http.NotFoundHandler())
	sender := &collectSender{}
	h.SetServer(sender)
	h.session("sess_shared")
	h.applyEventFrame(sseFrame{event: "turn_started", data: `{"sessionId":"sess_shared"}`})
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.updates) != 0 {
		t.Fatalf("backend-only activity leaked into ACP: %+v", sender.updates)
	}
}

func TestRemoteCancelFailureUsesExistingACPMessageType(t *testing.T) {
	h, _ := controlHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "stop unavailable", http.StatusServiceUnavailable)
	}))
	sender := &collectSender{}
	h.SetServer(sender)
	h.HandleSessionCancel(acp.SessionCancelParams{SessionID: "sess_shared"})
	h.WaitCancels(3 * time.Second)
	if got := strings.Join(sender.texts(), "\n"); !strings.Contains(got, "stop unavailable") {
		t.Fatalf("ordinary ACP sender did not receive a readable cancel error: %q", got)
	}
}
