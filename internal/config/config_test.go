package config_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
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
		"    enabled: true\n" +
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
