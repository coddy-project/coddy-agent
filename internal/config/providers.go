package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
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
	"codex":      {},
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
	// TimeoutMS, when positive, bounds each LLM HTTP request to this provider,
	// including the streamed body read. 0 (the default) sets no client timeout,
	// so slow prompt processing on large contexts is never cut short; the turn
	// context stays the only bound.
	TimeoutMS int `yaml:"timeout_ms"`
	// UsageLimitsPanel switches the account usage panel of this row: the
	// console footer line and /usage, the web UI's usage section and banner,
	// and the reads behind them (GET /v1/limits for a neuraldeep row). A nil
	// pointer (key omitted) means on. An explicit false makes Coddy treat the
	// row like a provider without a usage source: nothing is fetched and
	// nothing is shown. Only rows whose type has a usage source are affected.
	UsageLimitsPanel *bool `yaml:"usage_limits_panel,omitempty"`
}

// EffectiveUsageLimitsPanel reports whether the row's usage limits panel is
// on. Only an explicit usage_limits_panel: false turns it off.
func (p *ProviderConfig) EffectiveUsageLimitsPanel() bool {
	if p == nil || p.UsageLimitsPanel == nil {
		return true
	}
	return *p.UsageLimitsPanel
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
	return p.EffectiveAPIKeyContext(context.Background())
}

// EffectiveAPIKeyContext is EffectiveAPIKey bounded by ctx: a credential
// helper that outlives the caller's deadline is killed and the resolution
// falls back to the environment variable, so a hung helper cannot hold a
// caller that budgeted a few seconds for a network read.
func (p *ProviderConfig) EffectiveAPIKeyContext(ctx context.Context) string {
	key, _ := p.EffectiveAPIKeyContextErr(ctx)
	return key
}

// EffectiveAPIKeyContextErr is EffectiveAPIKeyContext that also reports why
// the credential helper yielded nothing when it ran out of time or was
// cancelled (context.DeadlineExceeded, context.Canceled): a caller can then
// tell "no credential" from "the helper did not answer in time". A helper
// that exited without output is not an error; the environment variable is
// the fallback in every case.
func (p *ProviderConfig) EffectiveAPIKeyContextErr(ctx context.Context) (string, error) {
	if p == nil {
		return "", nil
	}
	if k := strings.TrimSpace(p.APIKey); k != "" {
		return k, nil
	}
	var helperErr error
	if cmd := strings.TrimSpace(p.APIKeyCommand); cmd != "" {
		k, err := runAPIKeyCommandContext(ctx, cmd)
		if k != "" {
			return k, nil
		}
		helperErr = err
	}
	env := ProviderAPIKeyEnvVarName(p.Name)
	if env == "" {
		return "", helperErr
	}
	if k := strings.TrimSpace(os.Getenv(env)); k != "" {
		return k, nil
	}
	return "", helperErr
}

// runAPIKeyCommandContext executes a provider credential-helper command via
// the detected host shell, under the caller's context on top of the usual
// timeout, and returns its trimmed stdout. It returns "" on any failure
// (non-zero exit, timeout, cancellation, spawn failure) so the resolution can
// fall back to the conventional env var; the error is set only when the
// helper was cut short by the timeout or the caller's context.
func runAPIKeyCommandContext(parent context.Context, command string) (string, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, apiKeyCommandTimeout)
	defer cancel()
	commandShell := platform.CurrentShell()
	executable, args := commandShell.Command(command)
	out, err := exec.CommandContext(ctx, executable, args...).Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", nil
	}
	return strings.TrimSpace(platform.DecodeOutput(out)), nil
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
	if p.TimeoutMS < 0 {
		return fmt.Errorf("providers[%s]: timeout_ms must be >= 0", p.Name)
	}
	return nil
}
