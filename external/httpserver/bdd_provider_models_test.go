//go:build http

package httpserver

// Godog harness for features/provider_models_fetch.feature: drives
// POST /coddy/providers/models against a gateway whose saved config never holds
// the provider the scenario fetches (or holds it under another key), which is
// the state the settings form is in while a provider row is still unsaved, and
// against a saved codex provider whose catalog comes from a stand-in Codex
// backend that gates models by Codex release the way the live one does.

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
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/models"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type providerModelsWorld struct {
	ts       *httptest.Server
	upstream *httptest.Server

	gotAuth string

	// codex is the stand-in Codex backend, codexRows the catalog it offers and
	// codexVersions the client_version of every catalog request it answered;
	// codexMu guards both, since the stand-in reads the rows and records the
	// versions on its own goroutines.
	codex         *httptest.Server
	codexMu       sync.Mutex
	codexRows     []codexCatalogRow
	codexVersions []string

	ok      bool
	models  []string
	ctx     map[string]int
	catalog map[string]*models.Limits
}

// codexCatalogRow is one model of the stand-in Codex catalog: its slug and
// the oldest Codex release the backend offers it to.
type codexCatalogRow struct {
	slug       string
	minVersion string
}

// codexSourceBuildVersion is the release the stand-in answers a Codex source
// build (client_version 0.0.0) as. The live backend did the same on
// 2026-09-27: 0.0.0 got the catalog of a 0.153/0.154 release, with
// gpt-6-astra (0.153.0) in it and gpt-6-sol (0.155.0) left out.
const codexSourceBuildVersion = "0.154.0"

// upstreamServing starts a stand-in provider endpoint that answers the model
// list with the given ids and records the credential it was called with. An
// entry may carry a context window as "id:131072" - the stand-in reports it
// under the OpenRouter spelling, context_length - or as "id:catalog", the
// window the embedded models catalog has for the id.
func (w *providerModelsWorld) upstreamServing(t *testing.T, csv string) error {
	ids := strings.Split(csv, ",")
	var data []map[string]interface{}
	for _, part := range ids {
		part = strings.TrimSpace(part)
		id, ctx := part, 0
		if i := strings.LastIndex(part, ":"); i > 0 {
			id = part[:i]
			if part[i+1:] == "catalog" {
				limits, ok := models.Lookup(id)
				if !ok {
					return fmt.Errorf("the models catalog does not list %q any more: pick another model for this scenario", id)
				}
				ctx = limits.ContextWindow
			} else {
				ctx, _ = strconv.Atoi(part[i+1:])
			}
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

// codexCatalogStandIn starts a stand-in for the Codex backend's model
// catalog, GET /models, gating it the way the live backend does: client_version
// is required and must read x.y.z, a model is offered only to a client at or
// above its minimal_client_version, and a source build (0.0.0) is answered as
// codexSourceBuildVersion. Every row reports the 272000-token window the live
// catalog reports.
func (w *providerModelsWorld) codexCatalogStandIn(t *testing.T, csv, minVersion string) error {
	w.codexOffers(csv, minVersion)
	w.codex = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			http.NotFound(rw, r)
			return
		}
		raw := r.URL.Query().Get("client_version")
		w.codexMu.Lock()
		defer w.codexMu.Unlock()
		w.codexVersions = append(w.codexVersions, raw)
		client, ok := parseCodexVersion(raw)
		if !ok {
			http.Error(rw, `{"detail":"Invalid client_version format"}`, http.StatusBadRequest)
			return
		}
		if raw == "0.0.0" {
			client, _ = parseCodexVersion(codexSourceBuildVersion)
		}
		models := []map[string]any{}
		for i, row := range w.codexRows {
			minimal, _ := parseCodexVersion(row.minVersion)
			if codexVersionBelow(client, minimal) {
				continue
			}
			models = append(models, map[string]any{
				"slug":                   row.slug,
				"display_name":           strings.ToUpper(row.slug),
				"visibility":             "list",
				"priority":               i + 1,
				"minimal_client_version": row.minVersion,
				"context_window":         272000,
			})
		}
		rw.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(rw).Encode(map[string]any{"models": models})
	}))
	t.Cleanup(w.codex.Close)
	return nil
}

