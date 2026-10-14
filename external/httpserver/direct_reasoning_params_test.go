//go:build http

package httpserver

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A direct completion is the first request an API client sends, with no turn
// and no GET /v1/models before it: it reads the provider listing itself, so
// an aggregator model gets its reasoning level in the form the listing names
// (issue #258).
func TestDirectCompletionReadsTheListedReasoningParameter(t *testing.T) {
	backend := &aggregatorBackend{
		model:  "qwen/qwen3.8-flash",
		params: []string{"max_tokens", "reasoning", "tools"},
	}
	upstream := httptest.NewServer(backend)
	defer upstream.Close()

	root := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: filepath.Join(root, "home"), CWD: root},
		Providers: []config.ProviderConfig{{Name: "agg", Type: "openai", APIBase: upstream.URL, APIKey: "k"}},
		Models:    []config.ModelEntry{{Model: "agg/qwen/qwen3.8-flash"}},
		Agent:     config.Agent{Model: "agg/qwen/qwen3.8-flash"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), root, &session.FileStore{Root: filepath.Join(root, "sessions")})
	srv := New(cfg, mgr, slog.Default(), root)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Drain()

	body := `{"model":"agg/qwen/qwen3.8-flash","reasoning_effort":"high","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	res, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, raw)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.chats) != 1 {
		t.Fatalf("chat requests = %d, want 1", len(backend.chats))
	}
	req := backend.chats[0]
	if got := gjson.GetBytes(req, "reasoning.effort").String(); got != "high" || gjson.GetBytes(req, "reasoning_effort").Exists() {
		t.Fatalf("request = %s, want reasoning.effort high and no reasoning_effort", req)
	}
}
