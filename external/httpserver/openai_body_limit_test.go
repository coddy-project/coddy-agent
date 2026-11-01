//go:build http

package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"gopkg.in/yaml.v3"
)

func TestOpenAIBodyLimit(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		for _, chunked := range []bool{false, true} {
			t.Run(path+map[bool]string{true: "/chunked", false: "/length"}[chunked], func(t *testing.T) {
				cfg := &config.Config{}
				if err := yaml.Unmarshal([]byte("httpserver:\n  max_body_bytes: 128\n"), cfg); err != nil {
					t.Fatal(err)
				}
				runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
					return "", nil
				}
				mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
				srv := New(cfg, mgr, slog.Default(), "/tmp")
				body := `{"model":"missing","input":"` + strings.Repeat("x", 256) + `"}`
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				if chunked {
					req.ContentLength = -1
				}
				w := httptest.NewRecorder()
				srv.Handler().ServeHTTP(w, req)
				if w.Code != http.StatusRequestEntityTooLarge {
					t.Fatalf("got %d: %s", w.Code, w.Body.String())
				}
				if w.Header().Get("Content-Type") != "application/json" {
					t.Fatalf("content type %q", w.Header().Get("Content-Type"))
				}
			})
		}
	}
}

func TestOpenAIBodyLimitTrailingData(t *testing.T) {
	cfg := &config.Config{}
	if err := yaml.Unmarshal([]byte("httpserver:\n  max_body_bytes: 128\n"), cfg); err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"missing"}`+strings.Repeat(" ", 256)))
	req.ContentLength = -1
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}