// codexOffers adds catalog rows offered from the given Codex release on.
func (w *providerModelsWorld) codexOffers(csv, minVersion string) {
	w.codexMu.Lock()
	defer w.codexMu.Unlock()
	for _, slug := range strings.Split(csv, ",") {
		w.codexRows = append(w.codexRows, codexCatalogRow{slug: strings.TrimSpace(slug), minVersion: minVersion})
	}
}

// parseCodexVersion reads a Codex client version; ok is false for anything
// but x.y.z.
func parseCodexVersion(s string) (v [3]int, ok bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// codexVersionBelow reports whether release a is older than release b.
func codexVersionBelow(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// gatewayWithSignedInCodex boots a gateway whose config holds a codex
// provider row "codex" with a Coddy-managed ChatGPT credential, and points
// every Codex request of this process at the stand-in backend. CODEX_HOME is
// an empty directory, so a Codex CLI login of whoever runs the tests is never
// read.
func (w *providerModelsWorld) gatewayWithSignedInCodex(t *testing.T) error {
	if w.codex == nil {
		return fmt.Errorf("Codex stand-in not started")
	}
	t.Setenv(llm.EnvCodexBaseURL, w.codex.URL)
	t.Setenv("CODEX_HOME", t.TempDir())
	home := t.TempDir()
	yml := `providers:
  - name: codex
    type: codex
models:
  - model: codex/gpt-6-astra
agent:
  model: codex/gpt-6-astra
`
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(yml), 0o644); err != nil {
		return err
	}
	token := codexE2ETestJWT(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	auth, err := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens":    map[string]any{"access_token": token, "refresh_token": "rt-models", "account_id": "acct-models"},
	})
	if err != nil {
		return err
	}
	authPath := config.CodexAuthPath(home, "codex")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(authPath, auth, 0o600); err != nil {
		return err
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home})
	if err != nil {
		return err
	}
	w.serve(t, cfg, home)
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
	w.serve(t, cfg, home)
	return nil
}

// serve runs the gateway over cfg with a runner that ends every turn at once.
func (w *providerModelsWorld) serve(t *testing.T, cfg *config.Config, home string) {
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	srv := New(cfg, mgr, slog.Default(), home)
	w.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(w.ts.Close)
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
	return w.readModels(res)
}

// getSaved reads the model list of a saved provider row through
// GET /coddy/providers/{name}/models, the route that looks the row up in the
// saved config instead of merging a posted one.
func (w *providerModelsWorld) getSaved(name string) error {
	if w.ts == nil {
		return fmt.Errorf("gateway not started")
	}
	res, err := http.Get(w.ts.URL + "/coddy/providers/" + name + "/models")
	if err != nil {
		return err
	}
	return w.readModels(res)
}

