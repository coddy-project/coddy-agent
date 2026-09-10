package config

import (
	"fmt"
	"strings"
)

// Defaults for hooks.
const (
	// HooksDefaultTimeoutSeconds bounds one hook process when the definition
	// gives no timeout of its own.
	HooksDefaultTimeoutSeconds = 60
	// HooksDefaultStopLoopLimit is how many times per turn a Stop hook may
	// send the agent back to work (Cursor's loop_limit default).
	HooksDefaultStopLoopLimit = 5
	// HooksDefaultMaxOutputChars caps the text a hook may hand to the model or
	// the user in one field (Claude Code's 10,000-character ceiling).
	HooksDefaultMaxOutputChars = 10000
)

// Hooks is the YAML hooks section (key hooks): where hook definition files
// come from, how files found inside the workspace are trusted, and the bounds
// the runner applies to every hook process.
type Hooks struct {
	// Enabled loads and runs hooks at all. Unset means enabled.
	Enabled *bool `yaml:"enable"`

	// Files lists definition files, lowest priority first (priority orders
	// the catalog and the run order; every matching hook runs). ${CODDY_HOME}
	// expands at load time, ${CWD} per session; a relative entry resolves
	// against the session cwd.
	Files []string `yaml:"files"`

	// ProjectTrust decides what a file found inside the workspace may do: ask
	// (default), allow, or deny. Same vocabulary as mcp.project_trust and
	// subagents.project_trust.
	ProjectTrust string `yaml:"project_trust"`

	// DefaultTimeoutSeconds bounds a hook process whose definition gives no
	// timeout. 0 uses the default.
	DefaultTimeoutSeconds int `yaml:"default_timeout_seconds"`

	// StopLoopLimit caps how many times per turn a Stop hook may keep the agent
	// working. 0 uses the default.
	StopLoopLimit int `yaml:"stop_loop_limit"`

	// MaxOutputChars caps additionalContext, systemMessage, reasons and plain
	// stdout that reach the model or the user; longer values are truncated
	// with a marker. 0 uses the default.
	MaxOutputChars int `yaml:"max_output_chars"`
}

// DefaultHookFiles are the definition files used when the operator lists
// none: the coddy home first, then the Claude Code settings files of the
// workspace (compatibility, only their hooks key is read), then the
// workspace's own file.
func DefaultHookFiles() []string {
	return []string{
		"${CODDY_HOME}/hooks.json",
		"${CWD}/.claude/settings.json",
		"${CWD}/.claude/settings.local.json",
		"${CWD}/.coddy/hooks.json",
	}
}

// ApplyDefaults fills Files the way subagents.dirs is filled: the defaults
// keep their ${CODDY_HOME} and ${CWD} placeholders for the loader to expand
// per session, while operator-supplied entries get ${CODDY_HOME} expanded at
// load time.
func (h *Hooks) ApplyDefaults(p Paths) {
	if len(h.Files) == 0 {
		h.Files = DefaultHookFiles()
		return
	}
	for i := range h.Files {
		h.Files[i] = ExpandCODDYHomeOnly(strings.TrimSpace(h.Files[i]), p)
	}
}

// ResolvedEnabled reports whether hooks run, defaulting to true when the
// field is unset or the section is nil.
func (h *Hooks) ResolvedEnabled() bool {
	if h == nil || h.Enabled == nil {
		return true
	}
	return *h.Enabled
}

// ResolvedProjectTrust returns ProjectTrust with a safe default of ask, so an
// empty or unknown value never widens the policy.
func (h Hooks) ResolvedProjectTrust() string {
	switch v := strings.ToLower(strings.TrimSpace(h.ProjectTrust)); v {
	case ProjectTrustAsk, ProjectTrustAllow, ProjectTrustDeny:
		return v
	default:
		return ProjectTrustAsk
	}
}

// EffectiveDefaultTimeoutSeconds returns default_timeout_seconds with the
// default applied.
func (h Hooks) EffectiveDefaultTimeoutSeconds() int {
	if h.DefaultTimeoutSeconds <= 0 {
		return HooksDefaultTimeoutSeconds
	}
	return h.DefaultTimeoutSeconds
}

// EffectiveStopLoopLimit returns stop_loop_limit with the default applied.
func (h Hooks) EffectiveStopLoopLimit() int {
	if h.StopLoopLimit <= 0 {
		return HooksDefaultStopLoopLimit
	}
	return h.StopLoopLimit
}

// EffectiveMaxOutputChars returns max_output_chars with the default applied.
func (h Hooks) EffectiveMaxOutputChars() int {
	if h.MaxOutputChars <= 0 {
		return HooksDefaultMaxOutputChars
	}
	return h.MaxOutputChars
}

// Validate normalises project_trust and rejects negative knobs; zero keeps
// meaning "use the default" everywhere.
func (h *Hooks) Validate() error {
	v := strings.ToLower(strings.TrimSpace(h.ProjectTrust))
	if v == "" {
		v = ProjectTrustAsk
	}
	switch v {
	case ProjectTrustAsk, ProjectTrustAllow, ProjectTrustDeny:
		h.ProjectTrust = v
	default:
		return fmt.Errorf("hooks.project_trust: must be one of %q, %q, %q (got %q)",
			ProjectTrustAsk, ProjectTrustAllow, ProjectTrustDeny, h.ProjectTrust)
	}
	for name, n := range map[string]int{
		"default_timeout_seconds": h.DefaultTimeoutSeconds,
		"stop_loop_limit":         h.StopLoopLimit,
		"max_output_chars":        h.MaxOutputChars,
	} {
		if n < 0 {
			return fmt.Errorf("hooks.%s: must be >= 0", name)
		}
	}
	return nil
}
