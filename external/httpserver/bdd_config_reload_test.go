//go:build http

package httpserver

// Godog harness for features/config_reload_broadcast.feature: proves a hot reload of
// the live configuration is announced on GET /coddy/events, and that a client acting
// on the announcement reads the new configuration rather than the outgoing one.
// Everything goes over the real HTTP surface; no LLM is involved.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// configReloadBaseYAML is the configuration on disk before the scenario saves over it.
const configReloadBaseYAML = `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096

agent:
  model: "openai/gpt-4o"
`

// configReloadSavedJSON is the settings-form body adding one model to the saved set.
const configReloadSavedJSON = `{"providers":[{"name":"openai","type":"openai","api_key":"k"},` +
	`{"name":"rpa","type":"openai","api_key":"k2"}],` +
	`"models":[{"model":"openai/gpt-4o","max_tokens":4096},{"model":"rpa/qwen3.6-35b-a3b","max_tokens":16384}],` +
	`"agent":{"model":"openai/gpt-4o"}}`

// eventsClient is one browser holding a GET /coddy/events subscription.
type eventsClient struct {
	body  *bufio.Reader
	close func()
}

type configReloadState struct {
	root    string
	cfgPath string
	ts      *httptest.Server
	srv     *Server

	stopWatch context.CancelFunc
	watchDone chan struct{}

	browsers []*eventsClient
}

func (s *configReloadState) reset() {
	s.close()
	s.root, _ = os.MkdirTemp("", "coddy-cfgreload-*")
}

