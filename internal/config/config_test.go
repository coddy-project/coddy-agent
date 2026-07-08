package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestResolveCODDYHomeEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(config.EnvCODDYHome, tmp)
	t.Setenv(config.EnvCODDYCWD, "")
	t.Setenv(config.EnvCODDYConfig, "")

	p, err := config.Resolve(config.CLIPaths{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.Clean(p.Home), filepath.Clean(tmp); got != want {
		t.Fatalf("Home %q want %q", got, want)
	}
	if !filepath.IsAbs(p.CWD) {
		t.Fatalf("CWD not absolute: %q", p.CWD)
	}
	wantCfg := filepath.Join(filepath.Clean(tmp), "config.yaml")
	if got := filepath.Clean(p.ConfigPath); got != wantCfg {
		t.Fatalf("ConfigPath %q want %q", got, wantCfg)
	}
}

func TestExpandPathHelpers(t *testing.T) {
	t.Run("ExpandCWD", func(t *testing.T) {
		got := config.ExpandCWD("${CWD}/.skills", "/home/user/project")
		want := "/home/user/project/.skills"
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("ExpandCODDYHomeOnlyLeavesCWD", func(t *testing.T) {
		p := config.Paths{Home: "/h", CWD: "/launch"}
		s := config.ExpandCODDYHomeOnly("${CODDY_HOME}/x ${CWD}/y", p)
		if s != "/h/x ${CWD}/y" {
			t.Fatalf("got %q", s)
		}
	})
	t.Run("ExpandPathVarsUsesForwardSlashes", func(t *testing.T) {
		p := config.Paths{Home: `C:\Users\dev\.coddy`, CWD: `C:\work\proj`}
		got := config.ExpandPathVars(`dirs: ["${CODDY_HOME}/skills", "${CWD}/x"]`, p)
		want := `dirs: ["C:/Users/dev/.coddy/skills", "C:/work/proj/x"]`
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})
}

// Regression: ${CODDY_HOME} expanded into a double-quoted YAML scalar must not
// inject backslashes; Windows paths like C:\Users\... were parsed as escape
// sequences and failed with "did not find expected hexdecimal number".
func TestLoadFromYAML_BackslashHomeInQuotedScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
providers:
  - name: local
    type: openai
    api_key: "test-key"

models:
  - model: "local/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "local/gpt-4o"

skills:
  dirs:
    - "${CODDY_HOME}/extra"

sessions:
  dir: "${CODDY_HOME}/mysess"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := config.Paths{Home: `C:\Users\dev\.coddy`, CWD: dir, ConfigPath: path}
	cfg, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatalf("LoadWithPaths: %v", err)
	}
	if len(cfg.Skills.Dirs) != 1 {
		t.Fatalf("skills.dirs len: got %d", len(cfg.Skills.Dirs))
	}
	if got, want := filepath.ToSlash(cfg.Skills.Dirs[0]), "C:/Users/dev/.coddy/extra"; got != want {
		t.Errorf("skills.dirs[0]: got %q want %q", got, want)
	}
	if got, want := filepath.ToSlash(cfg.Sessions.Dir), "C:/Users/dev/.coddy/mysess"; got != want {
		t.Errorf("sessions.dir: got %q want %q", got, want)
	}
}

func TestLoadFromYAML_EndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)

	content := `
providers:
  - name: local
    type: openai
    api_key: "test-key"

models:
  - model: "local/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "local/gpt-4o"
  max_turns: 7

prompts:
  dir: "/tmp/coddy-e2e-prompts"

skills:
  dirs:
    - "${CODDY_HOME}/extra"

sessions:
  dir: "${CODDY_HOME}/mysess"

tools:
  permission_mode: ask
  command_allowlist:
    - "  go test  "

logger:
  level: warn
  format: json
  outputs: ["stderr"]
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Paths.ConfigPath == "" {
		t.Fatal("expected Paths.ConfigPath set")
	}

	if len(cfg.Models) != 1 || cfg.Models[0].Model != "local/gpt-4o" {
		t.Errorf("models: got %+v", cfg.Models)
	}
	if cfg.Agent.Model != "local/gpt-4o" {
		t.Errorf("agent.model: got %q", cfg.Agent.Model)
	}
	if cfg.Agent.MaxTurns != 7 {
		t.Errorf("agent.max_turns: got %d want 7", cfg.Agent.MaxTurns)
	}
	if cfg.Agent.LLMRetryMax != config.AgentDefaultLLMRetryMax {
		t.Errorf("agent.llm_retry_max default: got %d", cfg.Agent.LLMRetryMax)
	}

	wantPrompts := filepath.Clean("/tmp/coddy-e2e-prompts")
	if got := cfg.Prompts.ResolvedDir("/ignored-cwd"); got != wantPrompts {
		t.Errorf("prompts.ResolvedDir: got %q want %q", got, wantPrompts)
	}

	wantSkills0 := filepath.Join(home, "extra")
	if len(cfg.Skills.Dirs) != 1 {
		t.Fatalf("skills.dirs len: got %d", len(cfg.Skills.Dirs))
	}
	if filepath.Clean(cfg.Skills.Dirs[0]) != filepath.Clean(wantSkills0) {
		t.Errorf("skills.dirs[0]: got %q want %q", cfg.Skills.Dirs[0], wantSkills0)
	}

	wantSess := filepath.Join(home, "mysess")
	if got := cfg.ResolvedSessionsRoot(); filepath.Clean(got) != filepath.Clean(wantSess) {
		t.Errorf("ResolvedSessionsRoot: got %q want %q", got, wantSess)
	}

	if len(cfg.Tools.CommandAllowlist) != 1 || cfg.Tools.CommandAllowlist[0] != "go test" {
		t.Errorf("tools.command_allowlist trimmed: got %#v", cfg.Tools.CommandAllowlist)
	}
	if cfg.Tools.PermissionMode != config.PermModeAsk {
		t.Errorf("tools.permission_mode: got %q want %q", cfg.Tools.PermissionMode, config.PermModeAsk)
	}

	if cfg.Logger.Level != "warn" || cfg.Logger.Format != "json" {
		t.Errorf("logger: level=%q format=%q", cfg.Logger.Level, cfg.Logger.Format)
	}
	if len(cfg.Logger.Outputs) != 1 || cfg.Logger.Outputs[0] != config.LogOutputStderr {
		t.Errorf("logger.outputs: %v", cfg.Logger.Outputs)
	}
}

func TestLoadFromCLIUsesCwdConfigWhenHomeConfigMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)
	cwdDir := t.TempDir()
	t.Chdir(cwdDir)

	content := `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "openai/gpt-4o"
