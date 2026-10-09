package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// printServer is a remote coddy serve that knows one stored session and
// records what the print-mode client asks of it.
type printServer struct {
	mu        sync.Mutex
	origins   map[string]string // X-Coddy-Session-Origin per prompted session
	listQuery []string
	deleted   []string
	rows      []map[string]any
	ts        *httptest.Server
}

func newPrintServer(t *testing.T, storedID string) *printServer {
	t.Helper()
	p := &printServer{origins: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"stub/m","owned_by":"stub","default":true}]}`))
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.origins[r.Header.Get("X-Coddy-Session-ID")] = r.Header.Get("X-Coddy-Session-Origin")
		p.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	mux.HandleFunc("GET /coddy/sessions", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.listQuery = append(p.listQuery, r.URL.RawQuery)
		rows := p.rows
		p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"sessions": rows})
	})
	mux.HandleFunc("GET /coddy/sessions/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != storedID {
			http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"messages":[]}`))
	})
	mux.HandleFunc("DELETE /coddy/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.deleted = append(p.deleted, r.PathValue("id"))
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	p.ts = httptest.NewServer(mux)
	t.Cleanup(p.ts.Close)
	return p
}

func (p *printServer) originOf(id string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.origins[id]
	return v, ok
}

func promptRemote(t *testing.T, h *Handler, id string) {
	t.Helper()
	sender := &collectSender{}
	h.SetServer(sender)
	if _, err := h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: id,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
	}, sender, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRemotePrintRunAsksTheServerToMarkTheSessionItCreates(t *testing.T) {
	srv := newPrintServer(t, "sess_stored")
	h, err := NewHandler(Options{BaseURL: srv.ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	h.SetNextSessionOrigin(session.PrintOrigin)
	res, err := h.HandleSessionNew(ctx, acp.SessionNewParams{})
	if err != nil {
		t.Fatal(err)
	}
	promptRemote(t, h, res.SessionID)
	if got, _ := srv.originOf(res.SessionID); got != session.PrintOrigin {
		t.Fatalf("fresh session prompt carried origin %q, want %q", got, session.PrintOrigin)
	}

	// A stored session the run continues is not the run's to label, and the
	// mark is spent on the session it was meant for.
	h.SetPreferredSessionID("sess_stored")
	h.SetNextSessionOrigin(session.PrintOrigin)
	again, err := h.HandleSessionNew(ctx, acp.SessionNewParams{})
	if err != nil {
		t.Fatal(err)
	}
	promptRemote(t, h, again.SessionID)
	if got, ok := srv.originOf("sess_stored"); !ok || got != "" {
		t.Fatalf("stored session prompt carried origin %q (seen %v)", got, ok)
	}
	plain, err := h.HandleSessionNew(ctx, acp.SessionNewParams{})
	if err != nil {
		t.Fatal(err)
	}
	promptRemote(t, h, plain.SessionID)
	if got, _ := srv.originOf(plain.SessionID); got != "" {
		t.Fatalf("a later session carried origin %q", got)
	}
}

func TestRemoteLatestSessionAsksForPrintRunsOnlyWhenTold(t *testing.T) {
	srv := newPrintServer(t, "")
	// The server lists pinned sessions first whatever the order: the latest is
	// the newest by its stamp, not the first row.
	srv.rows = []map[string]any{
		{"id": "sess_pinned", "updatedAt": "2026-10-01T10:00:00Z", "pinned": true},
		{"id": "sess_newest", "updatedAt": "2026-10-09T10:00:00Z"},
		{"id": "sess_older", "updatedAt": "2026-10-05T10:00:00Z"},
	}
	h, err := NewHandler(Options{BaseURL: srv.ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	id, err := h.LatestSessionID(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if id != "sess_newest" {
		t.Fatalf("latest = %q, want sess_newest", id)
	}
	if _, err := h.LatestSessionID(ctx, false); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	queries := append([]string(nil), srv.listQuery...)
	srv.mu.Unlock()
	if len(queries) != 2 || !strings.Contains(queries[0], "include_print=true") || strings.Contains(queries[1], "include_print") {
		t.Fatalf("list queries = %q", queries)
	}

	srv.rows = nil
	if _, err := h.LatestSessionID(ctx, true); err == nil {
		t.Fatal("an empty server must be an error")
	}
}

func TestRemoteDeleteSessionRemovesItOnTheServer(t *testing.T) {
	srv := newPrintServer(t, "")
	h, err := NewHandler(Options{BaseURL: srv.ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteSession(context.Background(), "sess_gone"); err != nil {
		t.Fatal(err)
	}
	if len(srv.deleted) != 1 || srv.deleted[0] != "sess_gone" {
		t.Fatalf("deleted = %v", srv.deleted)
	}
}