func (s *configReloadState) close() {
	if s.stopWatch != nil {
		s.stopWatch()
		<-s.watchDone
		s.stopWatch = nil
	}
	for _, b := range s.browsers {
		b.close()
	}
	s.browsers = nil
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// startServer boots a server over a real config.yaml, because the save path this
// feature exercises writes, backs up and re-reads that file.
func (s *configReloadState) startServer() error {
	home := filepath.Join(s.root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	s.cfgPath = filepath.Join(home, "config.yaml")
	if err := os.WriteFile(s.cfgPath, []byte(configReloadBaseYAML), 0o644); err != nil {
		return err
	}
	cfg, err := config.Load(s.cfgPath)
	if err != nil {
		return err
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.root, nil)
	s.srv = New(cfg, mgr, slog.Default(), s.root)
	s.ts = httptest.NewServer(s.srv.Handler())

	// The daemon watches the file it loaded, which is how an edit made by
	// somebody else - `coddy providers login` in another terminal, an operator
	// with an editor - reaches the clients this feature is about. The interval
	// is short because the scenario is waiting on it, not because anything
	// depends on the exact value.
	watch := &config.FileWatcher{
		Paths:    cfg.Paths,
		Interval: 20 * time.Millisecond,
		Live:     mgr.Cfg,
		Install:  mgr.ReplaceConfig,
		Log:      slog.Default(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.stopWatch = cancel
	s.watchDone = make(chan struct{})
	go func() {
		defer close(s.watchDone)
		_ = watch.Run(ctx)
	}()
	return nil
}

// addModelToConfigFile is somebody else rewriting config.yaml: a `coddy
// providers login` that added a provider and its models, or an operator's
// editor. Nothing tells the running process about it, which is why the file is
// watched.
func (s *configReloadState) addModelToConfigFile(model string) error {
	provider, _, ok := strings.Cut(model, "/")
	if !ok {
		return fmt.Errorf("model %q names no provider", model)
	}
	added := fmt.Sprintf(`
providers:
  - name: openai
    type: openai
    api_key: "k"
  - name: %[1]s
    type: openai
    api_key: "k2"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
  - model: "%[2]s"
    max_tokens: 16384

agent:
  model: "openai/gpt-4o"
`, provider, model)
	return os.WriteFile(s.cfgPath, []byte(added), 0o644)
}

// subscribe opens one events stream and drains the connect-time snapshot, so a later
// read sees only what happened after the subscription.
func (s *configReloadState) subscribe() error {
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/coddy/events", nil)
	if err != nil {
		cancel()
		return err
	}
	res, err := s.ts.Client().Do(req)
	if err != nil {
		cancel()
		return err
	}
	if res.StatusCode != http.StatusOK {
		cancel()
		res.Body.Close()
		return errStatus("events stream", res.StatusCode, "")
	}
	c := &eventsClient{
		body: bufio.NewReader(res.Body),
		close: func() {
			cancel()
			res.Body.Close()
		},
	}
	s.browsers = append(s.browsers, c)
	return c.await("event: ready")
}

// await reads whole SSE frames until one contains want.
func (c *eventsClient) await(want string) error {
	var seen strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		line, err := c.body.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read events (seen %q): %w", seen.String(), err)
		}
		seen.WriteString(line)
		if strings.Contains(seen.String(), want) && strings.HasSuffix(seen.String(), "\n\n") {
			return nil
		}
	}
	return fmt.Errorf("timed out waiting for %q, seen %q", want, seen.String())
}

// saveConfigWithModel is the settings form saving over the whole document.
func (s *configReloadState) saveConfigWithModel(model string) error {
	if !strings.Contains(configReloadSavedJSON, model) {
		return fmt.Errorf("the saved configuration does not carry %q", model)
	}
	req, err := http.NewRequest(http.MethodPut, s.ts.URL+"/coddy/config", strings.NewReader(configReloadSavedJSON))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.ts.Client().Do(req)
	if err != nil {
		return err
	}
	body, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		return errStatus("config save", res.StatusCode, string(body))
	}
	return nil
}

func (s *configReloadState) firstBrowserToldReloaded() error {
	if len(s.browsers) == 0 {
		return fmt.Errorf("no events subscription")
	}
	return s.browsers[0].await(`event: config_reloaded`)
}

func (s *configReloadState) allBrowsersToldReloaded() error {
	if len(s.browsers) < 2 {
		return fmt.Errorf("want at least two subscriptions, have %d", len(s.browsers))
	}
	for i, b := range s.browsers {
		if err := b.await(`event: config_reloaded`); err != nil {
			return fmt.Errorf("browser %d: %w", i+1, err)
		}
	}
	return nil
}

// modelsListAfterEventCarries is the whole point of the ordering: a client that reads
// the model list the instant it sees the event must never catch the outgoing config.
func (s *configReloadState) modelsListAfterEventCarries(model string) error {
	res, err := s.ts.Client().Get(s.ts.URL + "/v1/models")
	if err != nil {
		return err
	}
	body, _ := ioReadAllClose(res.Body)
	if res.StatusCode != http.StatusOK {
		return errStatus("model list", res.StatusCode, string(body))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("model list is not JSON: %w", err)
	}
	for _, m := range parsed.Data {
		if m.ID == model {
			return nil
		}
	}
	return fmt.Errorf("model list does not carry %q: %s", model, string(body))
}

func initializeConfigReloadScenario(sc *godog.ScenarioContext) {
	s := &configReloadState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy server with a saved configuration$`, s.startServer)
	sc.Step(`^a browser is subscribed to the server event stream$`, s.subscribe)
	sc.Step(`^a second browser is subscribed to the server event stream$`, s.subscribe)
	sc.Step(`^the configuration is saved with the model "([^"]*)" added$`, s.saveConfigWithModel)
	sc.Step(`^another process adds the model "([^"]*)" to config\.yaml$`, s.addModelToConfigFile)
	sc.Step(`^the browser is told the configuration reloaded$`, s.firstBrowserToldReloaded)
	sc.Step(`^both browsers are told the configuration reloaded$`, s.allBrowsersToldReloaded)
	sc.Step(`^the model list the browser reads after that event carries "([^"]*)"$`, s.modelsListAfterEventCarries)
}

func TestConfigReloadBroadcastFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "config-reload-broadcast",
		ScenarioInitializer: initializeConfigReloadScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/config_reload_broadcast.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("config reload broadcast feature suite failed")
	}
}
