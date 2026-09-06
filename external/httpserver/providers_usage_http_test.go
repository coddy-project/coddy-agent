//go:build http

package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The REST envelope's error branches; the happy paths live in
// features/neuraldeep_usage.feature.
func TestProviderUsageRouteErrorBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEURALDEEP_API_KEY", "")
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: home},
		Providers: []config.ProviderConfig{
			{Name: "neuraldeep", Type: "neuraldeep"},
			{Name: "stub", Type: "openai", APIBase: "http://127.0.0.1:0", APIKey: "test"},
		},
		Models: []config.ModelEntry{{Model: "neuraldeep/qwen", MaxTokens: 100, MaxContextTokens: 1000}},
		Agent:  config.Agent{Model: "neuraldeep/qwen"},
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.New(slog.DiscardHandler), home, nil)
	srv := New(cfg, mgr, slog.New(slog.DiscardHandler), home)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	get := func(path string) (int, map[string]interface{}) {
		t.Helper()
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var out map[string]interface{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	if status, _ := get("/coddy/providers/nope/usage"); status != http.StatusNotFound {
		t.Fatalf("unknown provider status = %d", status)
	}
	// No credential at all: the answer names the failure and carries the
	// (empty) snapshot so a client can render the hint, never a key.
	status, out := get("/coddy/providers/neuraldeep/usage")
	if status != http.StatusOK || out["ok"] != false || out["error"] != "unauthorized" {
		t.Fatalf("no credential: status=%d body=%v", status, out)
	}
	usage, _ := out["usage"].(map[string]interface{})
	if usage == nil || usage["provider"] != "neuraldeep" || usage["error"] != "unauthorized" {
		t.Fatalf("no credential usage = %v", out["usage"])
	}
	if status, out = get("/coddy/providers/stub/usage"); status != http.StatusOK || out["ok"] != false || out["unsupported"] != true || out["provider"] != "stub" || out["providerType"] != "openai" {
		t.Fatalf("unsupported: status=%d body=%v", status, out)
	}
}
