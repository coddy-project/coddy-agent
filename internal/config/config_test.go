package config_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	if cfg.Agent.EffectiveLLMRetryMax() != config.AgentDefaultLLMRetryMax {
		t.Errorf("agent.llm_retry_max default: got %d", cfg.Agent.EffectiveLLMRetryMax())
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
	if len(cfg.Skills.Dirs) != 0 {
		t.Fatalf("skills.dirs must stay empty without a config (the defaults are read beside it), got %q", cfg.Skills.Dirs)
	}
	if got := cfg.Skills.SearchDirs(); !reflect.DeepEqual(got, config.DefaultSkillDirs()) {
		t.Fatalf("skills search dirs without a config: got %q, want the defaults", got)
	}
	if cfg.Sessions.Dir != "" {
		t.Fatalf("sessions.dir default: %q", cfg.Sessions.Dir)
	}
}

func TestLoadFromCLIWhenConfigMissing_UsesLoopSafetyDefaults(t *testing.T) {
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
	if got := cfg.Agent.MaxTurns; got != 165 {
		t.Fatalf("agent.max_turns = %d, want 165", got)
	}
	if got := cfg.Agent.EffectiveLLMRetryMax(); got != 3 {
		t.Fatalf("agent.llm_retry_max = %d, want 3", got)
	}
	if got := cfg.Agent.EffectiveLoopToolRepeatLimit(); got != 2 {
		t.Fatalf("agent.loop_tool_repeat_limit = %d, want 2", got)
	}
	if got := cfg.Agent.EffectiveLoopNudgeMax(); got != 1 {
		t.Fatalf("agent.loop_nudge_max = %d, want 1", got)
	}

	path := filepath.Join(home, "unlimited.yaml")
	if err := os.WriteFile(path, []byte("agent:\n  max_turns: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlimited, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := unlimited.Agent.MaxTurns; got != 0 {
		t.Fatalf("explicit YAML agent.max_turns = %d, want 0", got)
	}

	fromJSON, err := config.ParseAndValidateConfigJSON([]byte(`{"agent":{"max_turns":0}}`), config.Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if got := fromJSON.Agent.MaxTurns; got != 0 {
		t.Fatalf("explicit JSON agent.max_turns = %d, want 0", got)
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
	if cfg.Logger.File != filepath.FromSlash("/tmp/coddy-legacy.log") {
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

func TestLoadNeuralDeepProviderWithMirrorAPIBase(t *testing.T) {
	t.Setenv("NEURALDEEP_API_KEY", "nd-test-key")

	content := `
providers:
  - name: neuraldeep
    type: neuraldeep
    api_base: "https://api.neuraldeep.tech/v1"
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
	// The mirror is a legitimate NeuralDeep deployment, so the row keeps it and
	// ResolveLLM carries it to the provider constructor.
	if got := cfg.Providers[0].APIBase; got != "https://api.neuraldeep.tech/v1" {
		t.Fatalf("api_base = %q, want the mirror", got)
	}
	rm, err := cfg.ResolveLLM("neuraldeep/default")
	if err != nil {
		t.Fatalf("ResolveLLM: %v", err)
	}
	if rm.BaseURL != "https://api.neuraldeep.tech/v1" {
		t.Fatalf("resolved base URL = %q, want the mirror", rm.BaseURL)
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
  enable: false
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
		t.Fatal("expected memory.enable false")
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
		t.Fatal("scheduler.enable should be observable")
	}
	if err := cfg.Scheduler.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Scheduler.MaxQueue != 10 {
		t.Fatalf("max_queue %d", cfg.Scheduler.MaxQueue)
	}
	wantDir := filepath.Join(home, "scheduler")
	if got := cfg.SchedulerUserDir(); got != wantDir {
		t.Fatalf("user jobs dir %q want %q", got, wantDir)
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

// httpAuthBaseYAML is a minimal valid config the httpserver.auth tests extend.
const httpAuthBaseYAML = `
providers:
  - name: openai
    type: openai
    api_key: k
models:
  - model: openai/gpt-4o
    max_tokens: 1024
    temperature: 0.2
agent:
  model: openai/gpt-4o
`

func TestHTTPServerAuthTokenParsedFromYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(f, []byte(httpAuthBaseYAML+"httpserver:\n  auth_token: \"s3cret\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatalf("load auth_token: %v", err)
	}
	if got := cfg.HTTPServer.EffectiveAuthTokens(); len(got) != 1 || got[0] != "s3cret" {
		t.Fatalf("auth_token not parsed: %v", got)
	}
}

func TestHTTPServerAuthTokenRedactedInJSONDTO(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(f, []byte(httpAuthBaseYAML+"httpserver:\n  auth_token: \"topsecret\"\n  public_docs: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	dto := config.ConfigToJSONDTO(cfg)
	if dto.HTTPServer.AuthToken != "" {
		t.Fatalf("GET /coddy/config must not expose auth_token, got %q", dto.HTTPServer.AuthToken)
	}
	if !dto.HTTPServer.AuthConfigured {
		t.Fatal("auth_configured should be reported in the DTO")
	}
	if !dto.HTTPServer.PublicDocs {
		t.Fatal("public_docs should be reported in the DTO")
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "topsecret") {
		t.Fatalf("serialized config leaked the token: %s", raw)
	}
}

func TestHTTPServerAuthTokenPreservedOnRedactedEdit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(f, []byte(httpAuthBaseYAML+"httpserver:\n  auth_token: \"keepme\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cur, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	// The UI GETs a redacted config (auth_configured, no token value) and saves it back
	// alongside an unrelated change; the token must survive the round-trip.
	body := `{"providers":[{"name":"openai","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"openai/gpt-4o","max_tokens":1024,"temperature":0.2}],` +
		`"agent":{"model":"openai/gpt-4o","max_turns":9},` +
		`"httpserver":{"auth_configured":true}}`
	next, err := config.ParseConfigJSONPreservingSecrets([]byte(body), cur.Paths, cur)
	if err != nil {
		t.Fatalf("preserve parse: %v", err)
	}
	if got := next.HTTPServer.EffectiveAuthTokens(); len(got) != 1 || got[0] != "keepme" {
		t.Fatalf("token not preserved across redacted edit: %v", got)
	}
	if next.Agent.MaxTurns != 9 {
		t.Fatalf("unrelated edit lost: max_turns=%d", next.Agent.MaxTurns)
	}
}

func TestHTTPServerCORSAndRemotesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	yaml := httpAuthBaseYAML +
		"httpserver:\n" +
		"  cors:\n" +
		"    enable: true\n" +
		"    allowed_origins: [\"http://localhost:5173\", \"*\"]\n" +
		"  remotes:\n" +
		"    - name: prod\n" +
		"      url: https://box.example:12345\n"
	if err := os.WriteFile(f, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HTTPServer.CORS.Enabled || len(cfg.HTTPServer.CORS.AllowedOrigins) != 2 {
		t.Fatalf("cors not parsed: %+v", cfg.HTTPServer.CORS)
	}
	// An exact match echoes the origin; any other origin falls back to the "*" entry.
	if allow, ok := cfg.HTTPServer.CORSAllowOrigin("http://localhost:5173"); !ok || allow != "http://localhost:5173" {
		t.Fatalf("exact-match CORSAllowOrigin = %q,%v", allow, ok)
	}
	if allow, ok := cfg.HTTPServer.CORSAllowOrigin("http://other.example"); !ok || allow != "*" {
		t.Fatalf("wildcard CORSAllowOrigin = %q,%v", allow, ok)
	}
	if len(cfg.HTTPServer.Remotes) != 1 || cfg.HTTPServer.Remotes[0].Name != "prod" {
		t.Fatalf("remotes not parsed: %+v", cfg.HTTPServer.Remotes)
	}

	// Round-trip through the config DTO (GET -> edit -> PUT): CORS + remotes survive.
	raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	back, err := config.ParseConfigJSONPreservingSecrets(raw, cfg.Paths, cfg)
	if err != nil {
		t.Fatalf("round-trip parse: %v", err)
	}
	if !back.HTTPServer.CORS.Enabled || len(back.HTTPServer.CORS.AllowedOrigins) != 2 {
		t.Fatalf("cors lost in round-trip: %+v", back.HTTPServer.CORS)
	}
	if len(back.HTTPServer.Remotes) != 1 || back.HTTPServer.Remotes[0].URL != "https://box.example:12345" {
		t.Fatalf("remotes lost in round-trip: %+v", back.HTTPServer.Remotes)
	}
}

// A remote may carry the token the browser presents to it (issue #401): the admin
// who writes the entry chooses to keep it in the file, usually as a ${ENV}
// reference. It travels to the page through the config document, like a
// provider's api_key, because the page is what talks to the remote.
func TestHTTPRemoteTokenLoadsFromTheEnvironmentAndRoundTrips(t *testing.T) {
	t.Setenv("CODDY_TEST_RELAY_TOKEN", "relay-client")
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	yaml := httpAuthBaseYAML +
		"httpserver:\n" +
		"  remotes:\n" +
		"    - name: office-relay\n" +
		"      url: http://relay.lan:12346\n" +
		"      token: \"  ${CODDY_TEST_RELAY_TOKEN}  \"\n" +
		"    - name: nas02\n" +
		"      url: https://nas02:12345\n"
	if err := os.WriteFile(f, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.HTTPServer.Remotes[0].Token; got != "relay-client" {
		t.Fatalf("remote token = %q, want the environment's value, trimmed", got)
	}
	if got := cfg.HTTPServer.Remotes[1].Token; got != "" {
		t.Fatalf("a remote without a token got %q", got)
	}
	raw, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"token":"relay-client"`) {
		t.Fatalf("the config document does not carry the remote's token: %s", raw)
	}
	if strings.Count(string(raw), `"token"`) != 1 {
		t.Fatalf("a remote without a token gained an empty one: %s", raw)
	}
	back, err := config.ParseConfigJSONPreservingSecrets(raw, cfg.Paths, cfg)
	if err != nil {
		t.Fatalf("round-trip parse: %v", err)
	}
	if got := back.HTTPServer.Remotes[0].Token; got != "relay-client" {
		t.Fatalf("remote token lost in round-trip: %q", got)
	}
}

func TestSkillsAutoDiscoveryDefaultsTrue(t *testing.T) {
	var s config.Skills
	s.ApplyDefaults("", func(x string) string { return x })
	if !s.AutoDiscoveryEnabled() {
		t.Fatal("skills.auto_discovery must default to true")
	}
	f := false
	s2 := config.Skills{AutoDiscovery: &f}
	if s2.AutoDiscoveryEnabled() {
		t.Fatal("explicit skills.auto_discovery=false must be respected")
	}
}

func TestAgentWaitForLimitResetJSONRoundTrip(t *testing.T) {
	// The Settings UI saves the whole config through the JSON DTO: an opt-in
	// set in YAML must survive an unrelated save, pointer semantics included.
	custom := 90_000
	c := &config.Config{Agent: config.Agent{Model: "m", WaitForLimitReset: true, WaitForLimitResetMaxMS: &custom}}
	dto := config.ConfigToJSONDTO(c)
	if !dto.Agent.WaitForLimitReset || dto.Agent.WaitForLimitResetMaxMS == nil || *dto.Agent.WaitForLimitResetMaxMS != 90_000 {
		t.Fatalf("DTO lost the wait settings: %+v", dto.Agent)
	}
	back := config.JSONDTOToConfig(dto, config.Paths{})
	if !back.Agent.WaitForLimitReset || back.Agent.WaitForLimitResetMaxMS == nil || *back.Agent.WaitForLimitResetMaxMS != 90_000 {
		t.Fatalf("round-trip lost the wait settings: %+v", back.Agent)
	}
	plain := config.JSONDTOToConfig(config.ConfigToJSONDTO(&config.Config{Agent: config.Agent{Model: "m"}}), config.Paths{})
	if plain.Agent.WaitForLimitReset || plain.Agent.WaitForLimitResetMaxMS != nil {
		t.Fatalf("an unset maximum must stay nil (the default), got %+v", plain.Agent)
	}
	if ex := config.SchemaExampleConfigJSON(); ex.Agent.WaitForLimitResetMaxMS == nil || *ex.Agent.WaitForLimitResetMaxMS != config.AgentDefaultWaitForLimitResetMaxMS {
		t.Fatalf("the schema example must carry the default maximum, got %+v", ex.Agent.WaitForLimitResetMaxMS)
	}
}

func TestHTTPRequestAllowlistJSONRoundTrip(t *testing.T) {
	// A save from the settings screen goes through the JSON DTO: an allowlist
	// set in YAML must survive it, or the next request to those hosts asks again.
	c := &config.Config{Tools: config.Tools{HTTPRequest: config.ToolHTTPRequest{Allowlist: []string{"api.github.com", "http://localhost:8080"}}}}
	back := config.JSONDTOToConfig(config.ConfigToJSONDTO(c), config.Paths{})
	if got := strings.Join(back.Tools.HTTPRequest.Allowlist, ","); got != "api.github.com,http://localhost:8080" {
		t.Fatalf("allowlist after the round-trip = %q", got)
	}
}

func TestSettingsSaveKeepsWebSearchAndSSHTimeout(t *testing.T) {
	// PUT /coddy/config rebuilds the whole file from the JSON DTO. A key the DTO
	// does not carry comes back empty, so an unrelated save from the settings
	// screen used to erase the search engines, the SearXNG address, the Brave key
	// and the SSH timeout the operator had set. This walks the same path the
	// handler does: load, DTO, JSON, parse over the live config, render, reload.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yml := `providers:
  - name: p
    type: openai
models:
  - model: p/m
agent:
  model: p/m
tools:
  ssh_connect_timeout: 77
  websearch:
    engines: [searxng, brave]
    engine_timeout_seconds: 5
    total_timeout_seconds: 12
    max_concurrent_engines: 2
    snippet_chars: 200
    cache_ttl_seconds: -1
    searxng_url: http://127.0.0.1:8888
    brave_api_key: BSA-secret
`
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{ConfigPath: path, Home: dir, CWD: dir}
	live, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(config.ConfigToJSONDTO(live))
	if err != nil {
		t.Fatal(err)
	}
	next, err := config.ParseConfigJSONPreservingSecrets(body, live.Paths, live)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := config.MarshalConfigYAMLForFile(next, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, saved, 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatalf("the saved file does not load: %v\n%s", err, saved)
	}
	if got := reloaded.Tools.SSHConnectTimeout; got != 77 {
		t.Errorf("tools.ssh_connect_timeout after a save = %d, want 77", got)
	}
	want := live.Tools.WebSearch
	if got := reloaded.Tools.WebSearch; !reflect.DeepEqual(got, want) {
		t.Errorf("tools.websearch after a save = %+v, want %+v\n%s", got, want, saved)
	}
}

func TestSettingsSaveKeepsEnvironmentReferences(t *testing.T) {
	// A key the operator keeps in the environment is written as ${VAR} in
	// config.yaml, or not written at all. The load expands the reference, so the
	// config the settings screen saves holds the secret itself; a save that wrote
	// that value back turned every reference into the key in plain text.
	t.Setenv("CODDY_TEST_PROVIDER_KEY", "sk-from-env")
	t.Setenv("CODDY_TEST_BRAVE_KEY", "BSA-from-env")
	t.Setenv(config.WebSearchBraveAPIKeyEnv, "BSA-fallback")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yml := `providers:
  - name: p
    type: openai
    api_key: "${CODDY_TEST_PROVIDER_KEY}"
  - name: q
    type: openai
    api_key: ${CODDY_TEST_PROVIDER_KEY}
models:
  - model: p/m
agent:
  model: p/m
tools:
  websearch:
    brave_api_key: ${CODDY_TEST_BRAVE_KEY}
`
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := config.Paths{ConfigPath: path, Home: dir, CWD: dir}
	save := func(edit func(*config.ConfigJSON)) string {
		t.Helper()
		live, err := config.LoadWithPaths(paths)
		if err != nil {
			t.Fatal(err)
		}
		dto := config.ConfigToJSONDTO(live)
		if edit != nil {
			edit(dto)
		}
		body, err := json.Marshal(dto)
		if err != nil {
			t.Fatal(err)
		}
		next, err := config.ParseConfigJSONPreservingSecrets(body, live.Paths, live)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := config.MarshalConfigYAMLForFile(next, path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, saved, 0o644); err != nil {
			t.Fatal(err)
		}
		return string(saved)
	}

	saved := save(nil)
	for _, secret := range []string{"sk-from-env", "BSA-from-env", "BSA-fallback"} {
		if strings.Contains(saved, secret) {
			t.Fatalf("a save wrote the secret %q into config.yaml:\n%s", secret, saved)
		}
	}
	if strings.Count(saved, "${CODDY_TEST_PROVIDER_KEY}") != 2 || !strings.Contains(saved, "${CODDY_TEST_BRAVE_KEY}") {
		t.Fatalf("a save dropped the environment references:\n%s", saved)
	}
	reloaded, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Providers[0].APIKey != "sk-from-env" || reloaded.Tools.WebSearch.BraveAPIKey != "BSA-from-env" {
		t.Fatalf("the references no longer resolve: %+v %+v", reloaded.Providers[0], reloaded.Tools.WebSearch)
	}

	// A value the operator changed on the settings screen is written as changed.
	saved = save(func(dto *config.ConfigJSON) { dto.Tools.WebSearch.BraveAPIKey = "BSA-typed-in" })
	if strings.Contains(saved, "${CODDY_TEST_BRAVE_KEY}") || !strings.Contains(saved, "BSA-typed-in") {
		t.Fatalf("an edited key was not written:\n%s", saved)
	}
}

func TestSkillsAutoDiscoveryJSONRoundTrip(t *testing.T) {
	f := false
	c := &config.Config{Skills: config.Skills{AutoDiscovery: &f}}
	dto := config.ConfigToJSONDTO(c)
	back := config.JSONDTOToConfig(dto, config.Paths{})
	if back.Skills.AutoDiscoveryEnabled() {
		t.Fatal("auto_discovery=false must survive the JSON DTO round-trip")
	}
}

func TestApplySkillsAutoDiscoveryFlag(t *testing.T) {
	newFS := func(args []string) (*flag.FlagSet, *bool) {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		v := fs.Bool(config.SkillsAutoDiscoveryFlagName, true, "")
		if err := fs.Parse(args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		return fs, v
	}

	// Flag not provided → config value untouched (stays nil = default-on).
	fs, v := newFS(nil)
	cfg := &config.Config{}
	config.ApplySkillsAutoDiscoveryFlag(fs, cfg, v)
	if cfg.Skills.AutoDiscovery != nil {
		t.Fatalf("unset flag must not touch config, got %v", *cfg.Skills.AutoDiscovery)
	}

	// -skills-auto-discovery=false → overrides to false.
	fs, v = newFS([]string{"-skills-auto-discovery=false"})
	cfg = &config.Config{}
	config.ApplySkillsAutoDiscoveryFlag(fs, cfg, v)
	if cfg.Skills.AutoDiscoveryEnabled() {
		t.Fatalf("flag=false must disable auto_discovery")
	}

	// -skills-auto-discovery=true → explicit enable.
	fs, v = newFS([]string{"-skills-auto-discovery=true"})
	cfg = &config.Config{}
	config.ApplySkillsAutoDiscoveryFlag(fs, cfg, v)
	if cfg.Skills.AutoDiscovery == nil || !cfg.Skills.AutoDiscoveryEnabled() {
		t.Fatalf("flag=true must explicitly enable auto_discovery")
	}
}

func TestMCPProjectTrustDefaultsToAskAndRejectsUnknown(t *testing.T) {
	// An empty or unrecognised value must never widen the policy.
	for _, in := range []string{"", "   ", "nonsense"} {
		var c config.MCP
		c.ProjectTrust = in
		if got := c.ResolvedProjectTrust(); got != config.ProjectTrustAsk {
			t.Errorf("ResolvedProjectTrust(%q) = %q, want %q", in, got, config.ProjectTrustAsk)
		}
	}

	var c config.MCP
	if err := c.Validate(); err != nil || c.ProjectTrust != config.ProjectTrustAsk {
		t.Fatalf("empty Validate = %v, project_trust %q", err, c.ProjectTrust)
	}
	c.ProjectTrust = "ALLOW"
	if err := c.Validate(); err != nil || c.ProjectTrust != config.ProjectTrustAllow {
		t.Fatalf("case-insensitive Validate = %v, project_trust %q", err, c.ProjectTrust)
	}
	c.ProjectTrust = "sometimes"
	if err := c.Validate(); err == nil {
		t.Fatal("unknown project_trust must be rejected")
	}
}

func TestMCPProjectTrustRoundTripsThroughConfigJSON(t *testing.T) {
	// The Settings UI PUTs the whole document back; a key missing from the
	// JSON DTO would silently reset the policy to the default.
	cfg := &config.Config{MCP: config.MCP{ProjectTrust: config.ProjectTrustDeny}}
	back := config.JSONDTOToConfig(config.ConfigToJSONDTO(cfg), config.Paths{})
	if got := back.MCP.ResolvedProjectTrust(); got != config.ProjectTrustDeny {
		t.Fatalf("project_trust after round trip = %q, want %q", got, config.ProjectTrustDeny)
	}
}

func TestApplyProjectTrustFlag(t *testing.T) {
	newFS := func(args []string) (*flag.FlagSet, *string) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		v := fs.String(config.ProjectTrustFlagName, config.ProjectTrustAsk, "")
		if err := fs.Parse(args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		return fs, v
	}

	// Unset flag must not touch config, or every launch would reset the policy.
	fs, v := newFS(nil)
	cfg := &config.Config{MCP: config.MCP{ProjectTrust: config.ProjectTrustDeny}}
	if err := config.ApplyProjectTrustFlag(fs, cfg, v); err != nil {
		t.Fatalf("unset flag: %v", err)
	}
	if cfg.MCP.ProjectTrust != config.ProjectTrustDeny {
		t.Fatalf("unset flag changed policy to %q", cfg.MCP.ProjectTrust)
	}

	fs, v = newFS([]string{"-" + config.ProjectTrustFlagName + "=allow"})
	cfg = &config.Config{MCP: config.MCP{ProjectTrust: config.ProjectTrustDeny}}
	if err := config.ApplyProjectTrustFlag(fs, cfg, v); err != nil {
		t.Fatalf("allow: %v", err)
	}
	if cfg.MCP.ProjectTrust != config.ProjectTrustAllow {
		t.Fatalf("flag=allow left policy %q", cfg.MCP.ProjectTrust)
	}

	// A typo must fail loudly instead of silently falling back to ask.
	fs, v = newFS([]string{"-" + config.ProjectTrustFlagName + "=allo"})
	cfg = &config.Config{}
	if err := config.ApplyProjectTrustFlag(fs, cfg, v); err == nil {
		t.Fatal("unknown flag value must be rejected")
	}
}

// TestModelStreamToggleYAMLAndDTO covers the tri-state of models[].stream: an
// omitted key means streaming, an explicit false must survive both the YAML load
// and the Settings JSON round trip without becoming "unset" or vice versa.
func TestModelStreamToggleYAMLAndDTO(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)

	content := `
providers:
  - name: local
    type: openai
    api_key: "test-key"

models:
  - model: "local/streamed"
  - model: "local/blocking"
    stream: false
  - model: "local/explicit-true"
    stream: true

agent:
  model: "local/streamed"
`
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Config: path})
	if err != nil {
		t.Fatalf("LoadFromCLI: %v", err)
	}

	for _, tc := range []struct {
		ref        string
		wantStream bool
		wantSet    bool
	}{
		{"local/streamed", true, false},
		{"local/blocking", false, true},
		{"local/explicit-true", true, true},
	} {
		entry := cfg.FindModelEntry(tc.ref)
		if entry == nil {
			t.Fatalf("model %q missing from config", tc.ref)
		}
		if got := entry.EffectiveStream(); got != tc.wantStream {
			t.Fatalf("%s: EffectiveStream() = %v, want %v", tc.ref, got, tc.wantStream)
		}
		if (entry.Stream != nil) != tc.wantSet {
			t.Fatalf("%s: key set = %v, want %v", tc.ref, entry.Stream != nil, tc.wantSet)
		}
		rm, err := cfg.ResolveLLM(tc.ref)
		if err != nil {
			t.Fatalf("%s: ResolveLLM: %v", tc.ref, err)
		}
		if rm.Stream != tc.wantStream {
			t.Fatalf("%s: ResolvedLLM.Stream = %v, want %v", tc.ref, rm.Stream, tc.wantStream)
		}
	}

	// Opening and saving Settings must not turn an omitted key into an explicit false.
	dto := config.ConfigToJSONDTO(cfg)
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal DTO: %v", err)
	}
	if strings.Contains(string(raw), `"model":"local/streamed","stream"`) {
		t.Fatalf("the omitted key was materialized in the DTO: %s", raw)
	}
	back, err := config.ParseAndValidateConfigJSON(raw, cfg.Paths)
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}
	if e := back.FindModelEntry("local/streamed"); e == nil || e.Stream != nil {
		t.Fatalf("round trip materialized stream on an omitted key: %+v", e)
	}
	if e := back.FindModelEntry("local/blocking"); e == nil || e.Stream == nil || *e.Stream {
		t.Fatalf("round trip lost an explicit stream: false: %+v", e)
	}
}

// TestCodexRejectsStreamFalse pins the one unsupported combination: the Codex
// Responses backend is streaming-only, so it cannot honor one blocking request.
func TestCodexRejectsStreamFalse(t *testing.T) {
	blocking := false
	streaming := true

	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
		Models:    []config.ModelEntry{{Model: "codex/gpt-5.5", Stream: &blocking}},
		Agent:     config.Agent{Model: "codex/gpt-5.5"},
	}
	err := cfg.ValidateModelsProvidersAndAgent()
	if err == nil {
		t.Fatal("codex with stream: false must be rejected")
	}
	if !strings.Contains(err.Error(), "streaming-only") {
		t.Fatalf("error %q does not explain why", err)
	}

	// An omitted key and an explicit true stay valid for codex.
	for name, entry := range map[string]config.ModelEntry{
		"omitted":  {Model: "codex/gpt-5.5"},
		"explicit": {Model: "codex/gpt-5.5", Stream: &streaming},
	} {
		cfg.Models = []config.ModelEntry{entry}
		if err := cfg.ValidateModelsProvidersAndAgent(); err != nil {
			t.Fatalf("%s stream key rejected for codex: %v", name, err)
		}
	}
}

// TestUISchemaModelStreamDefault pins the one boolean in the settings schema whose
// absence means true. The form seeds a new entry from schema defaults and draws an
// unset switch from them, so a missing default here would make the UI write and
// display the opposite of how the agent behaves.
func TestUISchemaModelStreamDefault(t *testing.T) {
	schema := config.UISchemaMap()
	models, ok := schema["properties"].(map[string]interface{})["models"].(map[string]interface{})
	if !ok {
		t.Fatal("models section missing from the UI schema")
	}
	items := models["items"].(map[string]interface{})
	props := items["properties"].(map[string]interface{})

	stream, ok := props["stream"].(map[string]interface{})
	if !ok {
		t.Fatal("models[].stream missing from the UI schema")
	}
	if def, ok := stream["default"].(bool); !ok || !def {
		t.Fatalf("models[].stream default = %v, want true", stream["default"])
	}
	// Field order drives the rendered form; a field absent from it is not shown.
	order, _ := items["x-coddy-property-order"].([]interface{})
	found := false
	for _, name := range order {
		if s, _ := name.(string); s == "stream" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stream missing from the models field order: %v", order)
	}
}

// TestAgentLLMRetryAndTimeoutKnobs pins the unset/explicit-zero distinction
// for llm_retry_max and llm_first_token_timeout_ms, and the providers[]
// timeout_ms plumbing into ResolvedLLM.
func TestAgentLLMRetryAndTimeoutKnobs(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)

	content := `
providers:
  - name: local
    type: openai
    api_key: "test-key"
    timeout_ms: 120000

models:
  - model: "local/gpt-4o"

agent:
  model: "local/gpt-4o"
  llm_retry_max: 0
  llm_first_token_timeout_ms: 0
  llm_stream_idle_timeout_ms: 0
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

	if got := cfg.Agent.EffectiveLLMRetryMax(); got != 0 {
		t.Errorf("explicit llm_retry_max: 0 resolved to %d, want 0 (retries disabled)", got)
	}
	if got := cfg.Agent.EffectiveLLMFirstTokenTimeout(); got != 0 {
		t.Errorf("explicit llm_first_token_timeout_ms: 0 resolved to %v, want 0 (guard disabled)", got)
	}
	if got := cfg.Agent.EffectiveLLMStreamIdleTimeout(); got != 0 {
		t.Errorf("explicit llm_stream_idle_timeout_ms: 0 resolved to %v, want 0 (guard disabled)", got)
	}

	rm, err := cfg.ResolveLLM("local/gpt-4o")
	if err != nil {
		t.Fatalf("ResolveLLM: %v", err)
	}
	if rm.TimeoutMS != 120000 {
		t.Errorf("resolved provider timeout_ms = %d, want 120000", rm.TimeoutMS)
	}

	unset := config.Agent{}
	if got := unset.EffectiveLLMRetryMax(); got != config.AgentDefaultLLMRetryMax {
		t.Errorf("unset llm_retry_max resolved to %d, want default %d", got, config.AgentDefaultLLMRetryMax)
	}
	if got := unset.EffectiveLLMFirstTokenTimeout(); got != config.AgentDefaultLLMFirstTokenTimeoutMS*time.Millisecond {
		t.Errorf("unset llm_first_token_timeout_ms resolved to %v, want 90s", got)
	}
	if got := unset.EffectiveLLMStreamIdleTimeout(); got != config.AgentDefaultLLMStreamIdleTimeoutMS*time.Millisecond {
		t.Errorf("unset llm_stream_idle_timeout_ms resolved to %v, want 5m", got)
	}
}

// TestAgentLLMKnobsValidation rejects negative values for the new knobs.
func TestAgentLLMKnobsValidation(t *testing.T) {
	neg := -1
	a := config.Agent{LLMRetryMax: &neg}
	if err := a.Validate(); err == nil {
		t.Error("negative llm_retry_max must fail validation")
	}
	a = config.Agent{LLMFirstTokenTimeoutMS: &neg}
	if err := a.Validate(); err == nil {
		t.Error("negative llm_first_token_timeout_ms must fail validation")
	}
	a = config.Agent{LLMStreamIdleTimeoutMS: &neg}
	if err := a.Validate(); err == nil {
		t.Error("negative llm_stream_idle_timeout_ms must fail validation")
	}
	p := config.ProviderConfig{Name: "x", Type: "openai", TimeoutMS: -5}
	if err := p.Validate(); err == nil {
		t.Error("negative providers timeout_ms must fail validation")
	}
}

// TestProviderAuthPathByType pins where each provider type keeps its managed
// credential. Every call site that builds an llm.ProviderInput must resolve the
// auth file through this one helper: a second hand-rolled path (as the model
// listing HTTP handler once had) silently splits codex and neuraldeep logins
// into different files.
func TestProviderAuthPathByType(t *testing.T) {
	home := "/tmp/coddy-home"
	cases := []struct {
		typ, want string
	}{
		{"codex", filepath.Join(home, "providers", "nd", "codex-auth.json")},
		{"neuraldeep", filepath.Join(home, "providers", "nd", "neuraldeep-auth.json")},
		{"openai", ""},
		{"anthropic", ""},
	}
	for _, c := range cases {
		if got := config.ProviderAuthPath(home, "nd", c.typ); got != c.want {
			t.Fatalf("ProviderAuthPath(%q) = %q, want %q", c.typ, got, c.want)
		}
	}
	if got := config.NeuralDeepAuthPath(home, "bad name!"); got != "" {
		t.Fatalf("invalid provider name must yield empty path, got %q", got)
	}
	if got := config.NeuralDeepAuthPath("", "nd"); got != "" {
		t.Fatalf("empty home must yield empty path, got %q", got)
	}
}

func TestAgentWaitForLimitResetDefaults(t *testing.T) {
	var a config.Agent
	if a.WaitForLimitReset {
		t.Fatal("wait_for_limit_reset must be off by default")
	}
	if got := a.EffectiveWaitForLimitResetMax(); got != config.AgentDefaultWaitForLimitResetMaxMS*time.Millisecond {
		t.Fatalf("default maximum wait = %v, want %v", got, config.AgentDefaultWaitForLimitResetMaxMS*time.Millisecond)
	}
	if config.AgentDefaultWaitForLimitResetMaxMS != 4*60*60*1000 {
		t.Fatalf("default maximum wait is %d ms, want four hours", config.AgentDefaultWaitForLimitResetMaxMS)
	}
	custom := 90_000
	a.WaitForLimitResetMaxMS = &custom
	if got := a.EffectiveWaitForLimitResetMax(); got != 90*time.Second {
		t.Fatalf("custom maximum wait = %v, want 90s", got)
	}
	zero := 0
	a.WaitForLimitResetMaxMS = &zero
	if got := a.EffectiveWaitForLimitResetMax(); got != 0 {
		t.Fatalf("an explicit 0 must mean no wait at all, got %v", got)
	}
}

// Regression for coddy-project/coddy-agent#146: ${CWD} is a session placeholder.
// A config file that spells it out (skills.dirs, subagents.dirs, hooks.files,
// prompts.dir) must keep it verbatim through load so every session
// resolves it against its own workspace, while the process-scoped directories
// (sessions, scheduler, memory, log file) still resolve it against the default
// working directory at load time. An environment variable that happens to be
// named CWD must not be mistaken for the placeholder either.
func TestLoadFromYAML_SessionCWDPlaceholderSurvivesLoad(t *testing.T) {
	t.Setenv("CWD", filepath.Join("decoy", "env"))
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	launch := filepath.Join(dir, "launch")
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
    - "${CWD}/.agents/skills"
    - "${CODDY_HOME}/skills"

subagents:
  dirs:
    - "${CWD}/.coddy/agents"

hooks:
  files:
    - "${CWD}/.coddy/hooks.json"

prompts:
  dir: "${CWD}/prompts"

sessions:
  dir: "${CWD}/sessions"

scheduler:
  dir: "${CWD}/.scheduler"

memory:
  dir: "${CWD}/memory"

logger:
  file: "${CWD}/coddy.log"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: home, CWD: launch, ConfigPath: path})
	if err != nil {
		t.Fatalf("LoadWithPaths: %v", err)
	}

	perSession := []struct {
		name string
		got  string
		want string
	}{
		{"skills.dirs[0]", cfg.Skills.Dirs[0], "${CWD}/.agents/skills"},
		{"skills.dirs[1]", cfg.Skills.Dirs[1], filepath.Join(home, "skills")},
		{"subagents.dirs[0]", cfg.Subagents.Dirs[0], "${CWD}/.coddy/agents"},
		{"hooks.files[0]", cfg.Hooks.Files[0], "${CWD}/.coddy/hooks.json"},
		{"prompts.dir", cfg.Prompts.Dir, "${CWD}/prompts"},
	}
	for _, tc := range perSession {
		// ${CODDY_HOME} is substituted with forward slashes; compare slash-normalised.
		if filepath.ToSlash(tc.got) != filepath.ToSlash(tc.want) {
			t.Errorf("%s: got %q want %q", tc.name, tc.got, tc.want)
		}
	}

	// The consumers resolve the placeholder against the session that asks.
	sessionCWD := filepath.Join(dir, "project")
	if got, want := cfg.Prompts.ResolvedDir(sessionCWD), filepath.Join(sessionCWD, "prompts"); got != want {
		t.Errorf("prompts.ResolvedDir(session): got %q want %q", got, want)
	}

	processScoped := []struct {
		name string
		got  string
		want string
	}{
		{"sessions.dir", cfg.Sessions.Dir, filepath.Join(launch, "sessions")},
		{"memory.dir", cfg.Memory.Dir, filepath.Join(launch, "memory")},
		{"logger.file", cfg.Logger.File, filepath.Join(launch, "coddy.log")},
	}
	for _, tc := range processScoped {
		if filepath.Clean(tc.got) != filepath.Clean(tc.want) {
			t.Errorf("%s: got %q want %q", tc.name, tc.got, tc.want)
		}
	}
}

// The Settings UI reads the configuration as JSON and writes it back as YAML.
// A skills.dirs entry with ${CWD} must survive that round trip verbatim on
// both legs: GET reports the placeholder, PUT stores it, and the next load
// still leaves it to the session (coddy-project/coddy-agent#146).
func TestConfigJSONRoundTripKeepsSessionCWDPlaceholder(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	launch := filepath.Join(dir, "launch")
	path := filepath.Join(dir, "config.yaml")
	paths := config.Paths{Home: home, CWD: launch, ConfigPath: path}
	body := `{"providers":[{"name":"local","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"local/gpt-4o","max_tokens":1024,"temperature":0.2}],` +
		`"agent":{"model":"local/gpt-4o"},` +
		`"skills":{"dirs":["${CWD}/.agents/skills","${CODDY_HOME}/skills"]}}`
	next, err := config.ParseConfigJSONPreservingSecrets([]byte(body), paths, nil)
	if err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if got := next.Skills.Dirs[0]; got != "${CWD}/.agents/skills" {
		t.Fatalf("PUT lost the placeholder before writing: %q", got)
	}
	if got := config.ConfigToJSONDTO(next).Skills.Dirs[0]; got != "${CWD}/.agents/skills" {
		t.Fatalf("GET must report the placeholder verbatim, got %q", got)
	}
	yb, err := config.MarshalConfigYAML(next)
	if err != nil {
		t.Fatalf("marshal yaml: %v", err)
	}
	if err := os.WriteFile(path, yb, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.LoadWithPaths(paths)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Skills.Dirs[0]; got != "${CWD}/.agents/skills" {
		t.Fatalf("reload after PUT baked the placeholder: %q", got)
	}
	if got, want := filepath.ToSlash(reloaded.Skills.Dirs[1]), filepath.ToSlash(filepath.Join(home, "skills")); got != want {
		t.Fatalf("reload after PUT: skills.dirs[1] got %q want %q", got, want)
	}
}

// TestProviderUsageLimitsPanelYAMLDTOAndSchema pins providers[].usage_limits_panel:
// an omitted key reads as on, an explicit false parses, the Settings DTO keeps
// the operator's choice without materialising the omitted key, and the UI
// schema draws the switch on by default.
func TestProviderUsageLimitsPanelYAMLDTOAndSchema(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)

	content := `
providers:
  - name: nd
    type: neuraldeep
    api_key: "sk-test"
  - name: nd-quiet
    type: neuraldeep
    api_key: "sk-test"
    usage_limits_panel: false
  - name: nd-loud
    type: neuraldeep
    api_key: "sk-test"
    usage_limits_panel: true

models:
  - model: "nd/qwen"

agent:
  model: "nd/qwen"
`
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Config: path})
	if err != nil {
		t.Fatalf("LoadFromCLI: %v", err)
	}
	for _, tc := range []struct {
		name    string
		wantOn  bool
		wantSet bool
	}{
		{"nd", true, false},
		{"nd-quiet", false, true},
		{"nd-loud", true, true},
	} {
		p := cfg.FindProvider(tc.name)
		if p == nil {
			t.Fatalf("provider %q missing from config", tc.name)
		}
		if got := p.EffectiveUsageLimitsPanel(); got != tc.wantOn {
			t.Fatalf("%s: EffectiveUsageLimitsPanel() = %v, want %v", tc.name, got, tc.wantOn)
		}
		if (p.UsageLimitsPanel != nil) != tc.wantSet {
			t.Fatalf("%s: key set = %v, want %v", tc.name, p.UsageLimitsPanel != nil, tc.wantSet)
		}
	}
	var none *config.ProviderConfig
	if !none.EffectiveUsageLimitsPanel() {
		t.Fatal("a nil provider must read as on")
	}

	// Opening and saving Settings must not turn an omitted key into an
	// explicit value, and must keep an explicit false.
	dto := config.ConfigToJSONDTO(cfg)
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal DTO: %v", err)
	}
	if got := strings.Count(string(raw), `"usage_limits_panel"`); got != 2 {
		t.Fatalf("the DTO carries the key %d times, want 2 (the two explicit rows): %s", got, raw)
	}
	if dto.Providers[1].UsageLimitsPanel == cfg.Providers[1].UsageLimitsPanel {
		t.Fatal("the DTO must own a copy of the pointer field, not alias the live config")
	}
	back, err := config.ParseAndValidateConfigJSON(raw, cfg.Paths)
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}
	if p := back.FindProvider("nd"); p == nil || p.UsageLimitsPanel != nil {
		t.Fatalf("round trip materialised the omitted key: %+v", p)
	}
	if p := back.FindProvider("nd-quiet"); p == nil || p.UsageLimitsPanel == nil || *p.UsageLimitsPanel || p.EffectiveUsageLimitsPanel() {
		t.Fatalf("round trip lost usage_limits_panel: false: %+v", p)
	}
	if p := back.FindProvider("nd-loud"); p == nil || p.UsageLimitsPanel == nil || !*p.UsageLimitsPanel {
		t.Fatalf("round trip lost an explicit usage_limits_panel: true: %+v", p)
	}

	// The Settings form seeds new rows from the schema default and renders
	// an unset switch from it, so the schema has to say the default is on.
	schema := config.UISchemaMap()
	providers, ok := schema["properties"].(map[string]interface{})["providers"].(map[string]interface{})
	if !ok {
		t.Fatal("providers section missing from the UI schema")
	}
	items := providers["items"].(map[string]interface{})
	props := items["properties"].(map[string]interface{})
	field, ok := props["usage_limits_panel"].(map[string]interface{})
	if !ok {
		t.Fatal("providers[].usage_limits_panel missing from the UI schema")
	}
	if field["type"] != "boolean" {
		t.Fatalf("providers[].usage_limits_panel type = %v, want boolean", field["type"])
	}
	if def, ok := field["default"].(bool); !ok || !def {
		t.Fatalf("providers[].usage_limits_panel default = %v, want true", field["default"])
	}
	order, _ := items["x-coddy-property-order"].([]interface{})
	found := false
	for _, name := range order {
		if s, _ := name.(string); s == "usage_limits_panel" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("usage_limits_panel missing from the providers field order: %v", order)
	}
}

// TestInstructionsDefaultMatchesTheSchema is the guard against the drift this
// test was written for: the loader, the UI defaults and the embedded schema all
// have to name the same default, or a config.yaml validates against a default
// the binary does not apply. Since issue #425 that default is nothing: the
// AGENTS.md and DESIGN.md documents are read without being listed, and the
// list only adds.
func TestInstructionsDefaultMatchesTheSchema(t *testing.T) {
	assertFiles := func(what string, got []string) {
		t.Helper()
		if got == nil || len(got) != 0 {
			t.Fatalf("%s = %#v, want an empty list", what, got)
		}
	}

	// An absent key and an explicit empty list are the same thing, as they are
	// for skills.dirs and hooks.files.
	for _, tc := range []struct {
		name  string
		start config.Instructions
	}{
		{"key absent", config.Instructions{}},
		{"files: []", config.Instructions{Files: []string{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.start
			c.ApplyDefaults()
			assertFiles("instructions.files", c.Files)
		})
	}

	assertFiles("the loader defaults", config.DocDefaults(config.Paths{Home: filepath.FromSlash("/agent/home")}).Instructions.Files)
	assertFiles("the UI example config", config.SchemaExampleConfigJSON().Instructions.Files)

	var schema struct {
		Properties struct {
			Instructions struct {
				Properties struct {
					Files struct {
						Default []string `json:"default"`
					} `json:"files"`
				} `json:"properties"`
			} `json:"instructions"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(config.ConfigSchemaJSON(), &schema); err != nil {
		t.Fatal(err)
	}
	assertFiles("the embedded schema default", schema.Properties.Instructions.Properties.Files.Default)
}

// TestAgentModelOptional covers coddy-project/coddy-agent#336: agent.model is
// optional, so a Settings save that lists models but leaves the field empty
// parses and keeps it empty - interactive surfaces pick a model per session,
// and the unattended paths report a missing model when they resolve one.
func TestAgentModelOptional(t *testing.T) {
	paths := config.Paths{Home: t.TempDir(), CWD: t.TempDir()}
	body := `{"providers":[{"name":"openai","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"openai/gpt-4o"},{"model":"openai/gpt-5"}]}`

	next, err := config.ParseConfigJSONPreservingSecrets([]byte(body), paths, nil)
	if err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if next.Agent.Model != "" {
		t.Fatalf("agent.model materialized: %q", next.Agent.Model)
	}
	// An explicit value is kept verbatim.
	body = `{"providers":[{"name":"openai","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"openai/gpt-4o"},{"model":"openai/gpt-5"}],` +
		`"agent":{"model":"openai/gpt-5"}}`
	next, err = config.ParseConfigJSONPreservingSecrets([]byte(body), paths, nil)
	if err != nil {
		t.Fatalf("parse json with explicit model: %v", err)
	}
	if got, want := next.Agent.Model, "openai/gpt-5"; got != want {
		t.Fatalf("explicit agent.model overwritten: got %q want %q", got, want)
	}
	// And a name that matches no configured model is still refused.
	body = `{"providers":[{"name":"openai","type":"openai","api_key":"k"}],` +
		`"models":[{"model":"openai/gpt-4o"}],` +
		`"agent":{"model":"openai/typo"}}`
	if _, err := config.ParseConfigJSONPreservingSecrets([]byte(body), paths, nil); err == nil ||
		!strings.Contains(err.Error(), "not found in models list") {
		t.Fatalf("unknown agent.model: err = %v", err)
	}
}

// The file path accepts the same optional shape: a hand-edited config.yaml
// with models but no agent.model loads, and a bad name is still refused.
func TestLoadAgentModelOptional(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yml := "providers:\n  - name: openai\n    type: openai\n    api_key: k\n" +
		"models:\n  - model: openai/gpt-4o\n"
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load without agent.model: %v", err)
	}
	if cfg.Agent.Model != "" {
		t.Fatalf("agent.model materialized: %q", cfg.Agent.Model)
	}
}

// Every workspace reads four folders, lowest priority first: the user's agents
// skills, the project's agents skills, Coddy's own, the project's Coddy
// skills. skills.dirs only adds directories after them, which win a name over
// the defaults; an absent key adds none.
func TestSkillsDirsAddToTheFourDefaults(t *testing.T) {
	want := []string{
		"${HOME}/.agents/skills",
		"${CWD}/.agents/skills",
		"${CODDY_HOME}/skills",
		"${CWD}/.coddy/skills",
	}
	if got := config.DefaultSkillDirs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultSkillDirs() = %q, want %q", got, want)
	}
	var unset config.Skills
	unset.ApplyDefaults("/home/dev/.coddy", func(s string) string { return s })
	if len(unset.Dirs) != 0 {
		t.Fatalf("an absent skills.dirs must stay empty, got %q", unset.Dirs)
	}
	if got := unset.SearchDirs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("absent skills.dirs reads %q, want the defaults %q", got, want)
	}
	set := config.Skills{Dirs: []string{"/srv/team-skills"}}
	set.ApplyDefaults("/home/dev/.coddy", func(s string) string { return s })
	if got := set.SearchDirs(); !reflect.DeepEqual(got, append(append([]string(nil), want...), "/srv/team-skills")) {
		t.Fatalf("a set skills.dirs must come after the defaults, got %q", got)
	}
}

// Skill marketplaces are declared in two files of one shape: the operator's
// <home>/marketplaces.json and the project's .coddy/marketplaces.json. A
// missing file declares nothing; one that does not parse is an error, so
// nothing writes over what it holds.
func TestMarketplacesFileReadAndWrite(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	if got := config.GlobalMarketplacesPath(home); got != filepath.Join(home, "marketplaces.json") {
		t.Fatalf("GlobalMarketplacesPath = %q", got)
	}
	if got := config.ProjectMarketplacesPath(cwd); got != filepath.Join(cwd, ".coddy", "marketplaces.json") {
		t.Fatalf("ProjectMarketplacesPath = %q", got)
	}
	empty, err := config.ReadMarketplacesFile(config.ProjectMarketplacesPath(cwd))
	if err != nil || len(empty.Sources) != 0 || len(empty.Marketplaces) != 0 {
		t.Fatalf("a missing file = %+v, %v", empty, err)
	}
	want := config.MarketplacesFile{
		Sources:      []string{"owner/whole"},
		Marketplaces: []config.DeclaredMarketplace{{Name: "catalog", Source: "owner/catalog"}},
	}
	path := config.ProjectMarketplacesPath(cwd)
	if err := config.WriteMarketplacesFile(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := config.ReadMarketplacesFile(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("read back %+v, %v; want %+v", got, err, want)
	}
	if err := os.WriteFile(path, []byte(`{"sources": [`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.ReadMarketplacesFile(path); err == nil {
		t.Fatal("a file that does not parse must be an error")
	}
}

// skills.sources left config.yaml: a config that still has it hands the list
// to <home>/marketplaces.json on load (a source the file has already, in any
// case, and the system source are not repeated), the key and its comment
// leave the file, the rest of the section stays, and one backup of the old
// file covers this move and the mcp_servers one.
func TestLegacySkillsSourcesMoveIntoHomeMarketplacesJSON(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := config.WriteMarketplacesFile(config.GlobalMarketplacesPath(home), config.MarketplacesFile{Sources: []string{"Owner/Kept"}}); err != nil {
		t.Fatal(err)
	}
	body := "agent:\n  model: local/m\nskills:\n  dirs:\n    - /opt/team-skills\n  # Remote marketplaces\n  sources:\n    - owner/kept\n    - owner/new\n    - " + config.SystemSkillsSource + "\n  auto_discovery: false\nmcp_servers: []\nrules:\n  enable: true\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: home, CWD: dir, ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.AutoDiscoveryEnabled() || len(cfg.Skills.Dirs) != 1 {
		t.Fatalf("the rest of the skills section was lost: %+v", cfg.Skills)
	}
	got, err := config.ReadMarketplacesFile(config.GlobalMarketplacesPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Sources, []string{"Owner/Kept", "owner/new"}) {
		t.Fatalf("home marketplaces.json sources = %v", got.Sources)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "agent:\n  model: local/m\nskills:\n  dirs:\n    - /opt/team-skills\n  auto_discovery: false\nrules:\n  enable: true\n"; string(after) != want {
		t.Fatalf("config.yaml after the moves:\n%s\nwant:\n%s", after, want)
	}
	if backups, _ := filepath.Glob(path + ".bak-*"); len(backups) != 1 {
		t.Fatalf("want one backup for both moves, got %v", backups)
	}
}

// scheduler.dir left config.yaml: the user jobs folder is fixed at
// ${CODDY_HOME}/scheduler. A config that still names another folder has its
// jobs and their .state sidecars copied there on load - a job the target has
// with the same bytes is skipped, one with other bytes lands as
// <id>-migrated.md - the old folder left as it was, and the key cut out.
func TestLegacySchedulerDirCopiesJobsIntoTheHome(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	old := filepath.Join(dir, "jobs")
	userDir := filepath.Join(home, "scheduler")
	for _, d := range []string{old, userDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(old, "nightly.md"), "---\nschedule: \"0 3 * * *\"\n---\nold nightly\n")
	write(filepath.Join(old, "nightly.state"), "{\"session_id\":\"sess_x\"}\n")
	write(filepath.Join(old, "taken.md"), "---\nschedule: \"0 4 * * *\"\n---\nold taken\n")
	write(filepath.Join(old, "taken.state"), "{\"session_id\":\"sess_old\"}\n")
	write(filepath.Join(userDir, "taken.md"), "---\nschedule: \"0 5 * * *\"\n---\nhome taken\n")
	write(filepath.Join(old, "same.md"), "---\nschedule: \"0 6 * * *\"\n---\nsame\n")
	write(filepath.Join(userDir, "same.md"), "---\nschedule: \"0 6 * * *\"\n---\nsame\n")
	write(filepath.Join(old, "same.state"), "{\"session_id\":\"sess_same\"}\n")
	write(filepath.Join(old, "twice.md"), "---\nschedule: \"0 7 * * *\"\n---\nold twice\n")
	write(filepath.Join(userDir, "twice.md"), "---\nschedule: \"0 7 * * *\"\n---\nhome twice\n")
	write(filepath.Join(userDir, "twice-migrated.md"), "---\nschedule: \"0 7 * * *\"\n---\nan earlier copy\n")

	body := "agent:\n  model: local/m\nscheduler:\n  enable: true\n  dir: " + old + "\n  max_queue: 3\n"
	path := filepath.Join(dir, "config.yaml")
	write(path, body)
	cfg, err := config.LoadWithPaths(config.Paths{Home: home, CWD: dir, ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scheduler.MaxQueue != 3 || !cfg.Scheduler.Enabled {
		t.Fatalf("the rest of the scheduler section was lost: %+v", cfg.Scheduler)
	}
	if got, _ := os.ReadFile(filepath.Join(userDir, "nightly.md")); !strings.Contains(string(got), "old nightly") {
		t.Fatalf("nightly.md not copied: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(userDir, "nightly.state")); !strings.Contains(string(got), "sess_x") {
		t.Fatalf("nightly.state not copied: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(userDir, "taken.md")); !strings.Contains(string(got), "home taken") {
		t.Fatalf("a job the home already had was overwritten: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(userDir, "taken-migrated.md")); !strings.Contains(string(got), "old taken") {
		t.Fatalf("a clashing job was not copied aside: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(userDir, "taken-migrated.state")); !strings.Contains(string(got), "sess_old") {
		t.Fatalf("the clashing job's state did not follow it: %q", got)
	}
	if _, err := os.Stat(filepath.Join(userDir, "same-migrated.md")); err == nil {
		t.Fatal("a job with the same bytes was copied aside")
	}
	if got, _ := os.ReadFile(filepath.Join(userDir, "same.state")); !strings.Contains(string(got), "sess_same") {
		t.Fatalf("the checkpoint of a job the home already had was not carried: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(userDir, "twice-migrated-2.md")); !strings.Contains(string(got), "old twice") {
		t.Fatalf("a job whose -migrated name was taken was dropped: %q", got)
	}
	if _, err := os.Stat(filepath.Join(old, "nightly.md")); err != nil {
		t.Fatalf("the old folder was touched: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "agent:\n  model: local/m\nscheduler:\n  enable: true\n  max_queue: 3\n"; string(after) != want {
		t.Fatalf("config.yaml after the move:\n%s\nwant:\n%s", after, want)
	}
}

// A scheduler.dir that already names ${CODDY_HOME}/scheduler only leaves the
// file.
func TestLegacySchedulerDirAtTheDefaultIsCutOnly(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	body := "scheduler:\n  dir: ${CODDY_HOME}/scheduler\n  enable: false\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadWithPaths(config.Paths{Home: home, CWD: dir, ConfigPath: path}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "scheduler:\n  enable: false\n"; string(after) != want {
		t.Fatalf("config.yaml after the move:\n%s\nwant:\n%s", after, want)
	}
}
