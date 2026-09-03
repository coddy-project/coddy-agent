package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// fakeHub emulates the NeuralDeep hub endpoints the providers command talks
// to: browser-flow start (an HTML page whose link carries state and key, as
// in production), whoami, status, and revoke.
func fakeHub(t *testing.T, key string) (*httptest.Server, *bool) {
	t.Helper()
	revoked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cli/auth/start":
			q := r.URL.Query()
			cb := fmt.Sprintf("http://127.0.0.1:%s/cb?state=%s&key=%s", q.Get("port"), q.Get("state"), key)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<html><body><a href="%s">continue</a></body></html>`, cb)
		case "/api/cli/whoami":
			_ = json.NewEncoder(w).Encode(map[string]string{"email": "u@e", "name": "tester", "tier": "starter"})
		case "/api/cli/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tier": "starter",
				"models": []map[string]any{
					{"id": "qwen3.6-35b-a3b", "mode": "chat", "ctx": 262144},
					{"id": "gpt-oss-120b", "mode": "chat", "ctx": 131072},
				},
			})
		case "/api/cli/revoke":
			revoked = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &revoked
}

// browseFollowingLink acts as the user's browser: opens the auth URL and
// follows the callback link from the hub's HTML page.
func browseFollowingLink(t *testing.T, authURL string) {
	t.Helper()
	resp, err := http.Get(authURL)
	if err != nil {
		t.Errorf("open auth url: %v", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	m := regexp.MustCompile(`href="([^"]+)"`).FindSubmatch(body)
	if m == nil {
		t.Errorf("no callback link in %s", body)
		return
	}
	cb, err := http.Get(string(m[1]))
	if err != nil {
		t.Errorf("follow callback: %v", err)
		return
	}
	_ = cb.Body.Close()
}

func TestProvidersUsage(t *testing.T) {
	if err := runProviders(nil); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("bare providers must print usage, got %v", err)
	}
	if err := runProviders([]string{"frobnicate"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("unknown subcommand must print usage, got %v", err)
	}
}

func TestProvidersLoginNeuralDeepStoresKeyAndWritesConfig(t *testing.T) {
	hub, _ := fakeHub(t, "sk-live-key")
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)
	home := t.TempDir()

	prevOpen := openBrowserFn
	openBrowserFn = func(url string) error {
		go browseFollowingLink(t, url)
		return nil
	}
	defer func() { openBrowserFn = prevOpen }()

	if err := runProviders([]string{"login", "neuraldeep", "--home", home}); err != nil {
		t.Fatalf("providers login: %v", err)
	}

	authPath := config.NeuralDeepAuthPath(home, "neuraldeep")
	key, err := llm.LoadNeuralDeepKey(authPath)
	if err != nil || key != "sk-live-key" {
		t.Fatalf("stored key = %q, %v", key, err)
	}

	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	prov := cfg.FindProvider("neuraldeep")
	if prov == nil || prov.Type != "neuraldeep" {
		t.Fatalf("provider not written to config: %+v", cfg.Providers)
	}
	if cfg.FindModelEntry("neuraldeep/qwen3.6-35b-a3b") == nil || cfg.FindModelEntry("neuraldeep/gpt-oss-120b") == nil {
		t.Fatalf("tier models not written, models = %+v", cfg.Models)
	}
	if e := cfg.FindModelEntry("neuraldeep/qwen3.6-35b-a3b"); e.MaxContextTokens != 262144 {
		t.Fatalf("model ctx = %d, want 262144", e.MaxContextTokens)
	}
	if !strings.HasPrefix(cfg.Agent.Model, "neuraldeep/") {
		t.Fatalf("agent.model = %q, want a neuraldeep model when it was unset", cfg.Agent.Model)
	}
}

