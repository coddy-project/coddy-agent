//go:build http

package httpserver

// Edge and error cases of GET /coddy/config/reasoning-levels. The happy path is
// features/reasoning_levels_fetch.feature.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type reasoningLevelsReply struct {
	OK       bool     `json:"ok"`
	Error    string   `json:"error"`
	Model    string   `json:"model"`
	Detected bool     `json:"detected"`
	Levels   []string `json:"levels"`
}

// newReasoningLevelsServer boots a gateway with one openai and one codex provider.
func newReasoningLevelsServer(t *testing.T) *httptest.Server {
	t.Helper()
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	yml := `providers:
  - name: valera
    type: openai
    api_key: k
  - name: codex
    type: codex
models:
  - model: valera/seed-model
    max_tokens: 4096
  - model: valera/qwen3.8-27b
    max_tokens: 4096
    reasoning_levels: []
agent:
  model: valera/seed-model
`
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	ts := httptest.NewServer(New(cfg, mgr, slog.Default(), home).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func getReasoningLevels(t *testing.T, ts *httptest.Server, query string) (int, reasoningLevelsReply) {
	t.Helper()
	res, err := http.Get(ts.URL + "/coddy/config/reasoning-levels" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var reply reasoningLevelsReply
	if err := json.NewDecoder(res.Body).Decode(&reply); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return res.StatusCode, reply
}

// TestReasoningLevelsRequiresModel pins the one case that is a client bug rather
// than in-progress form input: no model to resolve at all.
func TestReasoningLevelsRequiresModel(t *testing.T) {
	ts := newReasoningLevelsServer(t)
	for _, q := range []string{"", "?model=", "?model=%20%20"} {
		code, reply := getReasoningLevels(t, ts, q)
		if code != http.StatusBadRequest {
			t.Errorf("query %q: status %d, want 400", q, code)
		}
		if reply.OK {
			t.Errorf("query %q: ok should be false", q)
		}
	}
}

// TestReasoningLevelsMalformedModelIsInline covers a half-typed id: the settings
// form is mid-edit, so the answer is an inline ok:false rather than an HTTP error
// the form would have to render as a failed request.
func TestReasoningLevelsMalformedModelIsInline(t *testing.T) {
	ts := newReasoningLevelsServer(t)
	for _, model := range []string{"qwen3.8-27b", "valera/", "/qwen3"} {
		code, reply := getReasoningLevels(t, ts, "?model="+url.QueryEscape(model))
		if code != http.StatusOK {
			t.Errorf("model %q: status %d, want 200", model, code)
		}
		if reply.OK {
			t.Errorf("model %q: ok should be false", model)
		}
		if reply.Error == "" {
			t.Errorf("model %q: expected an error message for the form", model)
		}
		if len(reply.Levels) != 0 || reply.Detected {
			t.Errorf("model %q: expected no levels, got %v", model, reply.Levels)
		}
	}
}

// TestReasoningLevelsIgnoresSavedOverride is the point of the button: it reports
// what the model id offers, not what the config currently says. valera/qwen3.8-27b
// is saved with an explicit [] opt-out, and the endpoint must still answer with
// the detected tiers so the operator can take them back.
func TestReasoningLevelsIgnoresSavedOverride(t *testing.T) {
	ts := newReasoningLevelsServer(t)
	code, reply := getReasoningLevels(t, ts, "?model="+url.QueryEscape("valera/qwen3.8-27b"))
	if code != http.StatusOK || !reply.OK {
		t.Fatalf("status %d ok %v", code, reply.OK)
	}
	if got := strings.Join(reply.Levels, ","); got != "low,medium,high" {
		t.Fatalf("levels = %q, want low,medium,high despite the saved opt-out", got)
	}
	if !reply.Detected {
		t.Fatal("detected should be true")
	}
}

// TestReasoningLevelsUnknownProviderStillDetects covers a provider the operator
// has typed but not saved yet: detection is model-id based, so it still answers.
// Only the Codex remap needs the provider, and it cannot apply here.
func TestReasoningLevelsUnknownProviderStillDetects(t *testing.T) {
	ts := newReasoningLevelsServer(t)
	code, reply := getReasoningLevels(t, ts, "?model="+url.QueryEscape("not-saved-yet/gpt-5.5"))
	if code != http.StatusOK || !reply.OK {
		t.Fatalf("status %d ok %v", code, reply.OK)
	}
	if got := strings.Join(reply.Levels, ","); got != "minimal,low,medium,high" {
		t.Fatalf("levels = %q, want the un-remapped gpt-5 tiers", got)
	}
}

// TestReasoningLevelsRejectsNonGET keeps the route to its documented method.
func TestReasoningLevelsRejectsNonGET(t *testing.T) {
	ts := newReasoningLevelsServer(t)
	res, err := http.Post(ts.URL+"/coddy/config/reasoning-levels?model=valera/gpt-5", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d, want 404 or 405", res.StatusCode)
	}
}
