package main

// Godog harness for the @cli scenarios of features/devin_provider.feature:
// drives `coddy providers login devin` against the offline Devin stand
// (internal/devinfake), with the browser faked to follow the sign-in page
// back to the loopback listener, and inspects what the command stored.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/devinfake"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const devinLoginStandToken = "devin-session-token$login-stand"

// devinStandModels is the catalog both harnesses serve: a family with
// reasoning variants and paid SKU variants, and a family of one uid.
func devinStandModels() []devinfake.Model {
	var out []devinfake.Model
	for _, lv := range []string{"medium", "low", "high"} {
		out = append(out, devinfake.Model{
			UID: "claude-opus-5-" + lv, Label: "Claude Opus 5 " + strings.ToUpper(lv[:1]) + lv[1:],
			Family: "claude-opus-5", FamilyLabel: "Claude Opus 5", Default: lv == "medium",
			ContextWindow: 1000000, MaxOutput: 128000, Images: true,
		})
	}
	out = append(out, devinfake.Model{
		UID: "claude-opus-5-high-fast", Label: "Claude Opus 5 High Fast",
		Family: "claude-opus-5", FamilyLabel: "Claude Opus 5", ContextWindow: 1000000, MaxOutput: 128000,
	})
	out = append(out, devinfake.Model{
		UID: "swe-1-6", Label: "SWE-1.6", Family: "swe-1.6", FamilyLabel: "SWE-1.6",
		ContextWindow: 200000, MaxOutput: 64000,
	})
	return out
}

type devinLoginState struct {
	stand   *devinfake.Server
	ts      *httptest.Server
	home    string
	stdout  string
	runErr  error
	envPrev map[string]*string

	mu         sync.Mutex
	browserErr error

	prevOpen    func(string) error
	prevBrowser func() bool
	prevPaste   io.Reader
	restoreWait func()
}

func (s *devinLoginState) setEnv(name, value string) error {
	if _, saved := s.envPrev[name]; !saved {
		if prev, ok := os.LookupEnv(name); ok {
			s.envPrev[name] = &prev
		} else {
			s.envPrev[name] = nil
		}
	}
	return os.Setenv(name, value)
}

func (s *devinLoginState) reset() error {
	s.close()
	s.envPrev = map[string]*string{}
	s.stdout, s.runErr, s.browserErr = "", nil, nil
	s.prevOpen, s.prevBrowser = openBrowserFn, localBrowserAvailableFn
	s.prevPaste = devinPasteInput
	// Nothing in the scenario types into the terminal.
	devinPasteInput = nil
	// A sign-in that never gets its callback waits ten minutes in production.
	s.restoreWait = llm.SetDevinLoginTimeout(20 * time.Second)
	return nil
}

func (s *devinLoginState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.home != "" {
		_ = os.RemoveAll(s.home)
		s.home = ""
	}
	for name, prev := range s.envPrev {
		if prev == nil {
			_ = os.Unsetenv(name)
		} else {
			_ = os.Setenv(name, *prev)
		}
	}
	s.envPrev = nil
	if s.prevOpen != nil {
		openBrowserFn, localBrowserAvailableFn = s.prevOpen, s.prevBrowser
		devinPasteInput = s.prevPaste
		s.prevOpen, s.prevBrowser, s.prevPaste = nil, nil, nil
	}
	if s.restoreWait != nil {
		s.restoreWait()
		s.restoreWait = nil
	}
}

func (s *devinLoginState) standServingFamilies() error {
	s.stand = devinfake.New(devinfake.Options{SessionToken: devinLoginStandToken, Models: devinStandModels()})
	s.ts = httptest.NewServer(s.stand)
	for _, env := range []string{llm.EnvDevinAPIServerURL, llm.EnvDevinWebappURL, llm.EnvDevinAPIURL} {
		if err := s.setEnv(env, s.ts.URL); err != nil {
			return err
		}
	}
	return nil
}

