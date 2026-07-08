package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// apiKeyCommandTimeout bounds how long a provider api_key_command may run.
const apiKeyCommandTimeout = 30 * time.Second

// validProviderName constrains providers[].name to ASCII letters, digits, hyphen, and underscore,
// starting with a letter (stable mapping to environment variable names).
var validProviderName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)

// AllowedLLMProviderTypes lists provider kinds accepted in YAML (internal/llm.NewProvider).
var AllowedLLMProviderTypes = map[string]struct{}{
	"openai":     {},
	"anthropic":  {},
	"neuraldeep": {},
}

// ProviderConfig is one entry under YAML key providers.
type ProviderConfig struct {
	Name    string `yaml:"name"`
	Type    string `yaml:"type"`
	APIBase string `yaml:"api_base"`
	APIKey  string `yaml:"api_key"`
	// APIKeyCommand is an optional credential-helper command. When api_key is empty,
	// the command is run via the shell and its trimmed stdout is used as the key
	// (akin to git/docker credential helpers or AWS credential_process). This lets a
	// provider fetch short-lived or login-issued keys without storing a static secret
	// in the config. On failure resolution falls back to the conventional env var.
	APIKeyCommand string `yaml:"api_key_command"`
	// Proxy is an optional HTTP, HTTPS, SOCKS5, or SOCKS5h proxy URL for outbound LLM requests for this provider only.
	Proxy string `yaml:"proxy"`
}

// ProviderAPIKeyEnvVarName returns the conventional environment variable name for this provider's
// API key when api_key is left empty (uppercase name with hyphens mapped to underscores, plus _API_KEY).
// Returns empty when providerName is not a valid provider id.
func ProviderAPIKeyEnvVarName(providerName string) string {
	name := strings.TrimSpace(providerName)
	if !validProviderName.MatchString(name) {
		return ""
	}
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_API_KEY"
}

// EffectiveAPIKey returns the key to pass to LLM clients. Resolution order:
// the configured non-empty api_key, then api_key_command stdout (when set and it
// succeeds), then the conventional environment variable derived from the provider
// name (see ProviderAPIKeyEnvVarName).
func (p *ProviderConfig) EffectiveAPIKey() string {
	if p == nil {
		return ""
	}
	if k := strings.TrimSpace(p.APIKey); k != "" {
		return k
	}
	if cmd := strings.TrimSpace(p.APIKeyCommand); cmd != "" {
		if k := runAPIKeyCommand(cmd); k != "" {
			return k
		}
	}
	env := ProviderAPIKeyEnvVarName(p.Name)
	if env == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(env))
}

// runAPIKeyCommand executes a provider credential-helper command via the shell and
// returns its trimmed stdout. It returns "" on any error (non-zero exit, timeout,
// spawn failure) so EffectiveAPIKey can fall back to the conventional env var.
func runAPIKeyCommand(command string) string {
	ctx, cancel := context.WithTimeout(context.Background(), apiKeyCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", command).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Normalize trims string fields in place.
func (p *ProviderConfig) Normalize() {
	p.Name = strings.TrimSpace(p.Name)
	p.Type = strings.TrimSpace(p.Type)
	p.APIBase = strings.TrimSpace(p.APIBase)
	p.APIKey = strings.TrimSpace(p.APIKey)
	p.APIKeyCommand = strings.TrimSpace(p.APIKeyCommand)
	p.Proxy = strings.TrimSpace(p.Proxy)
}

// Validate checks a single provider after Normalize.
func (p *ProviderConfig) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("providers: name is required")
	}
	if !validProviderName.MatchString(p.Name) {
		return fmt.Errorf("providers[%s]: name must be ASCII letters, digits, hyphen, or underscore, starting with a letter", p.Name)
	}
	if p.Type == "" {
		return fmt.Errorf("providers[%s]: type is required", p.Name)
	}
	if _, ok := AllowedLLMProviderTypes[p.Type]; !ok {
		return fmt.Errorf("providers[%s]: unsupported type %q", p.Name, p.Type)
	}
	if err := validateProviderProxyURL(p.Proxy); err != nil {
		return fmt.Errorf("providers[%s]: %w", p.Name, err)
	}
	return nil
}
