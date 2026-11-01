//go:build http

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestOpenAIErrorsFormatAndMethod(t *testing.T) {
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/v1/chat/completions", "{", 400},
		{"POST", "/v1/responses", "{", 400},
		{"GET", "/v1/chat/completions", "", 405},
		{"DELETE", "/v1/responses/abc", "", 405},
		{"POST", "/v1/models", "", 405},
		{"GET", "/v1/not-a-route", "", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			if w.Code != tc.status || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("status %d content type %q body %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
			}
			var body struct {
				Error map[string]any `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"message", "type", "code", "param"} {
				if _, ok := body.Error[key]; !ok {
					t.Errorf("missing %s in %s", key, w.Body.String())
				}
			}
		})
	}
	_, pattern := srv.mux.Handler(httptest.NewRequest(http.MethodGet, "/v1/not-a-route", nil))
	if pattern == "/" {
		t.Fatal("unknown API route fell through to the SPA")
	}
}

func TestOpenAIProviderErrorHidesDetails(t *testing.T) {
	err := errors.New(`provider "private" http://secret.internal:1234/v1?key=secret`)
	reply := replyForError(err)
	raw, e := json.Marshal(reply.body)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(raw, []byte("secret")) || bytes.Contains(raw, []byte("private")) {
		t.Fatalf("sensitive error: %s", raw)
	}
}

func TestOpenAIErrorWriterFlushesErrorStatus(t *testing.T) {
	r := httptest.NewRecorder()
	w := &openAIErrorWriter{ResponseWriter: r, server: &Server{log: slog.Default()}}
	w.WriteHeader(http.StatusBadRequest)
	w.Flush()
	if r.Code != http.StatusBadRequest || !json.Valid(r.Body.Bytes()) {
		t.Fatalf("status %d, body %q", r.Code, r.Body.String())
	}
}

func TestOpenAIAuthErrorFormat(t *testing.T) {
	cfg := &config.Config{}
	cfg.HTTPServer.AuthToken = "secret"
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if w.Code != 401 || w.Header().Get("Content-Type") != "application/json" || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("status %d, type %q body %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong method bypassed authentication: %d %s", w.Code, w.Body.String())
	}
}

func TestOpenAIStreamAttachmentErrorHidesPath(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	cfg := &config.Config{}
	dir := t.TempDir()
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, logger, dir, nil)
	srv := New(cfg, mgr, logger, dir)
	w := httptest.NewRecorder()
	body := `{"model":"agent","input":"read","stream":true,"attachments":[{"path":"secret-filename.txt"}]}`
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if strings.Contains(w.Body.String(), "secret-filename") {
		t.Fatalf("exposed path: %s", w.Body.String())
	}
	if !strings.Contains(logs.String(), "secret-filename") {
		t.Fatalf("missing diagnostic: %s", logs.String())
	}
}

func TestOpenAIValidationErrorParams(t *testing.T) {
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	for _, tc := range []struct{ body, param string }{
		{`{"messages":[]}`, "model"},
		{`{"model":"agent","messages":[]}`, "messages"},
		{`{"model":"agent","messages":[{"role":"user","content":"hi"}],"metadata":123}`, "metadata"},
	} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body)))
		var got struct {
			Error struct {
				Param string `json:"param"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != 400 || got.Error.Param != tc.param {
			t.Errorf("got %d %s, want param %s", w.Code, w.Body.String(), tc.param)
		}
	}
}

// The upstream_error body writeErrorReply produces is already sanitized - a
// fixed message, a status and a status code, never the provider's text - so
// the error writer must not flatten it into the generic 500 message.
func TestOpenAIErrorWriterKeepsUpstreamDetails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"upstream 500", upstreamHTTPError(500, nil), http.StatusBadGateway, "Upstream provider request failed"},
		{"upstream 503", upstreamHTTPError(503, nil), http.StatusBadGateway, "Upstream provider request failed"},
		{"upstream 504", upstreamHTTPError(504, nil), http.StatusGatewayTimeout, "Upstream provider request failed"},
		{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, "Upstream request timed out"},
		{"own failure", errors.New("store broke on /secret/path"), http.StatusInternalServerError, "Internal server error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRecorder()
			ow := &openAIErrorWriter{ResponseWriter: r, server: &Server{log: slog.Default()}}
			writeErrorReply(ow, tc.err)
			if r.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", r.Code, tc.status, r.Body.String())
			}
			var got struct {
				Error map[string]any `json:"error"`
			}
			if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
				t.Fatalf("%s", r.Body.String())
			}
			if got.Error["message"] != tc.message {
				t.Fatalf("message = %v, want %q: %s", got.Error["message"], tc.message, r.Body.String())
			}
			if got.Error["type"] != "upstream_error" && tc.status != http.StatusInternalServerError {
				t.Fatalf("type = %v, want upstream_error: %s", got.Error["type"], r.Body.String())
			}
		})
	}
	// upstream_status survives for a client that reads it.
	r := httptest.NewRecorder()
	ow := &openAIErrorWriter{ResponseWriter: r, server: &Server{log: slog.Default()}}
	writeErrorReply(ow, upstreamHTTPError(503, nil))
	if !strings.Contains(r.Body.String(), `"upstream_status":503`) {
		t.Fatalf("upstream_status lost: %s", r.Body.String())
	}
}

// The mux sends HEAD to the GET handlers and the method fallback advertises
// "GET, HEAD", so a HEAD request gets a 200 answer, not a 404.
func TestOpenAIModelsAcceptsHead(t *testing.T) {
	cfg := &config.Config{}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/v1/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("HEAD /v1/models: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/models", nil))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST /v1/models: %d Allow %q", w.Code, w.Header().Get("Allow"))
	}
}

// A failed attachment tells the client what went wrong without repeating the
// path or any server-side detail (the full error stays in the log).
func TestOpenAIAttachmentErrorReason(t *testing.T) {
	cfg := &config.Config{}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A PNG header with NUL padding: valid bytes, never text.
	blob := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 32)...)
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), dir, nil)
	srv := New(cfg, mgr, slog.Default(), dir)
	for _, tc := range []struct {
		name, attachments, wantReason string
	}{
		{"missing file", `[{"path":"missing.txt"}]`, "file not found"},
		{"directory", `[{"path":"sub"}]`, "directory"},
		{"escapes workspace", `[{"path":"../out.txt"}]`, "escapes the workspace"},
		{"empty path", `[{"path":""}]`, "path is empty"},
		{"binary", `[{"path":"bin.dat"}]`, "not readable text"},
		{"line range", `[{"path":"small.txt","source":{"startLine":10,"endLine":20}}]`, "line range"},
		{"unknown kind", `[{"path":"x.txt","kind":"bogus","source":{"literal":"y"}}]`, "kind is invalid"},
		{"stdin needs literal", `[{"path":"stdin","kind":"stdin"}]`, "literal"},
	} {
		body := `{"model":"agent","input":"read","attachments":` + tc.attachments + `}`
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
		var got struct {
			Error struct {
				Message string `json:"message"`
				Param   string `json:"param"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: %s", tc.name, w.Body.String())
		}
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400: %s", tc.name, w.Code, w.Body.String())
		}
		if !strings.Contains(got.Error.Message, tc.wantReason) {
			t.Errorf("%s: message %q does not name %q", tc.name, got.Error.Message, tc.wantReason)
		}
		if got.Error.Param != "attachments" {
			t.Errorf("%s: param = %q, want attachments", tc.name, got.Error.Param)
		}
	}
	// The reason is client-safe but the path stays out of the answer.
	body := `{"model":"agent","input":"read","attachments":[{"path":"../escape-me.txt"}]}`
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if strings.Contains(w.Body.String(), "escape-me") {
		t.Fatalf("answer leaks the attachment path: %s", w.Body.String())
	}
	// The streamed error frame names the same reason.
	body = `{"model":"agent","input":"read","stream":true,"attachments":[{"path":"missing.txt"}]}`
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	if !strings.Contains(w.Body.String(), "file not found") || strings.Contains(w.Body.String(), "missing.txt") {
		t.Fatalf("streamed attachment error: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"param":"attachments"`) {
		t.Fatalf("streamed attachment error has no param: %s", w.Body.String())
	}
}
