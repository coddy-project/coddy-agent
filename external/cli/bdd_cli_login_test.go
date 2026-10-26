//go:build cli

package cli

// Steps of features/cli_login.feature: a console over a fresh install (no
// config.yaml, no provider), and /login against a stand-in NeuralDeep hub
// that serves the device sign-in, the account and the tier's catalog. The
// suite is TestCLITUIFeature's; the steps are registered by
// registerCLILoginSteps.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// loginBDDUserCode is the code the stand-in hub asks the person to confirm.
const loginBDDUserCode = "WXYZ-2345"

// loginStand is the stand-in hub of a /login scenario and the environment it
// replaced, put back by stop.
type loginStand struct {
	hub     *httptest.Server
	env     map[string]*string
	restore func()
}

func (l *loginStand) stop() {
	if l == nil {
		return
	}
	if l.hub != nil {
		l.hub.Close()
	}
	if l.restore != nil {
		l.restore()
	}
	for name, prev := range l.env {
		if prev == nil {
			_ = os.Unsetenv(name)
		} else {
			_ = os.Setenv(name, *prev)
		}
	}
}

func (l *loginStand) setenv(name, value string) error {
	if _, saved := l.env[name]; !saved {
		if prev, ok := os.LookupEnv(name); ok {
			l.env[name] = &prev
		} else {
			l.env[name] = nil
		}
	}
	return os.Setenv(name, value)
}

func registerCLILoginSteps(sc *godog.ScenarioContext, s *cliTUIState) {
	sc.Step(`^a coddy console app with no model provider configured$`, s.buildFreshApp)
	sc.Step(`^a stand-in NeuralDeep hub whose tier serves the models "([^"]*)" and "([^"]*)"$`, s.standInLoginHub)
	sc.Step(`^the screen shows the hub's verification page and the code "([^"]*)"$`, s.screenShowsVerificationPage)
	sc.Step(`^config\.yaml lists the provider "([^"]*)" with the models "([^"]*)" and "([^"]*)"$`, s.configListsProviderAndModels)
	sc.Step(`^the screen no longer shows "([^"]*)"$`, s.footerNoLongerShows)
	sc.Step(`^the model selector offers "([^"]*)"$`, s.modelSelectorOffers)
	sc.Step(`^the slash menu offers "([^"]*)"$`, s.slashMenuOffers)
	sc.Step(`^a coddy console app on the example config with OPENAI_API_KEY unset$`, s.buildExampleConfigApp)
	sc.Step(`^config\.yaml names "([^"]*)" as agent\.model$`, s.configNamesAgentModel)
}

// exampleConfig is the part of config.example.yaml that decides a fresh
// install.sh setup: an openai row reading OPENAI_API_KEY, its model, and
// agent.model on it.
const exampleConfig = `providers:
  - name: openai
    type: openai
    api_key: "${OPENAI_API_KEY}"
models:
  - model: openai/gpt-5.6-terra
agent:
  model: openai/gpt-5.6-terra
`

// buildExampleConfigApp is buildFreshApp over the example config, loaded the
// way coddy loads it, with OPENAI_API_KEY unset.
func (s *cliTUIState) buildExampleConfigApp() error {
	if s.loginStand == nil {
		s.loginStand = &loginStand{env: map[string]*string{}}
	}
	if err := s.loginStand.setenv("OPENAI_API_KEY", ""); err != nil {
		return err
	}
	bare, err := s.freshConfig()
	if err != nil {
		return err
	}
	if err := os.WriteFile(bare.Paths.ConfigPath, []byte(exampleConfig), 0o644); err != nil {
		return err
	}
	loaded, err := config.LoadWithPaths(bare.Paths)
	if err != nil {
		return err
	}
	loaded.Tools.PermissionMode = bare.Tools.PermissionMode
	loaded.Rules.AutoDiscover = bare.Rules.AutoDiscover
	// The app and the manager start over the loaded configuration, the way a
	// start of coddy reads it. Replacing it afterwards raced with the
	// manager's watchers, which read it from the moment they start.
	return s.buildAppOver(loaded)
}

