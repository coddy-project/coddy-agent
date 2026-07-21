//go:build http && ui

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
)

func TestGETUIStatic(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", res.StatusCode, b)
	}
	if !strings.Contains(string(b), "<title>Coddy Agent</title>") {
		t.Fatal("missing UI title")
	}
	if !strings.Contains(string(b), `href="/coddy-favicon.svg"`) {
		t.Fatal("missing favicon link")
	}
}

func TestEmbeddedUIFaviconAssets(t *testing.T) {
	cfg := &config.Config{
		Agent: config.Agent{Model: "openai/gpt-4o"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	for _, tc := range []struct {
		path     string
		contains string
	}{
		{"/coddy-favicon.svg", "<svg"},
		{"/favicon-32.png", "\x89PNG"},
		{"/favicon.ico", "\x00\x00\x01\x00"},
		{"/apple-touch-icon.png", "\x89PNG"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			res, err := http.Get(ts.URL + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			b, err := ioReadAllClose(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d body %q", res.StatusCode, b)
			}
			if !strings.Contains(string(b), tc.contains) {
				t.Fatalf("body missing %q", tc.contains)
			}
		})
	}
}

func TestUIDisabledServesAPIOnly(t *testing.T) {
	disabled := false
	cfg := &config.Config{
		Agent:  config.Agent{Model: "openai/gpt-4o"},
		Models: []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		UI:     config.UIConfig{Enabled: &disabled},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// GET / serves the API-only notice, not the SPA, even though the UI is compiled in.
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ioReadAllClose(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("ui disabled GET / status %d want 404: %s", res.StatusCode, b)
	}
	if strings.Contains(string(b), "<title>Coddy Agent</title>") {
		t.Fatal("ui disabled should not serve the SPA index")
	}
	if !strings.Contains(string(b), "ui.enabled") {
		t.Fatalf("expected the UI-disabled notice, got %q", b)
	}

	// The API still works.
	mres, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	mb, _ := ioReadAllClose(mres.Body)
	if mres.StatusCode != http.StatusOK {
		t.Fatalf("api with ui disabled: status %d body %s", mres.StatusCode, mb)
	}
}

func TestEmbeddedUIPublicAssetsCacheControl(t *testing.T) {
	cfg := &config.Config{
		Agent: config.Agent{Model: "openai/gpt-4o"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	for _, path := range []string{
		"/", "/index.html", "/app.js", "/styles.css",
		"/coddy-favicon.svg", "/favicon-32.png", "/favicon.ico", "/apple-touch-icon.png",
	} {
		t.Run(path, func(t *testing.T) {
			res, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
				t.Fatalf("Cache-Control %q for %s, want no-cache", cc, path)
			}
		})
	}
}