`
	path := filepath.Join(cwdDir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		t.Fatalf("LoadFromCLI: %v", err)
	}
	if got := filepath.Clean(cfg.Paths.ConfigPath); got != filepath.Clean(path) {
		t.Fatalf("ConfigPath %q want %q", got, path)
	}
	if cfg.Agent.Model != "openai/gpt-4o" {
		t.Fatalf("model %q", cfg.Agent.Model)
	}
}

func TestLoadFromCLIWhenConfigMissing_AppliesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)
	cfgPath := filepath.Join(home, "empty.yaml")
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvCODDYConfig, cfgPath)

	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.MaxTurns != config.AgentDefaultMaxTurns {
		t.Fatalf("agent defaults: max_turns=%d", cfg.Agent.MaxTurns)
	}
	if cfg.Logger.Level != config.LogLevelInfo {
		t.Fatalf("logger default level: %q", cfg.Logger.Level)
	}
	if len(cfg.Skills.Dirs) != 3 {
		t.Fatalf("skills default dirs: len=%d", len(cfg.Skills.Dirs))
	}
	if cfg.Sessions.Dir != "" {
		t.Fatalf("sessions.dir default: %q", cfg.Sessions.Dir)
	}
}

func TestLoadLegacyLoggerFileAddsOutputs(t *testing.T) {
	content := `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "openai/gpt-4o"

logger:
  level: "info"
  file: "/tmp/coddy-legacy.log"
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Logger.Outputs) != 2 {
		t.Fatalf("expected 2 outputs, got %v", cfg.Logger.Outputs)
	}
	if cfg.Logger.Outputs[0] != config.LogOutputStderr || cfg.Logger.Outputs[1] != config.LogOutputFile {
		t.Fatalf("unexpected outputs: %v", cfg.Logger.Outputs)
	}
	if cfg.Logger.File != "/tmp/coddy-legacy.log" {
		t.Fatalf("file: %q", cfg.Logger.File)
	}
}

func TestLoadRejectsInvalidLogger(t *testing.T) {
	content := `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "openai/gpt-4o"

logger:
  level: "not-a-real-level"
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for invalid logger.level")
	}
	if !strings.Contains(err.Error(), "logger") {
		t.Fatalf("error should mention logger: %v", err)
	}
}

func TestEnvVarExpansionInYAML(t *testing.T) {
	t.Setenv("TEST_API_KEY", "secret-key-123")

	content := `
providers:
  - name: openai
    type: openai
    api_key: "${TEST_API_KEY}"

models:
  - model: "openai/gpt-4o"

agent:
  model: "openai/gpt-4o"
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].APIKey != "secret-key-123" {
		t.Errorf("provider api_key: got %+v", cfg.Providers)
	}
	if len(cfg.Models) == 0 {
		t.Fatal("expected model definitions")
	}
	if cfg.Models[0].Model != "openai/gpt-4o" {
		t.Errorf("model: got %q", cfg.Models[0].Model)
	}
}

