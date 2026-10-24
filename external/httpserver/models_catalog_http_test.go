//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const catalogDoc = `{
  "anthropic": {"id": "anthropic", "models": {
    "claude-sonnet-4-5": {"modalities": {"input": ["text", "image"]}, "limit": {"context": 200000, "output": 64000}}
  }},
  "openrouter": {"id": "openrouter", "api": "https://openrouter.ai/api/v1", "models": {
    "anthropic/claude-sonnet-4.5": {"modalities": {"input": ["text"]}, "limit": {"context": 1000000, "output": 64000}}
  }}
}`

// catalogStand serves catalogDoc, or the status set in fail, and counts requests.
type catalogStand struct {
	*httptest.Server
	requests atomic.Int32
	fail     atomic.Int32
}

func newCatalogStand(t *testing.T) *catalogStand {
	t.Helper()
	s := &catalogStand{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.requests.Add(1)
		if code := s.fail.Load(); code != 0 {
			w.WriteHeader(int(code))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalogDoc))
	}))
	t.Cleanup(s.Close)
	return s
}

func catalogServer(t *testing.T, mc config.ModelsCatalogConfig, providers ...config.ProviderConfig) *httptest.Server {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		Paths:         config.Paths{Home: t.TempDir(), CWD: root},
		Providers:     providers,
		ModelsCatalog: mc,
	}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), root, &session.FileStore{Root: t.TempDir()})
	srv := New(cfg, mgr, slog.Default(), root)
	t.Cleanup(srv.Drain)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postCatalog(t *testing.T, ts *httptest.Server, body string) (int, modelsCatalogAnswer) {
	t.Helper()
	res, err := http.Post(ts.URL+"/coddy/models/catalog", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out modelsCatalogAnswer
	if res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode, out
}

// models_catalog.enable: false answers that the catalog is off and sends it
// nothing.
func TestModelsCatalogRouteTurnedOffAsksNothing(t *testing.T) {
	stand := newCatalogStand(t)
	off := false
	ts := catalogServer(t, config.ModelsCatalogConfig{Enabled: &off, URL: stand.URL})
	code, out := postCatalog(t, ts, `{"type":"anthropic","models":[{"id":"claude-sonnet-4-5"}]}`)
	if code != http.StatusOK || !out.OK || out.Enabled || len(out.Models) != 0 {
		t.Fatalf("status %d, answer %+v; want ok, disabled, no models", code, out)
	}
	if n := stand.requests.Load(); n != 0 {
		t.Errorf("a disabled catalog was asked %d time(s)", n)
	}
}

// Fields the body leaves empty come from the saved row of the same name.
func TestModelsCatalogRouteInheritsTheSavedRow(t *testing.T) {
	stand := newCatalogStand(t)
	ts := catalogServer(t, config.ModelsCatalogConfig{URL: stand.URL},
		config.ProviderConfig{Name: "router", Type: "openai", APIBase: "https://openrouter.ai/api/v1"})
	_, out := postCatalog(t, ts, `{"name":"router","models":[{"id":"anthropic/claude-sonnet-4.5"},{"id":"not-listed"},{"id":""}]}`)
	if out.Provider != "openrouter" {
		t.Fatalf("the saved row matched %q, want openrouter: %+v", out.Provider, out)
	}
	if len(out.Models) != 1 || out.Models["anthropic/claude-sonnet-4.5"].ContextWindow != 1000000 || out.Models["anthropic/claude-sonnet-4.5"].Multimodal {
		t.Errorf("models = %+v; want only the listed one, text only", out.Models)
	}
}

// A row the catalog does not carry matches nothing and is suggested nothing.
func TestModelsCatalogRouteRowWithoutAProvider(t *testing.T) {
	stand := newCatalogStand(t)
	ts := catalogServer(t, config.ModelsCatalogConfig{URL: stand.URL})
	_, out := postCatalog(t, ts, `{"type":"openai","api_base":"http://127.0.0.1:8000/v1","models":[{"id":"claude-sonnet-4-5"}]}`)
	if !out.OK || out.Provider != "" || len(out.Models) != 0 {
		t.Fatalf("a local server was matched: %+v", out)
	}
}

// With neither a copy nor a download the answer says so with HTTP 200, and
// the form keeps its fields as they are.
func TestModelsCatalogRouteCatalogUnavailable(t *testing.T) {
	stand := newCatalogStand(t)
	stand.fail.Store(http.StatusServiceUnavailable)
	ts := catalogServer(t, config.ModelsCatalogConfig{URL: stand.URL})
	code, out := postCatalog(t, ts, `{"type":"anthropic","models":[{"id":"claude-sonnet-4-5"}]}`)
	if code != http.StatusOK || out.OK || !out.Enabled || out.Error == "" || len(out.Models) != 0 {
		t.Fatalf("status %d, answer %+v; want ok false with the error", code, out)
	}
}

func TestModelsCatalogRouteRefusals(t *testing.T) {
	stand := newCatalogStand(t)
	ts := catalogServer(t, config.ModelsCatalogConfig{URL: stand.URL})

	res, err := http.Post(ts.URL+"/coddy/models/catalog", "text/plain", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("a text/plain body: status %d, want 415", res.StatusCode)
	}

	if code, _ := postCatalog(t, ts, `{"models":`); code != http.StatusBadRequest {
		t.Errorf("a malformed body: status %d, want 400", code)
	}

	var many bytes.Buffer
	many.WriteString(`{"type":"anthropic","models":[`)
	for i := range modelsCatalogMaxModels + 1 {
		if i > 0 {
			many.WriteByte(',')
		}
		fmt.Fprintf(&many, `{"id":"m%d"}`, i)
	}
	many.WriteString(`]}`)
	if code, _ := postCatalog(t, ts, many.String()); code != http.StatusBadRequest {
		t.Errorf("%d models: status %d, want 400", modelsCatalogMaxModels+1, code)
	}
	if n := stand.requests.Load(); n != 0 {
		t.Errorf("a refused request reached the catalog %d time(s)", n)
	}
}
