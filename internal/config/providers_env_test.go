package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestProviderAPIKeyEnvVarName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"rpa", "RPA_API_KEY"},
		{"my-rpa", "MY_RPA_API_KEY"},
		{"a_b", "A_B_API_KEY"},
		{in: "", want: ""},
		{"bad name", ""},
		{"9start", ""},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("in=%q", tt.in), func(t *testing.T) {
			if got := ProviderAPIKeyEnvVarName(tt.in); got != tt.want {
				t.Fatalf("ProviderAPIKeyEnvVarName(%q) = %q want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestProviderConfigEffectiveAPIKey(t *testing.T) {
	t.Setenv("RPA_API_KEY", "from-env")
	p := &ProviderConfig{Name: "rpa", Type: "openai", APIKey: ""}
	if got := p.EffectiveAPIKey(); got != "from-env" {
		t.Fatalf("EffectiveAPIKey empty: got %q", got)
	}
	p.APIKey = "literal"
	if got := p.EffectiveAPIKey(); got != "literal" {
		t.Fatalf("EffectiveAPIKey literal: got %q", got)
	}
	p = &ProviderConfig{Name: "bad name", Type: "openai", APIKey: ""}
	if got := p.EffectiveAPIKey(); got != "" {
		t.Fatalf("invalid name should not read env: got %q", got)
	}
}

func TestProviderConfigEffectiveAPIKeyCommand(t *testing.T) {
	// api_key_command stdout (trimmed) becomes the key when api_key is empty.
	p := &ProviderConfig{Name: "rpa", Type: "openai", APIKeyCommand: apiKeyOutputCommand("k-from-cmd")}
	if got := p.EffectiveAPIKey(); got != "k-from-cmd" {
		t.Fatalf("EffectiveAPIKey command: got %q want k-from-cmd", got)
	}
	// A literal api_key wins over the command.
	p.APIKey = "literal"
	if got := p.EffectiveAPIKey(); got != "literal" {
		t.Fatalf("literal should win over command: got %q", got)
	}
	// The command wins over the conventional env var.
	t.Setenv("RPA_API_KEY", "from-env")
	p = &ProviderConfig{Name: "rpa", Type: "openai", APIKeyCommand: apiKeyOutputCommand("k-from-cmd")}
	if got := p.EffectiveAPIKey(); got != "k-from-cmd" {
		t.Fatalf("command should win over env: got %q", got)
	}
	// A failing/empty command falls back to the env var (best-effort helper).
	p = &ProviderConfig{Name: "rpa", Type: "openai", APIKeyCommand: apiKeyFailureCommand()}
	if got := p.EffectiveAPIKey(); got != "from-env" {
		t.Fatalf("failed command should fall back to env: got %q", got)
	}
}

func apiKeyOutputCommand(value string) string {
	switch platform.CurrentShell().Kind {
	case platform.ShellPwsh, platform.ShellPowerShell:
		return "Write-Output '" + value + "'"
	case platform.ShellCmd:
		return "echo " + value
	default:
		return "printf '" + value + "\\n'"
	}
}

func apiKeyFailureCommand() string {
	if platform.CurrentShell().Kind == platform.ShellCmd {
		return "exit /b 3"
	}
	return "exit 3"
}

func TestResolveLLMUsesEffectiveAPIKey(t *testing.T) {
	t.Setenv("RPA_API_KEY", "k-env")
	cfg := &Config{
		Providers: []ProviderConfig{
			{Name: "rpa", Type: "openai", APIBase: "https://example", APIKey: ""},
		},
		Models: []ModelEntry{{Model: "rpa/m", MaxTokens: 10, Temperature: 0.1}},
		Agent:  Agent{Model: "rpa/m"},
	}
	rm, err := cfg.ResolveLLM("rpa/m")
	if err != nil {
		t.Fatal(err)
	}
	if rm.APIKey != "k-env" {
		t.Fatalf("APIKey got %q want k-env", rm.APIKey)
	}
}

func TestValidateProviderNamePattern(t *testing.T) {
	p := &ProviderConfig{Name: "bad name", Type: "openai", APIKey: "x"}
	p.Normalize()
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for invalid provider name")
	}
	p = &ProviderConfig{Name: "ok-name_1", Type: "openai", APIKey: "x"}
	p.Normalize()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadYAMLProviderEmptyAPIKeyStoredEmptyResolveUsesEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvCODDYHome, home)
	t.Setenv("RPA_API_KEY", "secret")

	yml := `
providers:
  - name: rpa
    type: openai
    api_base: "https://api.example"
    api_key: ""

models:
  - model: "rpa/m"
    max_tokens: 10
    temperature: 0.1

agent:
  model: "rpa/m"
`
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].APIKey != "" {
		t.Fatalf("stored api_key should stay empty for YAML empty string, got %#v", cfg.Providers)
	}
	rm, err := cfg.ResolveLLM("rpa/m")
	if err != nil {
		t.Fatal(err)
	}
	if rm.APIKey != "secret" {
		t.Fatalf("ResolveLLM APIKey got %q", rm.APIKey)
	}
}
