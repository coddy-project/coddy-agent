//go:build http

package httpserver

// Godog harness for features/provider_models_fetch.feature: drives
// POST /coddy/providers/models against a gateway whose saved config never holds
// the provider the scenario fetches (or holds it under another key), which is
// the state the settings form is in while a provider row is still unsaved.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type providerModelsWorld struct {
	ts       *httptest.Server
	upstream *httptest.Server

	gotAuth string

	ok     bool
	models []string
	ctx    map[string]int
}

// upstreamServing starts a stand-in provider endpoint that answers the model
// list with the given ids and records the credential it was called with. An
// entry may carry a context window as "id:131072" - the stand-in reports it
// under the OpenRouter spelling, context_length.
func (w *providerModelsWorld) upstreamServing(t *testing.T, csv string) error {
	ids := strings.Split(csv, ",")
	var data []map[string]interface{}
	for _, part := range ids {
		part = strings.TrimSpace(part)
		id, ctx := part, 0
		if i := strings.LastIndex(part, ":"); i > 0 {
			id = part[:i]
			ctx, _ = strconv.Atoi(part[i+1:])
		}
		entry := map[string]interface{}{"id": id}
		if ctx > 0 {
			entry["context_length"] = ctx
		}
		data = append(data, entry)
	}
	w.upstream = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.gotAuth = r.Header.Get("Authorization")
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]interface{}{"data": data})
	}))
	t.Cleanup(w.upstream.Close)
	return nil
}

// startGateway boots an HTTP gateway whose config holds the given provider
// rows (possibly none of the one under test) and a seed model on seedProvider
// so validation passes.
func (w *providerModelsWorld) startGateway(t *testing.T, providersYML, seedProvider string) error {
	home := t.TempDir()
	cfgPath := filepath.Join(home, "config.yaml")
	yml := providersYML + fmt.Sprintf(`models:
  - model: %s/seed-model
    max_tokens: 4096
agent:
  model: %s/seed-model
`, seedProvider, seedProvider)
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

func (w *providerModelsWorld) gatewayWithUnrelatedProvider(t *testing.T, name string) error {
	return w.startGateway(t, fmt.Sprintf(`providers:
  - name: %s
    type: openai
    api_key: k
`, name), name)
}

func (w *providerModelsWorld) gatewayWithProviderAtUpstream(t *testing.T, name, providerType, key string) error {
	if w.upstream == nil {
		return fmt.Errorf("upstream not started")
	}
	return w.startGateway(t, fmt.Sprintf(`providers:
  - name: %s
    type: %s
    api_base: %s
    api_key: %s
`, name, providerType, w.upstream.URL, key), name)
}

// postRow posts the full provider row, the way the settings form sends it.
func (w *providerModelsWorld) postRow(name, providerType, key string) error {
	if w.upstream == nil {
		return fmt.Errorf("upstream not started")
	}
	return w.postBody(fmt.Sprintf(
		`{"name":%q,"type":%q,"api_base":%q,"api_key":%q}`,
		name, providerType, w.upstream.URL, key))
}

// postNameOnly posts the sparse {"name": ...} body: no secrets travel, the
// saved provider's credentials are expected to fill in.
func (w *providerModelsWorld) postNameOnly(name string) error {
	return w.postBody(fmt.Sprintf(`{"name":%q}`, name))
}

func (w *providerModelsWorld) postBody(body string) error {
	if w.ts == nil {
		return fmt.Errorf("gateway not started")
	}
	res, err := http.Post(w.ts.URL+"/coddy/providers/models", "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d, want 200", res.StatusCode)
	}
	var payload struct {
		OK     bool `json:"ok"`
		Models []struct {
			ID            string `json:"id"`
			ContextWindow int    `json:"context_window"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return err
	}
	w.ok = payload.OK
	w.models = nil
	w.ctx = map[string]int{}
	for _, m := range payload.Models {
		w.models = append(w.models, m.ID)
		w.ctx[m.ID] = m.ContextWindow
	}
	return nil
}

func (w *providerModelsWorld) wantModels(csv string) error {
	if !w.ok {
		return fmt.Errorf("response reported ok:false")
	}
	if strings.Join(w.models, ",") != csv {
		return fmt.Errorf("models = %v, want %v", w.models, csv)
	}
	return nil
}

func (w *providerModelsWorld) upstreamSawKey(key string) error {
	if w.gotAuth != "Bearer "+key {
		return fmt.Errorf("upstream Authorization = %q, want Bearer %s", w.gotAuth, key)
	}
	return nil
}

// wantCtx asserts the response carried the context window the stand-in
// reported for that model.
func (w *providerModelsWorld) wantCtx(want int, id string) error {
	if w.ctx[id] != want {
		return fmt.Errorf("context_window for %q = %d, want %d", id, w.ctx[id], want)
	}
	return nil
}

func TestProviderModelsFetchFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "provider_models_fetch",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &providerModelsWorld{}
			sc.Step(`^an upstream model endpoint serving "([^"]*)"$`, func(csv string) error {
				return w.upstreamServing(t, csv)
			})
			sc.Step(`^a coddy server holding only a provider named "([^"]*)"$`, func(name string) error {
				return w.gatewayWithUnrelatedProvider(t, name)
			})
			sc.Step(`^a coddy server holding a provider "([^"]*)" of type "([^"]*)" at that upstream with key "([^"]*)"$`, func(name, providerType, key string) error {
				return w.gatewayWithProviderAtUpstream(t, name, providerType, key)
			})
			sc.Step(`^the settings form posts the provider row "([^"]*)" of type "([^"]*)" at that upstream with key "([^"]*)"$`, w.postRow)
			sc.Step(`^the settings form posts only the provider name "([^"]*)"$`, w.postNameOnly)
			sc.Step(`^the gateway answers with the models "([^"]*)"$`, w.wantModels)
			sc.Step(`^the gateway answers with context window (\d+) for "([^"]*)"$`, w.wantCtx)
			sc.Step(`^the upstream saw the key "([^"]*)"$`, w.upstreamSawKey)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/provider_models_fetch.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("provider_models_fetch feature failed")
	}
}
