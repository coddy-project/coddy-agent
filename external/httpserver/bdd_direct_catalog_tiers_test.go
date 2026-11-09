//go:build http

package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

func TestDirectCatalogTiersFeature(t *testing.T) {
	suite := godog.TestSuite{
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/direct_catalog_tiers.feature"}, TestingT: t},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^both direct endpoints accept a service tier advertised by the Codex catalog$`, func() error {
				return checkDirectCatalogTier(t, []string{"ultrafast"}, "ultrafast", http.StatusOK)
			})
		},
	}
	if suite.Run() != 0 {
		t.Fatal("direct catalog tiers failed")
	}
}

func TestDirectCatalogTierBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tiers     []string
		requested string
		status    int
	}{
		{"empty catalog", []string{}, "priority", http.StatusBadRequest},
		{"fast alias", []string{"priority"}, "fast", http.StatusOK},
		{"standard", []string{}, "default", http.StatusOK},
		{"absent catalog fallback", nil, "priority", http.StatusOK},
		{"absent catalog does not infer Ultra", nil, "ultrafast", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkDirectCatalogTier(t, tc.tiers, tc.requested, tc.status); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func checkDirectCatalogTier(t *testing.T, tiers []string, requested string, status int) error {
	t.Helper()
	cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "oauth", Type: "codex"}}, Models: []config.ModelEntry{{Model: "oauth/test", ReasoningLevels: &[]string{}}}}
	mgr := session.NewManager(cfg, noopSender{}, nil, slog.Default(), t.TempDir(), nil)
	mgr.SetContextWindowLister(func(context.Context, llm.ProviderInput) ([]llm.ModelEntry, error) {
		entry := llm.ModelEntry{ID: "test"}
		if tiers != nil {
			entry.ServiceTiers = &tiers
		}
		return []llm.ModelEntry{entry}, nil
	}, nil)
	mgr.AwaitContextWindows(context.Background(), cfg, []string{"oauth/test"}, time.Second)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	defer srv.Drain()
	calls := 0
	srv.makeLLMFromYAML = func(_ *config.Config, _ string, opts llm.RequestOptions) (llm.Provider, error) {
		calls++
		if opts.ServiceTier != requested {
			return nil, fmt.Errorf("tier=%q, want %q", opts.ServiceTier, requested)
		}
		return fakeProvider{reply: "ok"}, nil
	}
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses"} {
		body := fmt.Sprintf(`{"model":"oauth/test","service_tier":%q,"messages":[{"role":"user","content":"hi"}]}`, requested)
		if endpoint == "/v1/responses" {
			body = fmt.Sprintf(`{"model":"oauth/test","service_tier":%q,"input":"hi"}`, requested)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body)))
		if w.Code != status {
			return fmt.Errorf("%s: status=%d want=%d body=%s", endpoint, w.Code, status, w.Body.String())
		}
	}
	if status == http.StatusBadRequest && calls != 0 {
		return fmt.Errorf("refused tier reached provider %d times", calls)
	}
	return nil
}