func (s *cliTUIState) configNamesAgentModel(model string) error {
	deadline := time.Now().Add(within(3 * time.Second))
	for {
		cfg, err := config.LoadWithPaths(s.cfg.Paths)
		if err == nil && cfg.Agent.Model == model {
			return nil
		}
		if time.Now().After(deadline) {
			got := ""
			if cfg != nil {
				got = cfg.Agent.Model
			}
			return fmt.Errorf("config.yaml names %q as agent.model, want %q (%v)", got, model, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// buildFreshApp assembles the console over a home with no config.yaml: no
// provider, no model, the paths a sign-in writes config.yaml to.
func (s *cliTUIState) buildFreshApp() error {
	cfg, err := s.freshConfig()
	if err != nil {
		return err
	}
	return s.buildAppOver(cfg)
}

// freshConfig makes the scenario's home, with no config.yaml in it, and the
// bare configuration over it.
func (s *cliTUIState) freshConfig() (*config.Config, error) {
	s.home = filepath.Join(os.TempDir(), fmt.Sprintf("coddy-cli-login-%d", time.Now().UnixNano()))
	s.homes = append(s.homes, s.home)
	s.cwd = filepath.Join(s.home, "work")
	for _, d := range []string{s.home, s.cwd, filepath.Join(s.home, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	noAuto := false
	cfg := &config.Config{Paths: config.Paths{
		Home:       s.home,
		CWD:        s.cwd,
		ConfigPath: filepath.Join(s.home, "config.yaml"),
	}}
	cfg.Tools.PermissionMode = "ask"
	cfg.Rules.AutoDiscover = &noAuto
	return cfg, nil
}

// buildAppOver assembles the console and its manager over cfg.
func (s *cliTUIState) buildAppOver(cfg *config.Config) error {
	s.cfg = cfg
	s.store = &session.FileStore{Root: filepath.Join(s.home, "sessions")}

	log := slog.New(slog.DiscardHandler)
	late := &lateBoundSender{}
	mgr := session.NewManager(cfg, late, s.stubRunner, log, s.cwd, s.store)
	wireLocalManager(mgr)
	s.mgr = mgr
	app := newApp(cfg, mgr, log, &bddTerminal{cols: 100, rows: 35}, "dark", true)
	app.installConfig = mgr.InstallConfig
	// No browser opens from a test; the verification page is on the screen.
	app.browserAvailable = func() bool { return false }
	app.openURL = func(string) error { return nil }
	late.inner = app.Sender()
	s.app = app
	return nil
}

// standInLoginHub serves the device sign-in (confirmed on the first poll),
// whoami and the tier status with two chat models, and answers the usage
// read the console makes once it runs on the new provider. Both the hub and
// the API base point at it, so nothing leaves the machine.
func (s *cliTUIState) standInLoginHub(first, second string) error {
	stand := &loginStand{env: map[string]*string{}}
	var hub *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cli/device/start", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dc-secret", "user_code": loginBDDUserCode,
			"verification_uri":          hub.URL + "/device",
			"verification_uri_complete": hub.URL + "/device?code=" + loginBDDUserCode,
			"interval":                  1, "expires_in": 900,
		})
	})
	mux.HandleFunc("/api/cli/device/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "sk-console-login-0123456789", "token_type": "bearer"})
	})
	mux.HandleFunc("/api/cli/whoami", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"email": "dev@example.test", "name": "dev", "tier": "pro"})
	})
	mux.HandleFunc("/api/cli/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tier": "pro", "models": []map[string]any{
			{"id": first, "mode": "chat", "ctx": 262144},
			{"id": second, "mode": "chat", "ctx": 131072},
		}})
	})
	mux.HandleFunc("/limits", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	hub = httptest.NewServer(mux)
	stand.hub = hub
	stand.restore = llm.SetNeuralDeepLoginTimeout(20 * time.Second)
	s.loginStand = stand
	for name, value := range map[string]string{
		llm.EnvNeuralDeepHubURL:  hub.URL,
		llm.EnvNeuralDeepBaseURL: hub.URL,
		"NEURALDEEP_API_KEY":     "",
	} {
		if err := stand.setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *cliTUIState) screenShowsVerificationPage(code string) error {
	if err := s.waitScreen(s.loginStand.hub.URL+"/device", 5*time.Second); err != nil {
		return err
	}
	return s.waitScreen(code, 2*time.Second)
}

// configListsProviderAndModels reads config.yaml from the disk, the way the
// next start of coddy will.
func (s *cliTUIState) configListsProviderAndModels(provider, first, second string) error {
	cfg, err := config.LoadWithPaths(s.cfg.Paths)
	if err != nil {
		return err
	}
	if cfg.FindProvider(provider) == nil {
		return fmt.Errorf("config.yaml lists no provider %q", provider)
	}
	for _, ref := range []string{first, second} {
		if cfg.FindModelEntry(ref) == nil {
			return fmt.Errorf("config.yaml lists no model %q", ref)
		}
	}
	return nil
}

// modelSelectorOffers opens the model selector (ctrl+l), looks for the model
// and closes it again.
func (s *cliTUIState) modelSelectorOffers(model string) error {
	s.press("\x0c")
	if err := s.waitScreen("Select model", 3*time.Second); err != nil {
		return err
	}
	err := s.waitScreen(model, 2*time.Second)
	s.press("\x1b")
	return err
}

// slashMenuOffers reads the open autocomplete list for one command row: a
// line that starts with the command (after the selection arrow) and goes on
// with its description. The header's /login line names the command too, so
// a word anywhere on the screen proves nothing.
func (s *cliTUIState) slashMenuOffers(name string) error {
	deadline := time.Now().Add(within(3 * time.Second))
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(s.screenText(), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && fields[0] == "→" {
				fields = fields[1:]
			}
			if len(fields) > 1 && fields[0] == name {
				return nil
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("the slash menu offers no %q; last frame:\n%s", name, s.screenText())
}
