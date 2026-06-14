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
		Models: []config.ModelEntry{
			{Model: "openai/gpt-5", MaxTokens: 100, ReasoningDefault: "medium"},
			{Model: "openai/gpt-4o", MaxTokens: 100},
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