func (s *devinLoginState) freshHome() error {
	home, err := os.MkdirTemp("", "coddy-bdd-devin-login-*")
	if err != nil {
		return err
	}
	s.home = home
	// The developer's own Devin CLI login must not leak into the scenario.
	if err := s.setEnv(llm.EnvDevinCLICredentials, filepath.Join(home, "no-devin-cli", "credentials.toml")); err != nil {
		return err
	}
	return s.setEnv("DEVIN_API_KEY", "")
}

func (s *devinLoginState) devinCLILogin() error {
	path := filepath.Join(s.home, "devin-cli", "credentials.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := fmt.Sprintf("windsurf_api_key = %q\napi_server_url = %q\n", devinLoginStandToken, s.ts.URL)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return err
	}
	return s.setEnv(llm.EnvDevinCLICredentials, path)
}

// aBrowserThatSignsIn plays the person at the keyboard: the browser it
// "opens" loads the sign-in page, which sends it back to the loopback
// listener with the code.
func (s *devinLoginState) aBrowserThatSignsIn() {
	localBrowserAvailableFn = func() bool { return true }
	openBrowserFn = func(url string) error {
		go func() {
			var err error
			for attempt := 0; attempt < 5; attempt++ {
				var resp *http.Response
				resp, err = http.Get(url) //nolint:gosec // the stand and this flow's own loopback listener
				if err == nil {
					if resp.StatusCode != http.StatusOK {
						err = fmt.Errorf("the callback answered %s", resp.Status)
					}
					_ = resp.Body.Close()
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if err != nil {
				s.mu.Lock()
				s.browserErr = err
				s.mu.Unlock()
			}
		}()
		return nil
	}
}

func (s *devinLoginState) run(args ...string) error {
	full := append(append([]string{}, args...), "--home", s.home)
	out, err := captureStdout(func() { s.runErr = runProviders(full) })
	if err != nil {
		return err
	}
	s.stdout = out
	return nil
}

func (s *devinLoginState) runBrowserLogin() error {
	s.aBrowserThatSignsIn()
	if err := s.run("login", "devin"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.browserErr != nil {
		return fmt.Errorf("browser: %w", s.browserErr)
	}
	if s.runErr != nil {
		return fmt.Errorf("login failed: %w\n%s", s.runErr, s.stdout)
	}
	return nil
}

func (s *devinLoginState) runCLILogin() error {
	openBrowserFn = func(url string) error { return fmt.Errorf("no browser expected, asked to open %s", url) }
	if err := s.run("login", "devin", "--devin-cli"); err != nil {
		return err
	}
	if s.runErr != nil {
		return fmt.Errorf("login failed: %w\n%s", s.runErr, s.stdout)
	}
	return nil
}

func (s *devinLoginState) tokenStoredPrivately() error {
	path := config.DevinAuthPath(s.home, "devin")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("credential not stored: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return fmt.Errorf("credential mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), devinLoginStandToken[len("devin-session-token$"):]) {
		return fmt.Errorf("stored credential does not hold the stand's session token: %s", data)
	}
	if s.stand.Exchanges() != 1 {
		return fmt.Errorf("code exchanges = %d, want 1", s.stand.Exchanges())
	}
	return nil
}

func (s *devinLoginState) noManagedCredential() error {
	if _, err := os.Stat(config.DevinAuthPath(s.home, "devin")); !os.IsNotExist(err) {
		return fmt.Errorf("a Coddy-managed credential was written (stat err %v)", err)
	}
	return nil
}

func (s *devinLoginState) loadConfig() (*config.Config, error) {
	return config.LoadFromCLI(config.CLIPaths{Home: s.home})
}

func (s *devinLoginState) configListsProvider(name, typ string) error {
	cfg, err := s.loadConfig()
	if err != nil {
		return err
	}
	prov := cfg.FindProvider(name)
	if prov == nil || prov.Type != typ {
		return fmt.Errorf("provider %q of type %q not in config: %+v", name, typ, cfg.Providers)
	}
	return nil
}

func (s *devinLoginState) configListsModelWithLevels(ref, levels, def string) error {
	cfg, err := s.loadConfig()
	if err != nil {
		return err
	}
	m := cfg.FindModelEntry(ref)
	if m == nil {
		return fmt.Errorf("model %q not in config", ref)
	}
	if m.ReasoningLevels == nil || strings.Join(*m.ReasoningLevels, ",") != levels {
		return fmt.Errorf("model %q reasoning_levels = %v, want %s", ref, m.ReasoningLevels, levels)
	}
	if m.ReasoningDefault != def {
		return fmt.Errorf("model %q reasoning_default = %q, want %q", ref, m.ReasoningDefault, def)
	}
	if m.MaxContextTokens != 1000000 {
		return fmt.Errorf("model %q max_context_tokens = %d, want the catalog's 1000000", ref, m.MaxContextTokens)
	}
	return nil
}

func (s *devinLoginState) configListsModelWithoutLevels(ref string) error {
	cfg, err := s.loadConfig()
	if err != nil {
		return err
	}
	m := cfg.FindModelEntry(ref)
	if m == nil {
		return fmt.Errorf("model %q not in config", ref)
	}
	if m.ReasoningLevels == nil || len(*m.ReasoningLevels) != 0 {
		return fmt.Errorf("model %q reasoning_levels = %v, want an explicit empty list", ref, m.ReasoningLevels)
	}
	return nil
}

func (s *devinLoginState) agentModelIs(want string) error {
	cfg, err := s.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Agent.Model != want {
		return fmt.Errorf("agent.model = %q, want %q", cfg.Agent.Model, want)
	}
	return nil
}

func (s *devinLoginState) listReports(name, want string) error {
	out, err := captureStdout(func() { s.runErr = runProviders([]string{"list", "--home", s.home}) })
	if err != nil {
		return err
	}
	if s.runErr != nil {
		return s.runErr
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, name+" (devin)") && strings.Contains(line, want) {
			return nil
		}
	}
	return fmt.Errorf("providers list does not report %q for %s:\n%s", want, name, out)
}

func (s *devinLoginState) listSignedInAs(name, email string) error {
	return s.listReports(name, "signed in to Devin ("+email)
}

func (s *devinLoginState) listUsesDevinCLI(name string) error {
	return s.listReports(name, "Devin CLI login ")
}

func initializeDevinLoginScenario(sc *godog.ScenarioContext) {
	s := &devinLoginState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a Devin stand serving the Claude Opus 5 and SWE-1\.6 families$`, s.standServingFamilies)
	sc.Step(`^a fresh CODDY_HOME with an empty config and no Devin CLI login$`, s.freshHome)
	sc.Step(`^a Devin CLI login holding the stand's session token$`, s.devinCLILogin)
	sc.Step(`^I run "providers login devin" and the browser completes the sign-in$`, s.runBrowserLogin)
	sc.Step(`^I run "providers login devin --devin-cli"$`, s.runCLILogin)
	sc.Step(`^the Devin session token is stored with owner-only permissions$`, s.tokenStoredPrivately)
	sc.Step(`^no Coddy-managed Devin credential is written$`, s.noManagedCredential)
	sc.Step(`^config\.yaml lists provider "([^"]+)" of type "([^"]+)"$`, s.configListsProvider)
	sc.Step(`^config\.yaml lists model "([^"]+)" with reasoning levels "([^"]+)" and default "([^"]+)"$`, s.configListsModelWithLevels)
	sc.Step(`^config\.yaml lists model "([^"]+)" with no reasoning levels$`, s.configListsModelWithoutLevels)
	sc.Step(`^agent\.model is "([^"]+)"$`, s.agentModelIs)
	sc.Step(`^"providers list" reports provider "([^"]+)" signed in as "([^"]+)"$`, s.listSignedInAs)
	sc.Step(`^"providers list" reports provider "([^"]+)" using the Devin CLI login$`, s.listUsesDevinCLI)
}

func TestDevinProviderLoginFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "devin_provider_cli",
		ScenarioInitializer: initializeDevinLoginScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/devin_provider.feature"},
			Tags:     "@cli",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("devin_provider @cli feature failed")
	}
}
