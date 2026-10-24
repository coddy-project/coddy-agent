//go:build http

package httpserver

// Godog harness for features/models_catalog.feature: drives
// POST /coddy/models/catalog against a stand-in of models.dev that serves a
// catalog in the api.json shape, through the server models_catalog.url points
// at it, with no network and no provider credential.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type modelsCatalogWorld struct {
	t       *testing.T
	catalog *httptest.Server
	ts      *httptest.Server
	srv     *Server
	row     map[string]string
	answer  modelsCatalogAnswer
}

type modelsCatalogAnswer struct {
	OK       bool   `json:"ok"`
	Enabled  bool   `json:"enabled"`
	Error    string `json:"error"`
	Provider string `json:"provider"`
	Models   map[string]struct {
		ContextWindow   int  `json:"context_window"`
		MaxOutputTokens int  `json:"max_output_tokens"`
		MaxTokens       int  `json:"max_tokens"`
		Multimodal      bool `json:"multimodal"`
	} `json:"models"`
}

func (w *modelsCatalogWorld) reset() {
	for _, s := range []*httptest.Server{w.ts, w.catalog} {
		if s != nil {
			s.Close()
		}
	}
	if w.srv != nil {
		w.srv.Drain()
	}
	w.catalog, w.ts, w.srv, w.row, w.answer = nil, nil, nil, nil, modelsCatalogAnswer{}
}

func (w *modelsCatalogWorld) catalogLists(model, provider string, window, output int) error {
	doc := map[string]any{
		provider: map[string]any{
			"id": provider,
			"models": map[string]any{
				model: map[string]any{
					"id":         model,
					"modalities": map[string]any{"input": []string{"text", "image"}, "output": []string{"text"}},
					"limit":      map[string]any{"context": window, "output": output},
				},
			},
		},
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	w.catalog = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write(body)
	}))
	root := w.t.TempDir()
	cfg := &config.Config{
		Paths:         config.Paths{Home: w.t.TempDir(), CWD: root},
		ModelsCatalog: config.ModelsCatalogConfig{URL: w.catalog.URL, Proxy: "none"},
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), root, &session.FileStore{Root: w.t.TempDir()})
	w.srv = New(cfg, mgr, slog.Default(), root)
	w.ts = httptest.NewServer(w.srv.Handler())
	return nil
}

func (w *modelsCatalogWorld) editsRow(typ string) error {
	w.row = map[string]string{"name": "claude", "type": typ}
	return nil
}

func (w *modelsCatalogWorld) ask(model string, reported int) error {
	body, err := json.Marshal(map[string]any{
		"name":   w.row["name"],
		"type":   w.row["type"],
		"models": []map[string]any{{"id": model, "context_window": reported}},
	})
	if err != nil {
		return err
	}
	res, err := http.Post(w.ts.URL+"/coddy/models/catalog", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(&w.answer); err != nil {
		return err
	}
	if !w.answer.OK || !w.answer.Enabled {
		return fmt.Errorf("the catalog answered %+v", w.answer)
	}
	return nil
}

func (w *modelsCatalogWorld) askAbout(model string) error { return w.ask(model, 0) }

func (w *modelsCatalogWorld) askAboutReported(model string, reported int) error {
	return w.ask(model, reported)
}

func (w *modelsCatalogWorld) matches(provider string) error {
	if w.answer.Provider != provider {
		return fmt.Errorf("the row matched %q, want %q", w.answer.Provider, provider)
	}
	return nil
}

func (w *modelsCatalogWorld) suggests(window, maxTokens int) error {
	if len(w.answer.Models) != 1 {
		return fmt.Errorf("suggestions for %d models, want one: %+v", len(w.answer.Models), w.answer.Models)
	}
	for id, m := range w.answer.Models {
		if m.ContextWindow != window || m.MaxTokens != maxTokens || !m.Multimodal {
			return fmt.Errorf("%s: suggested %+v, want window %d, max_tokens %d and image support", id, m, window, maxTokens)
		}
	}
	return nil
}

func TestModelsCatalogFeature(t *testing.T) {
	w := &modelsCatalogWorld{t: t}
	suite := godog.TestSuite{
		Name: "models-catalog",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				w.reset()
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				w.reset()
				return ctx, nil
			})
			sc.Step(`^a models catalog that lists "([^"]*)" under "([^"]*)" with a (\d+) window, a (\d+) output limit and image input$`, w.catalogLists)
			sc.Step(`^the settings screen edits a provider row of type "([^"]*)"$`, w.editsRow)
			sc.Step(`^the settings screen asks the catalog about "([^"]*)"$`, w.askAbout)
			sc.Step(`^the settings screen asks the catalog about "([^"]*)" whose provider reports a (\d+) window$`, w.askAboutReported)
			sc.Step(`^the catalog matches the row to "([^"]*)"$`, w.matches)
			sc.Step(`^it suggests a context window of (\d+), max_tokens of (\d+) and image support$`, w.suggests)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/models_catalog.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("models catalog feature suite failed")
	}
}
