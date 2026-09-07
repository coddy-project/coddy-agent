//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func reasoningHTTPConfig() *config.Config {
	return &config.Config{
		Agent: config.Agent{Model: "openai/gpt-5"},
		Providers: []config.ProviderConfig{
			{Name: "openai", Type: "openai"},
			{Name: "neuraldeep", Type: "neuraldeep"},
		},
		Models: []config.ModelEntry{
			{Model: "openai/gpt-5", MaxTokens: 100, ReasoningDefault: "medium"},
			{Model: "openai/gpt-4o", MaxTokens: 100},
			{Model: "neuraldeep/qwen3.6-35b-a3b", MaxTokens: 100},
			{Model: "neuraldeep/gpt-oss-120b", MaxTokens: 100},
		},
	}
}

func TestGETModelsReasoningLevels(t *testing.T) {
	cfg := reasoningHTTPConfig()
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Data []struct {
			ID               string   `json:"id"`
			ReasoningLevels  []string `json:"reasoning_levels"`
			ReasoningDefault string   `json:"reasoning_default"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	got := map[string]struct {
		levels []string
		deflt  string
	}{}
	for _, d := range body.Data {
		got[d.ID] = struct {
			levels []string
			deflt  string
		}{d.ReasoningLevels, d.ReasoningDefault}
	}
	if !reflect.DeepEqual(got["openai/gpt-5"].levels, []string{"minimal", "low", "medium", "high"}) {
		t.Errorf("gpt-5 reasoning_levels = %v", got["openai/gpt-5"].levels)
	}
	if got["openai/gpt-5"].deflt != "medium" {
		t.Errorf("gpt-5 reasoning_default = %q, want medium", got["openai/gpt-5"].deflt)
	}
	if len(got["openai/gpt-4o"].levels) != 0 {
		t.Errorf("gpt-4o reasoning_levels = %v, want empty", got["openai/gpt-4o"].levels)
	}
	// Qwen3 and gpt-oss families auto-detect to the standard level set.
	if !reflect.DeepEqual(got["neuraldeep/qwen3.6-35b-a3b"].levels, []string{"low", "medium", "high"}) {
		t.Errorf("qwen3.6 reasoning_levels = %v", got["neuraldeep/qwen3.6-35b-a3b"].levels)
	}
	if !reflect.DeepEqual(got["neuraldeep/gpt-oss-120b"].levels, []string{"low", "medium", "high"}) {
		t.Errorf("gpt-oss reasoning_levels = %v", got["neuraldeep/gpt-oss-120b"].levels)
	}
}

func TestProfileMetadataPatchReasoning(t *testing.T) {
	cfg := reasoningHTTPConfig()
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	st, err := mgr.EnsureHTTPSession(context.Background(), "sess-reasoning", "/tmp")
	if err != nil {
		t.Fatal(err)
	}

	// Valid level for the selected model is applied.
	if _, err := profileMetadataPatch(cfg, st, json.RawMessage(`{"model":"openai/gpt-5","reasoning":"high"}`)); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if got := st.GetSelectedReasoning(); got != "high" {
		t.Errorf("selected reasoning = %q, want high", got)
	}

	// Invalid level is rejected.
	if _, err := profileMetadataPatch(cfg, st, json.RawMessage(`{"reasoning":"bogus"}`)); err == nil {
		t.Error("expected error for invalid reasoning level")
	}

	// A level not supported by the current model is rejected (minimal not valid for gpt-4o).
	if _, err := profileMetadataPatch(cfg, st, json.RawMessage(`{"model":"openai/gpt-4o","reasoning":"high"}`)); err == nil {
		t.Error("expected error for reasoning on non-reasoning model")
	}

	// Auto-detected levels of qwen3 / gpt-oss models are selectable in the same way.
	if _, err := profileMetadataPatch(cfg, st, json.RawMessage(`{"model":"neuraldeep/qwen3.6-35b-a3b","reasoning":"high"}`)); err != nil {
		t.Fatalf("qwen patch: %v", err)
	}
	if _, err := profileMetadataPatch(cfg, st, json.RawMessage(`{"model":"neuraldeep/gpt-oss-120b","reasoning":"low"}`)); err != nil {
		t.Fatalf("gpt-oss patch: %v", err)
	}
	if got := st.GetSelectedReasoning(); got != "low" {
		t.Errorf("selected reasoning = %q, want low", got)
	}
}

func TestCoddySessionPatchSelectedReasoning(t *testing.T) {
	root := t.TempDir()
	sessRoot := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := reasoningHTTPConfig()
	cfg.Paths = config.Paths{Home: filepath.Join(root, "home"), CWD: "/tmp"}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	store := &session.FileStore{Root: sessRoot}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", store)
	srv := New(cfg, mgr, slog.Default(), "/tmp")
	t.Cleanup(srv.Drain)

	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	sid := res.SessionID

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	doPatch := func(body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/coddy/sessions/"+url.PathEscape(sid), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Coddy-Session-ID", sid)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Valid level (agent model gpt-5 supports high) is accepted and echoed.
	resp := doPatch(`{"selectedReasoning":"high"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var parsed struct {
		SelectedReasoning string `json:"selectedReasoning"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	resp.Body.Close()
	if parsed.SelectedReasoning != "high" {
		t.Fatalf("selectedReasoning = %q, want high", parsed.SelectedReasoning)
	}

	// Persisted to disk.
	snap, err := store.ReadSnapshot(sid)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.SelectedReasoning != "high" {
		t.Fatalf("persisted selectedReasoning = %q, want high", snap.Meta.SelectedReasoning)
	}

	// Messages endpoint exposes the effective reasoning for open-session restore.
	mres, err := http.Get(ts.URL + "/coddy/sessions/" + url.PathEscape(sid) + "/messages")
	if err != nil {
		t.Fatal(err)
	}
	var mbody struct {
		SelectedReasoning string `json:"selectedReasoning"`
	}
	_ = json.NewDecoder(mres.Body).Decode(&mbody)
	mres.Body.Close()
	if mbody.SelectedReasoning != "high" {
		t.Fatalf("messages selectedReasoning = %q, want high", mbody.SelectedReasoning)
	}

	// Invalid level is rejected.
	bad := doPatch(`{"selectedReasoning":"bogus"}`)
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid level status = %d, want 400", bad.StatusCode)
	}
	bad.Body.Close()
}

// --- GET /coddy/config/reasoning-levels edge and error cases (happy path:
// features/reasoning_levels_fetch.feature) ---

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

// TestReasoningLevelsHonoursUnsavedProviderType covers the provider row the
// operator is still editing: a codex provider that is not saved yet asks with
// provider_type=codex and gets "none" in place of "minimal", while a saved codex
// provider whose type was just switched to openai in the form gets the
// un-remapped list. The saved config must not win over the form.
func TestReasoningLevelsHonoursUnsavedProviderType(t *testing.T) {
	ts := newReasoningLevelsServer(t)
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"unsaved codex provider", "?model=" + url.QueryEscape("brand-new/gpt-5.5") + "&provider_type=codex", "none,low,medium,high"},
		{"saved codex provider switched to openai in the form", "?model=" + url.QueryEscape("codex/gpt-5.5") + "&provider_type=openai", "minimal,low,medium,high"},
		{"saved codex provider without a hint keeps the saved remap", "?model=" + url.QueryEscape("codex/gpt-5.5"), "none,low,medium,high"},
		{"blank hint falls back to the saved provider", "?model=" + url.QueryEscape("codex/gpt-5.5") + "&provider_type=%20", "none,low,medium,high"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, reply := getReasoningLevels(t, ts, tc.query)
			if code != http.StatusOK || !reply.OK {
				t.Fatalf("status %d ok %v error %q", code, reply.OK, reply.Error)
			}
			if got := strings.Join(reply.Levels, ","); got != tc.want {
				t.Fatalf("levels = %q, want %q", got, tc.want)
			}
		})
	}
}