func TestProvidersLoginAPIBaseSelectsTheDeployment(t *testing.T) {
	hub, _ := fakeHub(t, "sk-mirror-key")
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)
	home := t.TempDir()

	prevOpen := openBrowserFn
	openBrowserFn = func(url string) error {
		go browseFollowingLink(t, url)
		return nil
	}
	defer func() { openBrowserFn = prevOpen }()

	// An endpoint outside the allowlist is refused up front: signing in against
	// the wrong hub would only surface later, as rejected requests.
	err := runProviders([]string{"login", "neuraldeep", "--home", home, "--api-base", "https://example.invalid/v1"})
	if err == nil || !strings.Contains(err.Error(), "not a NeuralDeep endpoint") {
		t.Fatalf("unknown --api-base must be refused, got %v", err)
	}
	if _, statErr := os.Stat(config.NeuralDeepAuthPath(home, "neuraldeep")); !os.IsNotExist(statErr) {
		t.Fatal("a refused --api-base must not start the login")
	}

	mirror := "https://api.neuraldeep.tech/v1"
	if err := runProviders([]string{"login", "neuraldeep", "--home", home, "--api-base", mirror}); err != nil {
		t.Fatalf("providers login --api-base: %v", err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	prov := cfg.FindProvider("neuraldeep")
	if prov == nil || prov.APIBase != mirror {
		t.Fatalf("api_base not written to config: %+v", cfg.Providers)
	}
	if note := neuralDeepEndpointNote(prov); !strings.Contains(note, mirror) {
		t.Fatalf("providers list must name the mirror, got %q", note)
	}

	// Signing in again for the other deployment moves the existing row with
	// it: the new key would otherwise be sent to the hub that never issued it.
	if err := runProviders([]string{"login", "neuraldeep", "--home", home, "--api-base", "https://api.neuraldeep.ru/v1"}); err != nil {
		t.Fatalf("providers login --api-base (default): %v", err)
	}
	cfg, err = config.LoadFromCLI(config.CLIPaths{Home: home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	prov = cfg.FindProvider("neuraldeep")
	if prov == nil || prov.APIBase != "" {
		t.Fatalf("row must be back on the default deployment, got %+v", prov)
	}
	if note := neuralDeepEndpointNote(prov); note != "" {
		t.Fatalf("providers list must not name the default deployment, got %q", note)
	}
}

func TestProvidersLoginRepairsAnUnrecognizedAPIBase(t *testing.T) {
	hub, _ := fakeHub(t, "sk-repair-key")
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)
	home := t.TempDir()
	// A row whose api_base names no NeuralDeep endpoint: requests already fall
	// back to the default deployment, so a plain login (no --api-base) signs
	// in there and must leave the row saying so instead of keeping the value
	// the startup notice complains about.
	yaml := "providers:\n  - name: neuraldeep\n    type: neuraldeep\n    api_base: https://custom.example/v1\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	prevOpen := openBrowserFn
	openBrowserFn = func(url string) error {
		go browseFollowingLink(t, url)
		return nil
	}
	defer func() { openBrowserFn = prevOpen }()

	if err := runProviders([]string{"login", "neuraldeep", "--home", home}); err != nil {
		t.Fatalf("providers login: %v", err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	prov := cfg.FindProvider("neuraldeep")
	if prov == nil || prov.APIBase != "" {
		t.Fatalf("row must be repaired to the default deployment, got %+v", prov)
	}
	for _, n := range llm.NeuralDeepAuthNotices(cfg) {
		if strings.Contains(n.Message, "not a NeuralDeep endpoint") {
			t.Fatalf("the endpoint notice must be gone after the repair, got %q", n.Message)
		}
	}
}

func TestProvidersLoginNoConfigSkipsYAML(t *testing.T) {
	hub, _ := fakeHub(t, "sk-noconf")
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)
	home := t.TempDir()

	prevOpen := openBrowserFn
	openBrowserFn = func(url string) error {
		go browseFollowingLink(t, url)
		return nil
	}
	defer func() { openBrowserFn = prevOpen }()

	if err := runProviders([]string{"login", "neuraldeep", "--home", home, "--no-config"}); err != nil {
		t.Fatalf("providers login --no-config: %v", err)
	}
	if key, _ := llm.LoadNeuralDeepKey(config.NeuralDeepAuthPath(home, "neuraldeep")); key != "sk-noconf" {
		t.Fatalf("key not stored, got %q", key)
	}
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("--no-config must not create config.yaml")
	}
}

func TestProvidersLogoutRevokesAndForgets(t *testing.T) {
	hub, revoked := fakeHub(t, "sk-x")
	t.Setenv(llm.EnvNeuralDeepHubURL, hub.URL)
	home := t.TempDir()
	authPath := config.NeuralDeepAuthPath(home, "neuraldeep")
	if err := llm.SaveNeuralDeepAuth(authPath, "sk-x", hub.URL, "coddy", "coddy"); err != nil {
		t.Fatal(err)
	}

	if err := runProviders([]string{"logout", "neuraldeep", "--home", home}); err != nil {
		t.Fatalf("providers logout: %v", err)
	}
	if !*revoked {
		t.Fatal("logout must attempt the server-side revoke")
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatal("auth file must be removed on logout")
	}
}

func TestProvidersListShowsSourcesAndShadowing(t *testing.T) {
	home := t.TempDir()
	// Keep the credential-source assertions hermetic: a developer machine may
	// export provider keys (or a loaded .env may have).
	t.Setenv("NEURALDEEP_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	cfg := &config.Config{}
	cfg.Paths.Home = home
	cfg.Providers = []config.ProviderConfig{
		{Name: "neuraldeep", Type: "neuraldeep", APIKey: "sk-manual"},
		{Name: "openai", Type: "openai"},
	}
	if err := llm.SaveNeuralDeepAuth(config.NeuralDeepAuthPath(home, "neuraldeep"), "sk-oauth-1234", "https://hub", "coddy", "coddy"); err != nil {
		t.Fatal(err)
	}

	lines := providersListLines(cfg)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "neuraldeep") || !strings.Contains(joined, "api_key") {
		t.Fatalf("list must show the active source, got:\n%s", joined)
	}
	if !strings.Contains(joined, "overrides") {
		t.Fatalf("list must warn that the explicit api_key overrides the login, got:\n%s", joined)
	}
	if strings.Contains(joined, "sk-manual") || strings.Contains(joined, "sk-oauth-1234") {
		t.Fatalf("list must never print raw keys:\n%s", joined)
	}
}

func TestProvidersLoginRefusesPlainKeyTypes(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("providers:\n  - name: mine\n    type: openai\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runProviders([]string{"login", "mine", "--home", home})
	if err == nil || !strings.Contains(err.Error(), "api_key") {
		t.Fatalf("login for a plain-key provider must point at api_key, got %v", err)
	}
}