func TestLoadNeuralDeepProviderWithoutAPIBase(t *testing.T) {
	t.Setenv("NEURALDEEP_API_KEY", "nd-test-key")

	content := `
providers:
  - name: neuraldeep
    type: neuraldeep
    api_key: "${NEURALDEEP_API_KEY}"

models:
  - model: "neuraldeep/default"

agent:
  model: "neuraldeep/default"
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("providers: got %+v", cfg.Providers)
	}
	if cfg.Providers[0].Type != "neuraldeep" {
		t.Fatalf("provider type = %q, want neuraldeep", cfg.Providers[0].Type)
	}
	if cfg.Providers[0].APIBase != "" {
		t.Fatalf("api_base = %q, want empty for fixed NeuralDeep endpoint", cfg.Providers[0].APIBase)
	}

	rm, err := cfg.ResolveLLM("neuraldeep/default")
	if err != nil {
		t.Fatalf("ResolveLLM: %v", err)
	}
	if rm.ProviderType != "neuraldeep" || rm.APIKey != "nd-test-key" {
		t.Fatalf("resolved LLM = %+v", rm)
	}
}

func TestResolvedSessionsRoot(t *testing.T) {
	t.Run("defaultUnderHome", func(t *testing.T) {
		home := t.TempDir()
		cfg := &config.Config{Paths: config.Paths{Home: home}}
		got := cfg.ResolvedSessionsRoot()
		want := filepath.Join(home, "sessions")
		if filepath.Clean(got) != filepath.Clean(want) {
			t.Fatalf("got %q want %q", got, want)
		}
	})
	t.Run("sessionsDirOverride", func(t *testing.T) {
		tmp := filepath.Join(t.TempDir(), "alt")
		cfg := &config.Config{
			Paths:    config.Paths{Home: t.TempDir()},
			Sessions: config.Sessions{Dir: tmp},
		}
		if got := cfg.ResolvedSessionsRoot(); filepath.Clean(got) != filepath.Clean(tmp) {
			t.Fatalf("got %q", got)
		}
	})
}

func TestLoggerCLIOverrides(t *testing.T) {
	c := config.Logger{Level: "debug", Outputs: []string{config.LogOutputStdout}, Format: "json"}
	c.ApplyOverrides(config.LoggerCLIOverrides{
		Level:  "warn",
		Output: "both",
		File:   "/tmp/x.log",
		Format: "text",
	})
	if c.Level != "warn" || c.Format != "text" || c.File != "/tmp/x.log" {
		t.Fatalf("apply: %+v", c)
	}
	if len(c.Outputs) != 2 || c.Outputs[0] != config.LogOutputStdout || c.Outputs[1] != config.LogOutputFile {
		t.Fatalf("outputs: %v", c.Outputs)
	}
}

func TestLoadExplicitMissingFileReturnsError(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent explicit config path")
	}
}

func TestMemoryEnabledFalseFromYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)
	content := `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "openai/gpt-4o"

memory:
  enabled: false
`
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Memory.Enabled {
		t.Fatal("expected memory.enabled false")
	}
}

func TestRecoverFromBackupRestoresPrimary(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)
	t.Setenv(config.EnvCODDYConfig, "")

	lastGood := `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "openai/gpt-4o"
`
	if err := os.WriteFile(filepath.Join(home, "config.yaml.bak"), []byte(lastGood), 0o644); err != nil {
		t.Fatal(err)
	}
	badPrimary := "[unclosed\n"
	cfgPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(badPrimary), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadFromCLI(config.CLIPaths{})
	if err != nil {
		t.Fatalf("LoadFromCLI: %v", err)
	}
	if cfg.Agent.Model != "openai/gpt-4o" {
		t.Fatalf("model %q", cfg.Agent.Model)
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(lastGood) {
		t.Fatalf("primary not restored from last good\ngot:\n%s", string(got))
	}
}

func TestSchedulerEffectiveEnabledAndDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)
	content := `
providers:
  - name: openai
    type: openai
    api_key: "k"

models:
  - model: "openai/gpt-4o"
    max_tokens: 4096
    temperature: 0.1

agent:
  model: "openai/gpt-4o"
`
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchedulerEffectiveEnabled() {
		t.Fatal("expected scheduler off by default")
	}
	cfg.Scheduler.Enabled = true
	if !cfg.SchedulerEffectiveEnabled() {
		t.Fatal("scheduler.enabled should be observable")
	}
	if err := cfg.Scheduler.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Scheduler.MaxQueue != 10 {
		t.Fatalf("max_queue %d", cfg.Scheduler.MaxQueue)
	}
	wantDir := filepath.Join(home, "scheduler")
	if cfg.Scheduler.Dir != wantDir {
		t.Fatalf("dir %q want %q", cfg.Scheduler.Dir, wantDir)
	}
}

func TestModelEntryMultimodalParsedFromYAML(t *testing.T) {
	dir := t.TempDir()
	yaml := `
providers:
  - name: openai
    type: openai
    api_key: test-key
models:
  - model: openai/gpt-4o
    max_tokens: 1024
    temperature: 0.2
    multimodal: true
  - model: openai/gpt-4o-mini
    max_tokens: 512
    temperature: 0.5
agent:
  model: openai/gpt-4o
`
	f := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(f, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(cfg.Models))
	}
	if !cfg.Models[0].Multimodal {
		t.Errorf("models[0] (gpt-4o): want multimodal=true")
	}
	if cfg.Models[1].Multimodal {
		t.Errorf("models[1] (gpt-4o-mini): want multimodal=false (default)")
	}
}
