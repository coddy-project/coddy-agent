package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type configToolsFeatureState struct {
	dir      string
	path     string
	env      *tooling.Env
	registry *Registry
	result   string
	lastErr  error
	reloads  int
	reloaded *config.Config
}

func (s *configToolsFeatureState) reset() error {
	dir, err := os.MkdirTemp("", "coddy-bdd-config-tools-*")
	if err != nil {
		return err
	}
	s.dir = dir
	s.path = filepath.Join(dir, "config.yaml")
	s.registry = NewRegistry()
	s.env = &tooling.Env{
		ConfigPath: s.path,
		ConfigHome: dir,
		ConfigCWD:  dir,
	}
	s.env.ReloadConfig = func(context.Context) ([]string, error) {
		s.reloads++
		cfg, err := config.LoadWithPaths(config.Paths{Home: dir, CWD: dir, ConfigPath: s.path})
		if err != nil {
			return nil, err
		}
		s.reloaded = cfg
		return nil, nil
	}
	return nil
}

func (s *configToolsFeatureState) cleanup() {
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

func (s *configToolsFeatureState) activeConfigWithoutMCP() error {
	return os.WriteFile(s.path, []byte("agent:\n  max_turns: 17\nmcp_servers: []\n"), 0o600)
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

func (s *configToolsFeatureState) updateSucceeds() error {
	if s.lastErr != nil {
		return s.lastErr
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
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := s.registry.Execute(context.Background(), "config_get", string(args), s.env)
	if err != nil {
		return err
	}
	var got struct {
		Exists bool        `json:"exists"`
		Value  interface{} `json:"value"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		return err
	}
	if !got.Exists || got.Value != want {
		return fmt.Errorf("config_get(%s) = %#v, want %q", path, got.Value, want)
	}
	return nil
}

func (s *configToolsFeatureState) unrelatedAgentSettingRemains() error {
	if s.reloaded == nil || s.reloaded.Agent.MaxTurns != 17 {
		return fmt.Errorf("agent.max_turns was not preserved")
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

	sc.Step(`^an active Coddy config with no MCP servers$`, s.activeConfigWithoutMCP)
	sc.Step(`^the agent sets config path "([^"]+)" to:$`, s.setConfigPath)
	sc.Step(`^the config update succeeds$`, s.updateSucceeds)
	sc.Step(`^the runtime config is reloaded once$`, s.reloadedOnce)
	sc.Step(`^config path "([^"]+)" equals "([^"]*)"$`, s.configPathEquals)
	sc.Step(`^the config still contains the unrelated agent setting$`, s.unrelatedAgentSettingRemains)
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
