package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	// Load with no config file - should return defaults.
	cfg, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
	_ = cfg
}

func TestLoadFromFile(t *testing.T) {
	content := `
models:
  default: "openai/gpt-4o"
  definitions:
    - id: "openai/gpt-4o"
      provider: "openai"
      model: "gpt-4o"
      api_key: "test-key"
      max_tokens: 4096
      temperature: 0.1

react:
  max_turns: 20

tools:
  require_permission_for_commands: true
  restrict_to_cwd: true

log:
  level: "debug"
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

	if cfg.Models.Default != "openai/gpt-4o" {
		t.Errorf("expected default model %q, got %q", "openai/gpt-4o", cfg.Models.Default)
	}
	if cfg.React.MaxTurns != 20 {
		t.Errorf("expected max_turns 20, got %d", cfg.React.MaxTurns)
	}
	if !cfg.Tools.RequirePermissionForCommands {
		t.Error("expected require_permission_for_commands to be true")
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("expected log level %q, got %q", "debug", cfg.Log.Level)
	}
}

func TestFindModelDef(t *testing.T) {
	cfg := &config.Config{
		Models: config.ModelsConfig{
			Default: "openai/gpt-4o",
			Defs: []config.ModelDefinition{
				{ID: "openai/gpt-4o", Provider: "openai", Model: "gpt-4o"},
				{ID: "local/qwen", Provider: "ollama", Model: "qwen2.5-coder"},
			},
		},
	}

	def, err := cfg.FindModelDef("openai/gpt-4o")
	if err != nil {
		t.Fatalf("FindModelDef: %v", err)
	}
	if def.Provider != "openai" {
		t.Errorf("expected provider %q, got %q", "openai", def.Provider)
	}

	def, err = cfg.FindModelDef("local/qwen")
	if err != nil {
		t.Fatalf("FindModelDef: %v", err)
	}
	if def.Model != "qwen2.5-coder" {
		t.Errorf("expected model %q, got %q", "qwen2.5-coder", def.Model)
	}

	_, err = cfg.FindModelDef("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent model")
	}
}

func TestModelForMode(t *testing.T) {
	cfg := &config.Config{
		Models: config.ModelsConfig{
			Default:   "openai/gpt-4o",
			AgentMode: "openai/gpt-4o",
			PlanMode:  "anthropic/claude-3-5",
		},
	}

	if got := cfg.ModelForMode("agent"); got != "openai/gpt-4o" {
		t.Errorf("agent mode: expected %q, got %q", "openai/gpt-4o", got)
	}
	if got := cfg.ModelForMode("plan"); got != "anthropic/claude-3-5" {
		t.Errorf("plan mode: expected %q, got %q", "anthropic/claude-3-5", got)
	}
	if got := cfg.ModelForMode("unknown"); got != "openai/gpt-4o" {
		t.Errorf("unknown mode: expected default %q, got %q", "openai/gpt-4o", got)
	}
}

func TestExpandCWD(t *testing.T) {
	result := config.ExpandCWD("${CWD}/.skills", "/home/user/project")
	if result != "/home/user/project/.skills" {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestResolvedPromptsDirEmpty(t *testing.T) {
	p := config.PromptsConfig{}
	if got := p.ResolvedPromptsDir("/tmp/ws"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestResolvedPromptsDirCWD(t *testing.T) {
	p := config.PromptsConfig{Dir: "${CWD}/prompts"}
	got := p.ResolvedPromptsDir("/project/root")
	want := filepath.Clean("/project/root/prompts")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestEnvVarExpansion(t *testing.T) {
	t.Setenv("TEST_API_KEY", "secret-key-123")

	content := `
models:
  definitions:
    - id: "test"
      provider: "openai"
      model: "gpt-4o"
      api_key: "${TEST_API_KEY}"
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

	if len(cfg.Models.Defs) == 0 {
		t.Fatal("expected model definitions")
	}
	if cfg.Models.Defs[0].APIKey != "secret-key-123" {
		t.Errorf("expected api_key %q, got %q", "secret-key-123", cfg.Models.Defs[0].APIKey)
	}
}
