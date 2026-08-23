//go:build http

package httpserver

// Godog harness for features/reasoning_levels_fetch.feature: drives
// GET /coddy/config/reasoning-levels against a gateway whose config holds only
// providers, so the answer comes from model-id detection rather than from a saved
// models[] entry - which is the state the settings form is in while the operator
// is still typing a new model id.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type reasoningLevelsWorld struct {
	ts *httptest.Server

	ok       bool
	detected bool
	levels   []string
}

// startGateway boots an HTTP gateway whose config has one provider of the given
// type and one unrelated model entry (config validation needs agent.model to
// resolve). The model the scenario asks about is deliberately not in models[].
func (w *reasoningLevelsWorld) startGateway(t *testing.T, providerType, providerName string) error {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	yml := fmt.Sprintf(`providers:
  - name: %s
    type: %s
    api_key: k
models:
  - model: %s/seed-model
    max_tokens: 4096
agent:
  model: %s/seed-model
`, providerName, providerType, providerName, providerName)
	if err := os.WriteFile(cfgPath, []byte(yml), 0o644); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	w.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(w.ts.Close)
	return nil
}

func (w *reasoningLevelsWorld) fetchLevels(model string) error {
	if w.ts == nil {
		return fmt.Errorf("gateway not started")
	}
	res, err := http.Get(w.ts.URL + "/coddy/config/reasoning-levels?model=" + url.QueryEscape(model))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d, want 200", res.StatusCode)
	}
	var payload struct {
		OK       bool     `json:"ok"`
		Detected bool     `json:"detected"`
		Levels   []string `json:"levels"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return err
	}
	w.ok, w.detected, w.levels = payload.OK, payload.Detected, payload.Levels
	return nil
}

func (w *reasoningLevelsWorld) wantLevels(csv string) error {
	if !w.ok {
		return fmt.Errorf("response reported ok:false")
	}
	want := strings.Split(csv, ",")
	if strings.Join(w.levels, ",") != strings.Join(want, ",") {
		return fmt.Errorf("levels = %v, want %v", w.levels, want)
	}
	return nil
}

func (w *reasoningLevelsWorld) wantNoLevels() error {
	if !w.ok {
		return fmt.Errorf("response reported ok:false")
	}
	if len(w.levels) != 0 {
		return fmt.Errorf("levels = %v, want none", w.levels)
	}
	return nil
}

func (w *reasoningLevelsWorld) wantDetected(detected bool) error {
	if w.detected != detected {
		return fmt.Errorf("detected = %v, want %v", w.detected, detected)
	}
	return nil
}

func TestReasoningLevelsFetchFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "reasoning_levels_fetch",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &reasoningLevelsWorld{}
			sc.Step(`^a coddy gateway with an? "([^"]*)" provider named "([^"]*)"$`,
				func(providerType, providerName string) error {
					return w.startGateway(t, providerType, providerName)
				})
			sc.Step(`^the settings form fetches the reasoning levels for "([^"]*)"$`, w.fetchLevels)
			sc.Step(`^the gateway answers with the levels "([^"]*)"$`, w.wantLevels)
			sc.Step(`^the gateway answers with no levels$`, w.wantNoLevels)
			sc.Step(`^the answer reports the levels as detected$`, func() error { return w.wantDetected(true) })
			sc.Step(`^the answer reports the levels as not detected$`, func() error { return w.wantDetected(false) })
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/reasoning_levels_fetch.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("reasoning_levels_fetch feature failed")
	}
}
