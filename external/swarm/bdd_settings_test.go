//go:build swarm

package swarm

// Godog harness for features/swarm_relay_settings.feature: the relay's own
// settings routes over its real HTTP surface and auth gate, against a
// config.yaml on disk, with the runtime's install replaced by a recorder.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

type settingsFeatureState struct {
	dir   string
	path  string
	token string
	ts    *httptest.Server

	mu        sync.Mutex
	live      *config.Config
	installed []*config.Config

	status int
	body   []byte
}

func (s *settingsFeatureState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
		s.dir = ""
	}
}

func (s *settingsFeatureState) relayWithSettingsFile(name, token string) error {
	s.close()
	dir, err := os.MkdirTemp("", "coddy-relay-settings-*")
	if err != nil {
		return err
	}
	s.dir, s.token = dir, token
	s.path = filepath.Join(dir, "config.yaml")
	// The host this relay runs on has a model provider too: its key is none of
	// a relay client's business.
	if err := os.Setenv("RELAY_TEST_PROVIDER_KEY", "sk-host-secret"); err != nil {
		return err
	}
	raw := fmt.Sprintf(`# the relay in the office rack
httpserver:
  enable: false
providers:
  - name: openai
    type: openai
    api_key: "${RELAY_TEST_PROVIDER_KEY}"
swarm:
  enable: true
  host: "127.0.0.1"
  port: 12346
  name: %q
  auth_token: %q # rotated every quarter
  pairing_tokens: ["pair-secret"]
`, name, token)
	if err := os.WriteFile(s.path, []byte(raw), 0o600); err != nil {
		return err
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: dir, CWD: dir, ConfigPath: s.path})
	if err != nil {
		return err
	}
	s.live = cfg
	srv, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	srv.EnableSettings(
		func() *config.Config {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.live
		},
		func(c *config.Config) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.live = c
			s.installed = append(s.installed, c)
			return nil
		},
	)
	s.ts = httptest.NewServer(srv.Handler())
	return nil
}

func (s *settingsFeatureState) do(method, path, bearer string, body []byte) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.ts.URL+path, rd)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.status = res.StatusCode
	s.body, _ = io.ReadAll(res.Body)
	return nil
}

func (s *settingsFeatureState) readSchema() error {
	return s.do(http.MethodGet, "/coddy/config/schema", s.token, nil)
}

func (s *settingsFeatureState) readSettings() error {
	return s.do(http.MethodGet, "/coddy/config", s.token, nil)
}

func (s *settingsFeatureState) readSettingsAnonymously() error {
	return s.do(http.MethodGet, "/coddy/config", "", nil)
}

func (s *settingsFeatureState) okBody() (map[string]any, error) {
	if s.status != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", s.status, s.body)
	}
	var doc map[string]any
	if err := json.Unmarshal(s.body, &doc); err != nil {
		return nil, fmt.Errorf("decode: %v: %s", err, s.body)
	}
	return doc, nil
}

func (s *settingsFeatureState) schemaIsARelays(list string) error {
	doc, err := s.okBody()
	if err != nil {
		return err
	}
	if doc["x-coddy-relay"] != true {
		return fmt.Errorf("the schema does not say it is a relay's: %s", s.body)
	}
	props, _ := doc["properties"].(map[string]any)
	want := strings.Split(list, ", ")
	if len(props) != len(want) {
		return fmt.Errorf("sections %v, want %v", keys(props), want)
	}
	for _, k := range want {
		if _, ok := props[k]; !ok {
			return fmt.Errorf("section %q missing: %v", k, keys(props))
		}
	}
	return nil
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (s *settingsFeatureState) settingsNameTheRelay(name string) error {
	doc, err := s.okBody()
	if err != nil {
		return err
	}
	sw, _ := doc["swarm"].(map[string]any)
	if sw["name"] != name {
		return fmt.Errorf("swarm.name = %v, want %q", sw["name"], name)
	}
	return nil
}

func (s *settingsFeatureState) settingsHideTheToken() error {
	doc, err := s.okBody()
	if err != nil {
		return err
	}
	if strings.Contains(string(s.body), s.token) || strings.Contains(string(s.body), "pair-secret") {
		return fmt.Errorf("the settings carry a credential: %s", s.body)
	}
	sw, _ := doc["swarm"].(map[string]any)
	if sw["auth_configured"] != true {
		return fmt.Errorf("the settings do not say a client token is set: %s", s.body)
	}
	return nil
}

func (s *settingsFeatureState) settingsCarryOnly(list string) error {
	doc, err := s.okBody()
	if err != nil {
		return err
	}
	want := map[string]bool{"revision": true}
	for _, k := range strings.Split(list, ", ") {
		want[k] = true
	}
	for k := range doc {
		if !want[k] {
			return fmt.Errorf("the settings carry the section %q: %s", k, s.body)
		}
	}
	if strings.Contains(string(s.body), "sk-host-secret") {
		return fmt.Errorf("the settings carry the host's provider key: %s", s.body)
	}
	return nil
}

func (s *settingsFeatureState) fileKeepsProvider() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(raw), `api_key: "${RELAY_TEST_PROVIDER_KEY}"`) {
		return fmt.Errorf("the saved file lost the provider as written:\n%s", raw)
	}
	return nil
}

func (s *settingsFeatureState) saveAllowingOrigin(origin string) error {
	if err := s.readSettings(); err != nil {
		return err
	}
	doc, err := s.okBody()
	if err != nil {
		return err
	}
	sw, _ := doc["swarm"].(map[string]any)
	sw["cors"] = map[string]any{"enable": true, "allowed_origins": []string{origin}}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return s.do(http.MethodPut, "/coddy/config", s.token, body)
}

