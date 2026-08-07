package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type configToolsRead struct {
	ConfigFile string      `json:"config_file"`
	Path       string      `json:"path"`
	Exists     bool        `json:"exists"`
	Value      interface{} `json:"value"`
	Redacted   bool        `json:"redacted"`
}

type configToolsFeatureState struct {
	dir      string
	path     string
	env      *tooling.Env
	registry *Registry
	result   string
	lastErr  error
	reloads  int
	reloaded *config.Config
	readRaw  string
	read     configToolsRead
}

func (s *configToolsFeatureState) reset() error {
	dir, err := os.MkdirTemp("", "coddy-bdd-config-tools-*")
	if err != nil {
		return err
	}
	s.dir = dir
	s.path = filepath.Join(dir, "config.yaml")
	s.registry = NewRegistry()
	s.result = ""
	s.lastErr = nil
	s.reloads = 0
	s.reloaded = nil
	s.readRaw = ""
	s.read = configToolsRead{}
	// Reload stays unwired until the scenario asks for a hot-reloadable session.
	s.env = &tooling.Env{
		ConfigPath: s.path,
		ConfigHome: dir,
		ConfigCWD:  dir,
	}
	return nil
}

func (s *configToolsFeatureState) cleanup() {
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

func (s *configToolsFeatureState) activeConfig(doc *godog.DocString) error {
	return os.WriteFile(s.path, []byte(doc.Content+"\n"), 0o600)
}

func (s *configToolsFeatureState) sessionCanReload() error {
	s.env.ReloadConfig = func(context.Context) ([]string, error) {
		s.reloads++
		cfg, err := config.LoadWithPaths(config.Paths{Home: s.dir, CWD: s.dir, ConfigPath: s.path})
		if err != nil {
			return nil, err
		}
		s.reloaded = cfg
		return nil, nil
	}
	return nil
}

func (s *configToolsFeatureState) readConfigPath(path string) error {
	args, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		return err
	}
	s.readRaw, s.lastErr = s.registry.Execute(context.Background(), "config_get", string(args), s.env)
	if s.lastErr != nil {
		return s.lastErr
	}
	s.read = configToolsRead{}
	return json.Unmarshal([]byte(s.readRaw), &s.read)
}

func (s *configToolsFeatureState) readReturns(want string) error {
	if !s.read.Exists {
		return fmt.Errorf("config path %q does not exist", s.read.Path)
	}
	if got := fmt.Sprintf("%v", s.read.Value); got != want {
		return fmt.Errorf("config_get(%s) = %q, want %q", s.read.Path, got, want)
	}
	return nil
}

func (s *configToolsFeatureState) readNamesConfigFile() error {
	if s.read.ConfigFile != s.path {
		return fmt.Errorf("config_file = %q, want %q", s.read.ConfigFile, s.path)
	}
	return nil
}

func (s *configToolsFeatureState) readIsRedacted() error {
	if !s.read.Redacted {
		return fmt.Errorf("config_get(%s) did not report redacted values", s.read.Path)
	}
	return nil
}

func (s *configToolsFeatureState) readDoesNotExpose(secret string) error {
	if strings.Contains(s.readRaw, secret) {
		return fmt.Errorf("config_get result leaked %q: %s", secret, s.readRaw)
	}
	return nil
}

func (s *configToolsFeatureState) setConfigPath(path string, value *godog.DocString) error {
	args, err := json.Marshal(map[string]interface{}{
		"path":  path,
		"value": json.RawMessage(value.Content),
	})
	if err != nil {
		return err
	}
	s.result, s.lastErr = s.registry.Execute(context.Background(), "config_set", string(args), s.env)
	return nil
}

func (s *configToolsFeatureState) deleteConfigPath(path string) error {
	args, err := json.Marshal(map[string]interface{}{"path": path, "delete": true})
	if err != nil {
		return err
	}
	s.result, s.lastErr = s.registry.Execute(context.Background(), "config_set", string(args), s.env)
	return nil
}

func (s *configToolsFeatureState) updateSucceededAndChanged() error {
	if s.lastErr != nil {
		return s.lastErr
	}
	var got struct {
		OK       bool `json:"ok"`
		Changed  bool `json:"changed"`
		Reloaded bool `json:"reloaded"`
	}
	if err := json.Unmarshal([]byte(s.result), &got); err != nil {
		return err
	}
	if !got.OK || !got.Changed || !got.Reloaded {
		return fmt.Errorf("config_set result = %s, want ok, changed, and reloaded", s.result)
	}
	return nil
}

func (s *configToolsFeatureState) reloadedOnce() error {
	if s.reloads != 1 {
		return fmt.Errorf("reloads = %d, want 1", s.reloads)
	}
	if s.reloaded == nil {
		return fmt.Errorf("runtime did not receive reloaded config")
	}
	return nil
}

func (s *configToolsFeatureState) configPathEquals(path, want string) error {
	if err := s.readConfigPath(path); err != nil {
		return err
	}
	return s.readReturns(want)
}

func (s *configToolsFeatureState) configPathAbsent(path string) error {
	if err := s.readConfigPath(path); err != nil {
		return err
	}
	if s.read.Exists {
		return fmt.Errorf("config path %q still exists with value %#v", path, s.read.Value)
	}
	return nil
}

func (s *configToolsFeatureState) reloadedMaxTurns(want int) error {
	if s.reloaded == nil {
		return fmt.Errorf("runtime did not receive reloaded config")
	}
	if s.reloaded.Agent.MaxTurns != want {
		return fmt.Errorf("agent.max_turns = %d, want %d", s.reloaded.Agent.MaxTurns, want)
	}
	return nil
}

// The pre-change backup written by the edit is refreshed by the reload that
// follows it, so the spec only pins the safety net down to its existence.
func (s *configToolsFeatureState) configBackupExists() error {
	if _, err := os.Stat(config.BackupPath(s.path)); err != nil {
		return fmt.Errorf("config backup: %w", err)
	}
	return nil
}

func initializeConfigToolsScenario(sc *godog.ScenarioContext) {
	s := &configToolsFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^an active Coddy config:$`, s.activeConfig)
	sc.Step(`^the session can hot-reload its runtime configuration$`, s.sessionCanReload)
	sc.Step(`^the agent reads config path "([^"]+)"$`, s.readConfigPath)
	sc.Step(`^the read returns "([^"]*)"$`, s.readReturns)
	sc.Step(`^the read names the active config file$`, s.readNamesConfigFile)
	sc.Step(`^the read is marked as redacted$`, s.readIsRedacted)
	sc.Step(`^the read does not expose "([^"]*)"$`, s.readDoesNotExpose)
	sc.Step(`^the agent sets config path "([^"]+)" to:$`, s.setConfigPath)
	sc.Step(`^the agent deletes config path "([^"]+)"$`, s.deleteConfigPath)
	sc.Step(`^the config update succeeds and reports the file was changed$`, s.updateSucceededAndChanged)
	sc.Step(`^the runtime config is reloaded once$`, s.reloadedOnce)
	sc.Step(`^config path "([^"]+)" equals "([^"]*)"$`, s.configPathEquals)
	sc.Step(`^config path "([^"]+)" is absent$`, s.configPathAbsent)
	sc.Step(`^the reloaded config still limits the agent to (\d+) turns$`, s.reloadedMaxTurns)
	sc.Step(`^a config backup sits next to the active file$`, s.configBackupExists)
}

func TestConfigToolsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "config-tools",
		ScenarioInitializer: initializeConfigToolsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/config_tools.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("config tools feature suite failed")
	}
}
