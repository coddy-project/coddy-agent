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
	// SessionDir points at the temp dir so staged commands persist file-backed,
	// the same way a persisted session does.
	s.env = &tooling.Env{
		SessionID:  filepath.Base(dir),
		SessionDir: dir,
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

func (s *configToolsFeatureState) stageConfigCommands(doc *godog.DocString) error {
	var commands []string
	for _, line := range strings.Split(doc.Content, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			commands = append(commands, trimmed)
		}
	}
	args, err := json.Marshal(map[string]interface{}{"commands": commands})
	if err != nil {
		return err
	}
	s.result, s.lastErr = s.registry.Execute(context.Background(), "config_set", string(args), s.env)
	return s.lastErr
}

func (s *configToolsFeatureState) pendingCommands() ([]string, error) {
	raw, err := s.registry.Execute(context.Background(), "config_changes", "{}", s.env)
	if err != nil {
		return nil, err
	}
	var got struct {
		Pending []string `json:"pending"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		return nil, err
	}
	return got.Pending, nil
}

func (s *configToolsFeatureState) stagingListsPending(want int) error {
	if s.lastErr != nil {
		return s.lastErr
	}
	var got struct {
		OK      bool     `json:"ok"`
		Pending []string `json:"pending"`
	}
	if err := json.Unmarshal([]byte(s.result), &got); err != nil {
		return err
	}
	if !got.OK || len(got.Pending) != want {
		return fmt.Errorf("config_set result = %s, want ok with %d pending commands", s.result, want)
	}
	return nil
}

func (s *configToolsFeatureState) listConfigChanges() error {
	s.result, s.lastErr = s.registry.Execute(context.Background(), "config_changes", "{}", s.env)
	return s.lastErr
}

func (s *configToolsFeatureState) changeListShows(command string) error {
	if s.lastErr != nil {
		return s.lastErr
	}
	var got struct {
		Pending []string `json:"pending"`
	}
	if err := json.Unmarshal([]byte(s.result), &got); err != nil {
		return err
	}
	for _, pending := range got.Pending {
		if pending == command {
			return nil
		}
	}
	return fmt.Errorf("config_changes = %s, want pending command %q", s.result, command)
}

func (s *configToolsFeatureState) commitStagedConfig() error {
	s.result, s.lastErr = s.registry.Execute(context.Background(), "config_commit", "{}", s.env)
	return nil
}

func (s *configToolsFeatureState) commitSucceededWithApplied() error {
	if s.lastErr != nil {
		return s.lastErr
	}
	var got struct {
		OK       bool     `json:"ok"`
		Applied  []string `json:"applied"`
		Reloaded bool     `json:"reloaded"`
	}
	if err := json.Unmarshal([]byte(s.result), &got); err != nil {
		return err
	}
	if !got.OK || len(got.Applied) == 0 || !got.Reloaded {
		return fmt.Errorf("config_commit result = %s, want ok, applied commands, and reloaded", s.result)
	}
	return nil
}

func (s *configToolsFeatureState) revertStagedConfig() error {
	s.result, s.lastErr = s.registry.Execute(context.Background(), "config_revert", "{}", s.env)
	return s.lastErr
}

func (s *configToolsFeatureState) rollbackCommittedConfig() error {
	s.result, s.lastErr = s.registry.Execute(context.Background(), "config_rollback", "{}", s.env)
	return nil
}

func (s *configToolsFeatureState) rollbackWarnsPreviousRestored() error {
	if s.lastErr != nil {
		return s.lastErr
	}
	var got struct {
		OK      bool   `json:"ok"`
		Warning string `json:"warning"`
	}
	if err := json.Unmarshal([]byte(s.result), &got); err != nil {
		return err
	}
	if !got.OK || !strings.Contains(got.Warning, "previous configuration") {
		return fmt.Errorf("config_rollback result = %s, want ok with a previous-configuration warning", s.result)
	}
	return nil
}

func (s *configToolsFeatureState) noCommandsRemainStaged() error {
	pending, err := s.pendingCommands()
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return fmt.Errorf("staged commands remain: %v", pending)
	}
	return nil
}

func (s *configToolsFeatureState) reloadedTimes(want int) error {
	if s.reloads != want {
		return fmt.Errorf("reloads = %d, want %d", s.reloads, want)
	}
	if want > 0 && s.reloaded == nil {
		return fmt.Errorf("runtime did not receive reloaded config")
	}
	return nil
}

func (s *configToolsFeatureState) reloadedOnce() error   { return s.reloadedTimes(1) }
func (s *configToolsFeatureState) reloadedTwice() error  { return s.reloadedTimes(2) }
func (s *configToolsFeatureState) notReloaded() error    { return s.reloadedTimes(0) }

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

func (s *configToolsFeatureState) preCommitSnapshotExists() error {
	if _, err := os.Stat(config.PrevConfigPath(s.path)); err != nil {
		return fmt.Errorf("pre-commit snapshot: %w", err)
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
	sc.Step(`^the agent stages config commands:$`, s.stageConfigCommands)
	sc.Step(`^the staging result lists (\d+) pending commands$`, s.stagingListsPending)
	sc.Step(`^the agent lists config changes$`, s.listConfigChanges)
	sc.Step(`^the change list shows "([^"]+)"$`, s.changeListShows)
	sc.Step(`^the agent commits the staged config$`, s.commitStagedConfig)
	sc.Step(`^the commit succeeds and reports the applied commands$`, s.commitSucceededWithApplied)
	sc.Step(`^the agent reverts the staged config$`, s.revertStagedConfig)
	sc.Step(`^the agent rolls back the committed config$`, s.rollbackCommittedConfig)
	sc.Step(`^the rollback warns that the previous configuration replaced the current one$`, s.rollbackWarnsPreviousRestored)
	sc.Step(`^no config commands remain staged$`, s.noCommandsRemainStaged)
	sc.Step(`^the runtime config is reloaded once$`, s.reloadedOnce)
	sc.Step(`^the runtime config is reloaded twice$`, s.reloadedTwice)
	sc.Step(`^the runtime config is not reloaded$`, s.notReloaded)
	sc.Step(`^config path "([^"]+)" equals "([^"]*)"$`, s.configPathEquals)
	sc.Step(`^config path "([^"]+)" is absent$`, s.configPathAbsent)
	sc.Step(`^the reloaded config still limits the agent to (\d+) turns$`, s.reloadedMaxTurns)
	sc.Step(`^a pre-commit snapshot sits next to the active file$`, s.preCommitSnapshotExists)
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