func (s *settingsFeatureState) saveSucceeds() error {
	if s.status != http.StatusOK {
		return fmt.Errorf("status %d: %s", s.status, s.body)
	}
	return nil
}

func (s *settingsFeatureState) fileAllowsOrigin(origin string) error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: s.dir, CWD: s.dir, ConfigPath: s.path})
	if err != nil {
		return err
	}
	if !cfg.Swarm.CORS.Enabled || len(cfg.Swarm.CORS.AllowedOrigins) != 1 || cfg.Swarm.CORS.AllowedOrigins[0] != origin {
		return fmt.Errorf("the file's swarm.cors is %+v:\n%s", cfg.Swarm.CORS, raw)
	}
	return nil
}

func (s *settingsFeatureState) fileKeepsCommentsAndToken() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	text := string(raw)
	for _, want := range []string{"# the relay in the office rack", "# rotated every quarter", s.token, "pair-secret"} {
		if !strings.Contains(text, want) {
			return fmt.Errorf("the saved file lost %q:\n%s", want, text)
		}
	}
	return nil
}

func (s *settingsFeatureState) relayWasHandedTheSettings() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.installed) != 1 {
		return fmt.Errorf("%d configurations installed, want 1", len(s.installed))
	}
	got := s.installed[0]
	if !got.Swarm.CORS.Enabled || got.Swarm.AuthToken != s.token {
		return fmt.Errorf("installed swarm %+v", got.Swarm)
	}
	return nil
}

// saveAllowingLoopback flips the loopback toggle the way the relay's Settings
// form does: the whole document back, with cors.allow_loopback on.
func (s *settingsFeatureState) saveAllowingLoopback() error {
	if err := s.readSettings(); err != nil {
		return err
	}
	doc, err := s.okBody()
	if err != nil {
		return err
	}
	sw, _ := doc["swarm"].(map[string]any)
	sw["cors"] = map[string]any{"enable": true, "allow_loopback": true}
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return s.do(http.MethodPut, "/coddy/config", s.token, body)
}

func (s *settingsFeatureState) fileAllowsLoopback() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: s.dir, CWD: s.dir, ConfigPath: s.path})
	if err != nil {
		return err
	}
	if !cfg.Swarm.CORS.Enabled || !cfg.Swarm.CORS.AllowLoopback {
		return fmt.Errorf("the file's swarm.cors is %+v:\n%s", cfg.Swarm.CORS, raw)
	}
	return nil
}

// relayWasHandedLoopback checks the handoff only: the harness records what
// the runtime would install, it does not rebuild the server under test. The
// rebuilt relay's behaviour is the fingerprint test's and swarm_mount's job.
func (s *settingsFeatureState) relayWasHandedLoopback() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.installed) != 1 {
		return fmt.Errorf("%d configurations installed, want 1", len(s.installed))
	}
	got := s.installed[0]
	if !got.Swarm.CORS.Enabled || !got.Swarm.CORS.AllowLoopback || got.Swarm.AuthToken != s.token {
		return fmt.Errorf("installed swarm %+v", got.Swarm)
	}
	return nil
}

func (s *settingsFeatureState) rejectedAsUnauthorized() error {
	if s.status != http.StatusUnauthorized {
		return fmt.Errorf("status %d, want 401: %s", s.status, s.body)
	}
	return nil
}

func TestSwarmRelaySettingsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "swarm_relay_settings",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &settingsFeatureState{}
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.close()
				return ctx, nil
			})
			sc.Step(`^a relay named "([^"]+)" whose settings live in a file, with client token "([^"]+)"$`, s.relayWithSettingsFile)
			sc.Step(`^I read the relay's settings schema with the client token$`, s.readSchema)
			sc.Step(`^the schema is a relay's, with the sections "([^"]+)"$`, s.schemaIsARelays)
			sc.Step(`^I read the relay's settings with the client token$`, s.readSettings)
			sc.Step(`^I read the relay's settings without a credential$`, s.readSettingsAnonymously)
			sc.Step(`^the settings name the relay "([^"]+)"$`, s.settingsNameTheRelay)
			sc.Step(`^the settings say a client token is set without carrying it$`, s.settingsHideTheToken)
			sc.Step(`^I save the relay's settings allowing the origin "([^"]+)" with the client token$`, s.saveAllowingOrigin)
			sc.Step(`^the save succeeds$`, s.saveSucceeds)
			sc.Step(`^the relay's file allows the origin "([^"]+)"$`, s.fileAllowsOrigin)
			sc.Step(`^the relay's file keeps its comments and its client token$`, s.fileKeepsCommentsAndToken)
			sc.Step(`^the relay was handed the new settings$`, s.relayWasHandedTheSettings)
			sc.Step(`^I save the relay's settings allowing loopback origins with the client token$`, s.saveAllowingLoopback)
			sc.Step(`^the relay's file allows loopback origins$`, s.fileAllowsLoopback)
			sc.Step(`^the relay was handed the new settings allowing loopback origins$`, s.relayWasHandedLoopback)
			sc.Step(`^the settings carry only the sections "([^"]+)"$`, s.settingsCarryOnly)
			sc.Step(`^the relay's file keeps the provider the settings do not show$`, s.fileKeepsProvider)
			sc.Step(`^the settings request is rejected as unauthorized$`, s.rejectedAsUnauthorized)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/swarm_relay_settings.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("swarm relay settings feature failed")
	}
}
