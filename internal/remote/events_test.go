package remote

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// A console attached over --remote is one client of a shared session: what
// someone queued in a browser reaches it as a message_queue frame on the
// server's event stream, and becomes the same session update a local manager
// would have published.
func TestEventsStreamDeliversTheMessageQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coddy/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		_, _ = fmt.Fprint(w, `event: message_queue`+"\n"+
			`data: {"object":"coddy.message_queue","sessionId":"sess_shared",`+
			`"messages":[{"id":"q_1","text":"check the Windows path too"}],"version":7}`+"\n\n")
		if fl != nil {
			fl.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	// Started explicitly, and stopped by Close: the subscription holds a
	// request open, so nothing may start one without owning its end.
	h.StartEvents()
	defer h.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sender.mu.Lock()
		updates := append([]interface{}(nil), sender.updates...)
		sender.mu.Unlock()
		for _, u := range updates {
			q, ok := u.(acp.MessageQueueUpdate)
			if !ok {
				continue
			}
			if q.SessionID != "sess_shared" {
				t.Fatalf("queue update for %q, want sess_shared", q.SessionID)
			}
			if q.Version != 7 {
				t.Fatalf("queue version = %d, want 7", q.Version)
			}
			if len(q.Messages) != 1 || q.Messages[0].Text != "check the Windows path too" {
				t.Fatalf("queue update carries %+v", q.Messages)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the events subscription never delivered the message queue")
}

// Close ends the subscription: a console that quits must not leave a request
// open against the server it was attached to.
func TestCloseStopsTheEventsSubscription(t *testing.T) {
	connects := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		<-r.Context().Done()
	}))
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(&collectSender{})
	h.StartEvents()
	select {
	case <-connects:
	case <-time.After(5 * time.Second):
		t.Fatal("the events subscription never connected")
	}

	done := make(chan struct{})
	go func() {
		h.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the events subscription")
	}
}

// Nothing starts the subscription on its own: a caller that only registers a
// sender (an embedder, a test) must not be left with an open request it never
// asked for and would never close.
func TestSetServerDoesNotStartTheEventsSubscription(t *testing.T) {
	connects := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(&collectSender{})
	select {
	case <-connects:
		t.Fatal("SetServer opened an events subscription by itself")
	case <-time.After(300 * time.Millisecond):
	}
}