// readModels records the {"ok","models"} answer both routes share.
func (w *providerModelsWorld) readModels(res *http.Response) error {
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d, want 200", res.StatusCode)
	}
	var payload struct {
		OK     bool `json:"ok"`
		Models []struct {
			ID            string `json:"id"`
			ContextWindow int    `json:"context_window"`
			Catalog       *struct {
				ContextWindow int `json:"context_window"`
				MaxTokens     int `json:"max_tokens"`
			} `json:"catalog"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return err
	}
	w.ok = payload.OK
	w.models = nil
	w.ctx = map[string]int{}
	w.catalog = map[string]*models.Limits{}
	for _, m := range payload.Models {
		w.models = append(w.models, m.ID)
		w.ctx[m.ID] = m.ContextWindow
		if m.Catalog != nil {
			w.catalog[m.ID] = &models.Limits{ContextWindow: m.Catalog.ContextWindow, MaxTokens: m.Catalog.MaxTokens}
		}
	}
	return nil
}

func (w *providerModelsWorld) wantModels(csv string) error {
	if !w.ok {
		return fmt.Errorf("response reported ok:false")
	}
	if strings.Join(w.models, ",") != csv {
		if w.codex != nil {
			w.codexMu.Lock()
			defer w.codexMu.Unlock()
			return fmt.Errorf("models = %v, want %v (the Codex catalog was asked with client_version %q)", w.models, csv, w.codexVersions)
		}
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

// wantCatalog holds the answer to the embedded models catalog, whatever its
// numbers are after the next refresh.
func (w *providerModelsWorld) wantCatalog(id string) error {
	want, ok := models.Lookup(id)
	if !ok {
		return fmt.Errorf("the models catalog does not list %q any more: pick another model for this scenario", id)
	}
	if got := w.catalog[id]; got == nil || *got != want {
		return fmt.Errorf("catalog for %q = %+v, want %+v", id, got, want)
	}
	return nil
}

func (w *providerModelsWorld) wantNoCatalog(id string) error {
	if got := w.catalog[id]; got != nil {
		return fmt.Errorf("catalog for %q = %+v, want none", id, *got)
	}
	return nil
}

// modelListReportsWindow checks the window GET /v1/models reports for a model
// row, the number the web UI draws its context ring against and automatic
// compaction measures against.
func (w *providerModelsWorld) modelListReportsWindow(want int, ref string) error {
	if w.ts == nil {
		return fmt.Errorf("gateway not started")
	}
	res, err := http.Get(w.ts.URL + "/v1/models")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Data []struct {
			ID               string `json:"id"`
			MaxContextTokens int    `json:"max_context_tokens"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode /v1/models: %w", err)
	}
	for _, m := range body.Data {
		if m.ID == ref {
			if m.MaxContextTokens != want {
				return fmt.Errorf("GET /v1/models max_context_tokens for %s = %d, want %d", ref, m.MaxContextTokens, want)
			}
			return nil
		}
	}
	return fmt.Errorf("GET /v1/models has no %s row", ref)
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
			sc.Step(`^a stand-in Codex backend whose catalog offers "([^"]*)" from Codex (\d+\.\d+\.\d+)$`, func(csv, minVersion string) error {
				return w.codexCatalogStandIn(t, csv, minVersion)
			})
			sc.Step(`^the Codex catalog offers "([^"]*)" from Codex (\d+\.\d+\.\d+)$`, func(csv, minVersion string) error {
				w.codexOffers(csv, minVersion)
				return nil
			})
			sc.Step(`^a coddy server holding a codex provider signed in to that backend$`, func() error {
				return w.gatewayWithSignedInCodex(t)
			})
			sc.Step(`^the settings form posts the provider row "([^"]*)" of type "([^"]*)" at that upstream with key "([^"]*)"$`, w.postRow)
			sc.Step(`^the settings form posts only the provider name "([^"]*)"$`, w.postNameOnly)
			sc.Step(`^the settings form reads the models of the saved provider "([^"]*)"$`, w.getSaved)
			sc.Step(`^the gateway answers with the models "([^"]*)"$`, w.wantModels)
			sc.Step(`^the gateway answers with context window (\d+) for "([^"]*)"$`, w.wantCtx)
			sc.Step(`^the gateway answers with the window and max_tokens the models catalog has for "([^"]*)"$`, w.wantCatalog)
			sc.Step(`^the gateway answers with nothing from the models catalog for "([^"]*)"$`, w.wantNoCatalog)
			sc.Step(`^the upstream saw the key "([^"]*)"$`, w.upstreamSawKey)
			sc.Step(`^the model list reports the context window (\d+) for "([^"]*)"$`, w.modelListReportsWindow)
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
