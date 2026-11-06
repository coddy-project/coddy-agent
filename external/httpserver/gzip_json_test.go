//go:build http

package httpserver

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func gzipTestHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Length", "999")
		_, _ = io.WriteString(w, `{"rows":"`+strings.Repeat("session ", 500)+`"}`)
	})
	mux.HandleFunc("/text", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "event: ready\ndata: {}\n\n")
		fl.Flush()
		<-r.Context().Done()
	})
	return gzipJSON(mux)
}

func get(t *testing.T, h http.Handler, method, path, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A JSON answer goes out gzip-compressed to a client that accepts it, with
// no stale Content-Length, and decompresses to exactly what the handler wrote.
func TestGzipJSONCompressesJSONForAClientThatAcceptsIt(t *testing.T) {
	h := gzipTestHandler()
	plain := get(t, h, http.MethodGet, "/json", "")
	if plain.Header().Get("Content-Encoding") != "" {
		t.Fatalf("a client without Accept-Encoding got %q", plain.Header().Get("Content-Encoding"))
	}
	rec := get(t, h, http.MethodGet, "/json", "br, gzip;q=0.8")
	if rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("headers = %v, want gzip with Vary", rec.Header())
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Fatalf("a compressed answer kept Content-Length %q", rec.Header().Get("Content-Length"))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain.Body.Bytes()) {
		t.Fatalf("decompressed body differs from the plain one")
	}
	if rec.Body.Len() >= plain.Body.Len() {
		t.Fatalf("compressed %d bytes, plain %d", rec.Body.Len(), plain.Body.Len())
	}
}

// Everything that is not a JSON body stays as the handler wrote it.
func TestGzipJSONLeavesOtherAnswersAlone(t *testing.T) {
	h := gzipTestHandler()
	for _, tc := range []struct{ method, path, accept string }{
		{http.MethodGet, "/text", "gzip"},
		{http.MethodGet, "/empty", "gzip"},
		{http.MethodHead, "/json", "gzip"},
		{http.MethodGet, "/json", "gzip;q=0"},
		{http.MethodGet, "/json", "identity"},
	} {
		rec := get(t, h, tc.method, tc.path, tc.accept)
		if enc := rec.Header().Get("Content-Encoding"); enc != "" {
			t.Errorf("%s %s with %q: Content-Encoding %q, want none", tc.method, tc.path, tc.accept, enc)
		}
	}
}

// An event stream is never compressed, and its first event reaches the
// client while the stream is still open.
func TestGzipJSONKeepsEventStreamsLive(t *testing.T) {
	srv := httptest.NewServer(gzipTestHandler())
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.Header.Get("Content-Encoding") != "" {
		t.Fatalf("event stream Content-Encoding %q", res.Header.Get("Content-Encoding"))
	}
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil || line != "event: ready\n" {
		t.Fatalf("first line = %q, %v", line, err)
	}
}

// An upgrade request keeps the writer it was given: the browser extension's
// WebSocket asserts http.Hijacker on it, which the wrapped writer does not
// implement, and browsers send Accept-Encoding: gzip on the upgrade.
func TestGzipJSONLeavesUpgradeRequestsAlone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(*gzipJSONWriter); ok {
			http.Error(w, "wrapped writer reached the upgrade handler", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	h := gzipJSON(mux)
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("upgrade request got Content-Encoding %q", enc)
	}
	if rec.Code != http.StatusSwitchingProtocols {
		t.Fatalf("status %d, want 101", rec.Code)
	}
}

func TestGzipJSONCompressesTheNormalizedOpenAIError(t *testing.T) {
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"missing/model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding %q, want gzip", w.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	// The error writer reads the body to normalize it, so it must see plain JSON
	// and the client must get that normalized body back, compressed once.
	w.Body = bytes.NewBuffer(plain)
	w.Header().Del("Content-Encoding")
	if err := checkUnknownModel(w); err != nil {
		t.Fatal(err)
	}
}
