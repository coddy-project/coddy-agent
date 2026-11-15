package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type remoteSuggestionClient interface {
	GeneratePromptSuggestion(context.Context, string) (string, error)
	NotePromptSuggestionOutcome(string, bool)
}

func suggestionClient(t *testing.T, h *Handler) remoteSuggestionClient {
	t.Helper()
	client, ok := any(h).(remoteSuggestionClient)
	if !ok {
		t.Fatal("remote handler must fetch suggestions and report their outcomes")
	}
	return client
}

func TestRemotePromptSuggestionResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		wantError        bool
	}{
		{"hint", `{"sessionId":"sess_remote","text":"  run the tests  "}`, "run the tests", 200, false},
		{"suppressed", `{"sessionId":"sess_remote","text":""}`, "", 200, false},
		{"older server", `not found`, "", 404, false},
		{"unauthorized", `unauthorized`, "", 401, true},
		{"unavailable", `unavailable`, "", 503, true},
		{"invalid JSON", `{`, "", 200, true},
		{"wrong session", `{"sessionId":"sess_other","text":"run the tests"}`, "", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/node/coddy/sessions/sess_remote/prompt-suggestion" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("wrong authenticated suggestion request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			h, err := NewHandler(Options{BaseURL: srv.URL + "/node", Token: "test-token"})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			got, err := suggestionClient(t, h).GeneratePromptSuggestion(context.Background(), " sess_remote ")
			if got != tc.want || (err != nil) != tc.wantError {
				t.Fatalf("suggestion=%q error=%v, want %q error=%v", got, err, tc.want, tc.wantError)
			}
		})
	}
}

func TestRemotePromptSuggestionOutcomes(t *testing.T) {
	accepted := make(chan bool, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/coddy/sessions/sess_remote/prompt-suggestion/outcome" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("wrong outcome request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Accepted *bool `json:"accepted"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Accepted == nil {
			t.Error("outcome must include accepted, even when false")
			return
		}
		accepted <- *body.Accepted
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL, Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	client := suggestionClient(t, h)
	client.NotePromptSuggestionOutcome("sess_remote", true)
	client.NotePromptSuggestionOutcome("sess_remote", false)
	if len(accepted) != 2 {
		t.Fatal("server did not receive both outcomes")
	}
	if !<-accepted || <-accepted {
		t.Fatal("server received incorrect acceptance values")
	}
}

func TestRemotePromptSuggestionCancellation(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "handler close"}[shutdown], func(t *testing.T) {
			started := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-r.Context().Done()
			}))
			defer srv.Close()
			h, err := NewHandler(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			client := suggestionClient(t, h)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := client.GeneratePromptSuggestion(ctx, "sess_remote"); done <- err }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("request did not start")
			}
			if shutdown {
				h.Close()
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation returned %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("request was not cancelled")
			}
		})
	}
}

func TestRemotePromptSuggestionRejectsInvalidSession(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	client := suggestionClient(t, h)
	for _, id := range []string{"", "../other", "sess_remote?query=1"} {
		if _, err := client.GeneratePromptSuggestion(context.Background(), id); err == nil {
			t.Errorf("accepted invalid session %q", id)
		}
		client.NotePromptSuggestionOutcome(id, true)
	}
	if requests.Load() != 0 {
		t.Fatal("invalid session reached server")
	}
}

type suggestionConfigSender struct {
	collectSender
	resets int
}

func (s *suggestionConfigSender) SendControlUpdate(id string, update any) error {
	if _, ok := update.(ConfigReloadedUpdate); ok && id == "" {
		s.resets++
	}
	return nil
}

func TestRemotePromptSuggestionConfigEvents(t *testing.T) {
	h, err := NewHandler(Options{BaseURL: "http://unused.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	sender := &suggestionConfigSender{}
	h.SetServer(sender)
	for _, event := range []string{"config_reloaded", "ready"} {
		h.applyEventFrame(sseFrame{event: event, data: `{}`})
	}
	if sender.resets != 2 {
		t.Fatalf("config reload and reconnect sent %d resets, want 2", sender.resets)
	}
}

func TestRemotePromptSuggestionOutcomeStopsOnClose(t *testing.T) {
	started, done := make(chan struct{}), make(chan struct{})
	h, err := NewHandler(Options{BaseURL: "http://remote.invalid", HTTPClient: &http.Client{Transport: controlTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	go func() { h.NotePromptSuggestionOutcome("sess_remote", true); close(done) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("outcome request did not start")
	}
	h.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("outcome request outlived the handler")
	}
}
