package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	"devin":      {},
	"acp":        {},
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
	// Proxy is how every request of this provider is routed: empty or
	// "inherit" follows the environment's proxy (HTTPS_PROXY, HTTP_PROXY,
	// NO_PROXY), "none" connects directly, and an http, https, socks5 or
	// socks5h URL goes through that proxy. See ParseProxySetting.
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
	// ACP is the agent an acp row runs: a local program speaking the Agent
	// Client Protocol on stdio, served as a model (internal/llm/acp*.go).
	// Read only on a row of type acp.
	ACP *ACPProviderConfig `yaml:"acp,omitempty"`
}

// ACP permission policies: what an acp row does with the agent's
// session/request_permission.
const (
	// ACPPermissionsAsk forwards the request to the person through the
	// session's permission prompt (the default).
	ACPPermissionsAsk = "ask"
	// ACPPermissionsAllow approves every request once.
	ACPPermissionsAllow = "allow"
	// ACPPermissionsDeny refuses every request.
	ACPPermissionsDeny = "deny"
)

// ACPProviderConfig is the acp block of a provider row of type acp.
type ACPProviderConfig struct {
	// Command is the program that speaks ACP on stdio (PATH lookup); Args
	// follow it.
	Command string   `yaml:"command" json:"command"`
	Args    []string `yaml:"args,omitempty" json:"args,omitempty"`
	// Env is added to the environment Coddy passes the agent.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// CWD, when set, is the folder of the agent process and of every ACP
	// session; empty opens each session in the Coddy session's workspace.
	CWD string `yaml:"cwd,omitempty" json:"cwd,omitempty"`
	// AuthMethod is the ACP authenticate methodId called after initialize
	// (codex-acp: api-key); empty calls none.
	AuthMethod string `yaml:"auth_method,omitempty" json:"auth_method,omitempty"`
	// Permissions is ask (default), allow or deny.
	Permissions string `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// Modes picks the agent's ACP mode for each Coddy session mode; an empty
	// value keeps the agent's own default.
	Modes ACPModes `yaml:"modes,omitempty" json:"modes"`
	// IdleTimeoutMS stops the agent this long after its last call; 0 means
	// sixty minutes.
	IdleTimeoutMS int `yaml:"idle_timeout_ms,omitempty" json:"idle_timeout_ms,omitempty"`
	// Process is shared (default: one agent process serves every session of
	// the row) or per_session (a process for each Coddy session, for an
	// agent that runs one prompt at a time, such as zcode-acp-server).
	Process string `yaml:"process,omitempty" json:"process,omitempty"`
}

// ACP process sharing values.
const (
	ACPProcessShared     = "shared"
	ACPProcessPerSession = "per_session"
)

// PerSession reports whether every Coddy session gets an agent process of
// its own.
func (c *ACPProviderConfig) PerSession() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(c.Process), ACPProcessPerSession)
}

// ACPModes maps the Coddy session modes onto ACP mode ids of the agent.
type ACPModes struct {
	Agent string `yaml:"agent,omitempty" json:"agent,omitempty"`
	Plan  string `yaml:"plan,omitempty" json:"plan,omitempty"`
	Ask   string `yaml:"ask,omitempty" json:"ask,omitempty"`
}

// For returns the ACP mode configured for a Coddy session mode.
func (m ACPModes) For(mode string) string {
	switch strings.TrimSpace(mode) {
	case "plan":
		return strings.TrimSpace(m.Plan)
	case "ask":
		return strings.TrimSpace(m.Ask)
	default:
		return strings.TrimSpace(m.Agent)
	}
}

// EffectivePermissions returns the permission policy with the default
// applied.
func (c *ACPProviderConfig) EffectivePermissions() string {
	if c == nil {
		return ACPPermissionsAsk
	}
	switch p := strings.ToLower(strings.TrimSpace(c.Permissions)); p {
	case ACPPermissionsAllow, ACPPermissionsDeny:
		return p
	default:
		return ACPPermissionsAsk
	}
}

// NamesWorkspace reports whether any value of the block names ${CWD}: such
// an agent is started once per workspace rather than once per row.
func (c *ACPProviderConfig) NamesWorkspace() bool {
	if c == nil {
		return false
	}
	has := func(s string) bool { return strings.Contains(s, sessionCWDPlaceholder) }
	if has(c.Command) || has(c.CWD) {
		return true
	}
	for _, a := range c.Args {
		if has(a) {
			return true
		}
	}
	for _, v := range c.Env {
		if has(v) {
			return true
		}
	}
	return false
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

// TakesAPIKey reports whether requests of this row carry an API key at all:
// every type but codex, which signs in with ChatGPT, and acp, whose agent
// signs in on its own; neither reads api_key, api_key_command or the
// NAME_API_KEY variable.
func (p *ProviderConfig) TakesAPIKey() bool {
	if p == nil {
		return false
	}
	switch strings.TrimSpace(p.Type) {
	case "codex", "acp":
		return false
	}
	return true
}

// EffectiveAPIKey returns the key to pass to LLM clients. Resolution order:
// the configured non-empty api_key, then api_key_command stdout (when set and it
// succeeds), then the conventional environment variable derived from the provider
// name (see ProviderAPIKeyEnvVarName). A row that takes no key (TakesAPIKey)
// resolves none: none of the three is read and no helper runs.
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
	if !p.TakesAPIKey() {
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
	cmd := exec.CommandContext(ctx, executable, args...)
	platform.AdaptCommand(cmd)
	out, err := cmd.Output()
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
	p.Proxy = normalizeProxySetting(p.Proxy)
	if p.ACP != nil {
		p.ACP.Command = strings.TrimSpace(p.ACP.Command)
		p.ACP.CWD = strings.TrimSpace(p.ACP.CWD)
		p.ACP.AuthMethod = strings.TrimSpace(p.ACP.AuthMethod)
		p.ACP.Permissions = strings.ToLower(strings.TrimSpace(p.ACP.Permissions))
		p.ACP.Process = strings.ToLower(strings.TrimSpace(p.ACP.Process))
	}
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
	if err := validateProxySetting(p.Proxy); err != nil {
		// The message opens with "proxy:", so the path it names is the key
		// itself and coddy -t points at that line with the key's own doc.
		return fmt.Errorf("providers[%s].%w", p.Name, err)
	}
	if p.TimeoutMS < 0 {
		return fmt.Errorf("providers[%s]: timeout_ms must be >= 0", p.Name)
	}
	if p.Type == "acp" {
		if p.ACP == nil || p.ACP.Command == "" {
			return fmt.Errorf("providers[%s].acp.command: an acp provider needs the command of its agent", p.Name)
		}
		switch p.ACP.Permissions {
		case "", ACPPermissionsAsk, ACPPermissionsAllow, ACPPermissionsDeny:
		default:
			return fmt.Errorf("providers[%s].acp.permissions: must be ask, allow or deny, not %q", p.Name, p.ACP.Permissions)
		}
		switch p.ACP.Process {
		case "", ACPProcessShared, ACPProcessPerSession:
		default:
			return fmt.Errorf("providers[%s].acp.process: must be shared or per_session, not %q", p.Name, p.ACP.Process)
		}
		if p.ACP.IdleTimeoutMS < 0 {
			return fmt.Errorf("providers[%s].acp.idle_timeout_ms: must be >= 0", p.Name)
		}
		if cwd := p.ACP.CWD; cwd != "" && !filepath.IsAbs(cwd) && cwd != "~" && !strings.HasPrefix(cwd, "~/") && !strings.HasPrefix(cwd, sessionCWDPlaceholder) {
			// ACP sessions open in an absolute folder; a relative one would
			// depend on wherever Coddy happened to start.
			return fmt.Errorf("providers[%s].acp.cwd: must be an absolute path (or start with ~ or ${CWD}), not %q", p.Name, cwd)
		}
	}
	return nil
}
