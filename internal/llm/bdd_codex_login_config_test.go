package llm

// Godog harness for the @cli scenario of features/codex_auth.feature: the
// half of a ChatGPT sign-in that happens after the token is stored. A stored
// credential plus a stand-in Codex backend serving the real catalog shape
// (slug, display_name, visibility, priority) drive ApplyCodexLoginToConfig
// against a real config.yaml on disk, so what the operator can pick after the
// login is what the scenario asserts.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// codexLoginBDDCatalog mirrors the live Codex catalog: two models the Codex
// picker hides ("hide"), and listed ones whose priority decides the ranking.
// Priorities are deliberately out of both alphabetical and file order so a
// harness that sorts by anything else cannot pass by accident.
const codexLoginBDDCatalog = `{"models":[
  {"slug":"gpt-5.6-luna","display_name":"GPT-5.6-Luna","visibility":"list","priority":8},
  {"slug":"codex-auto-review","display_name":"Codex Auto Review","visibility":"hide","priority":43},
  {"slug":"gpt-6-astra","display_name":"GPT-6-Astra","visibility":"list","priority":1},
  {"slug":"gpt-reserve","display_name":"GPT-Reserve","visibility":"hide","priority":3},
  {"slug":"gpt-5.6-sol","display_name":"GPT-5.6-Sol","visibility":"list","priority":6}
]}`

type codexLoginBDDState struct {
	home     string
	cfgPath  string
	authPath string
	backend  *httptest.Server

	added    []string
	lastYAML string
}

func (s *codexLoginBDDState) reset() error {
	home, err := os.MkdirTemp("", "coddy-codex-login-bdd-*")
	if err != nil {
		return err
	}
	s.home = home
	s.cfgPath = filepath.Join(home, "config.yaml")
	s.authPath = ""
	s.added = nil
	s.lastYAML = ""
	s.backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || filepath.Base(r.URL.Path) != "models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, codexLoginBDDCatalog)
	}))
	return nil
}

func (s *codexLoginBDDState) close() {
	if s.backend != nil {
		s.backend.Close()
		s.backend = nil
	}
	if s.home != "" {
		_ = os.RemoveAll(s.home)
		s.home = ""
	}
}

// codexLoginBDDJWT builds an unsigned JWT-shaped token: only the payload is
// read (exp for refresh decisions, chatgpt_account_id for the header).
func codexLoginBDDJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func (s *codexLoginBDDState) givenFreshHomeWithCredential() error {
	if err := os.WriteFile(s.cfgPath, []byte("providers: []\nmodels: []\n"), 0o600); err != nil {
		return err
	}
	s.authPath = config.CodexAuthPath(s.home, "codex")
	if s.authPath == "" {
		return fmt.Errorf("could not resolve the codex credential path under %s", s.home)
	}
	if err := os.MkdirAll(filepath.Dir(s.authPath), 0o700); err != nil {
		return err
	}
	token := codexLoginBDDJWT(map[string]any{
		"exp":                         4102444800, // far future: never refreshed during the scenario
		"chatgpt_account_id":          "acct-bdd",
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-bdd"},
	})
	auth := map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token":      token,
			"access_token":  token,
			"refresh_token": "rt-bdd",
			"account_id":    "acct-bdd",
		},
		"last_refresh": "2026-09-09T00:00:00Z",
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.authPath, data, 0o600)
}

func (s *codexLoginBDDState) whenLoginAppliesCatalog() error {
	yamlBefore, err := os.ReadFile(s.cfgPath)
	if err != nil {
		return err
	}
	s.lastYAML = string(yamlBefore)

	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: s.home})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	// The stand-in backend replaces the official one for this process only.
	if err := os.Setenv(EnvCodexBaseURL, s.backend.URL); err != nil {
		return err
	}
	defer func() { _ = os.Unsetenv(EnvCodexBaseURL) }()

	added, err := ApplyCodexLoginToConfig(context.Background(), cfg, "codex", s.authPath, "")
	if err != nil {
		return fmt.Errorf("ApplyCodexLoginToConfig: %w", err)
	}
	s.added = added
	return nil
}

// reloaded reads config.yaml back from disk, so the assertions see what a next
// `coddy` run would load rather than the in-memory value the apply mutated.
func (s *codexLoginBDDState) reloaded() (*config.Config, error) {
	return config.LoadFromCLI(config.CLIPaths{Home: s.home})
}

func (s *codexLoginBDDState) thenConfigGainedProviderAndModels() error {
	cfg, err := s.reloaded()
	if err != nil {
		return err
	}
	prov := cfg.FindProvider("codex")
	if prov == nil {
		return fmt.Errorf("config has no codex provider; added %v", s.added)
	}
	if prov.Type != "codex" {
		return fmt.Errorf("codex provider type = %q, want codex", prov.Type)
	}
	for _, want := range []string{"codex/gpt-6-astra", "codex/gpt-5.6-sol", "codex/gpt-5.6-luna"} {
		if cfg.FindModelEntry(want) == nil {
			return fmt.Errorf("config is missing model %q; added %v", want, s.added)
		}
	}
	return nil
}

func (s *codexLoginBDDState) thenHiddenModelsLeftOut() error {
	cfg, err := s.reloaded()
	if err != nil {
		return err
	}
	for _, hidden := range []string{"codex/gpt-reserve", "codex/codex-auto-review"} {
		if cfg.FindModelEntry(hidden) != nil {
			return fmt.Errorf("model %q is hidden by Codex and must not be written to config", hidden)
		}
	}
	return nil
}

func (s *codexLoginBDDState) thenAgentModelIsTopRanked() error {
	cfg, err := s.reloaded()
	if err != nil {
		return err
	}
	// priority 1 in the catalog, and neither first alphabetically nor first
	// in the served order.
	if cfg.Agent.Model != "codex/gpt-6-astra" {
		return fmt.Errorf("agent.model = %q, want codex/gpt-6-astra", cfg.Agent.Model)
	}
	return nil
}

func (s *codexLoginBDDState) thenConfigUnchanged() error {
	if len(s.added) != 0 {
		return fmt.Errorf("a repeated login reported changes: %v", s.added)
	}
	current, err := os.ReadFile(s.cfgPath)
	if err != nil {
		return err
	}
	if string(current) != s.lastYAML {
		return fmt.Errorf("config.yaml was rewritten by a repeated login:\n--- before ---\n%s\n--- after ---\n%s", s.lastYAML, current)
	}
	return nil
}

func initializeCodexLoginConfigScenario(sc *godog.ScenarioContext) {
	s := &codexLoginBDDState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a fresh CODDY_HOME with an empty config and a stored Codex credential$`, s.givenFreshHomeWithCredential)
	sc.Step(`^the Codex login applies the catalog to the config$`, s.whenLoginAppliesCatalog)
	sc.Step(`^the config gains the codex provider and its subscription models$`, s.thenConfigGainedProviderAndModels)
	sc.Step(`^the catalog models Codex hides are left out of the config$`, s.thenHiddenModelsLeftOut)
	sc.Step(`^agent\.model is the model Codex ranks first$`, s.thenAgentModelIsTopRanked)
	sc.Step(`^the config is left unchanged$`, s.thenConfigUnchanged)
}

func TestCodexLoginConfigE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "codex_auth_cli",
		ScenarioInitializer: initializeCodexLoginConfigScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/codex_auth.feature"},
			Tags:     "@cli",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("codex_auth @cli feature failed")
	}
}
