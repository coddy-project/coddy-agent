package config

import (
	"fmt"
	"strings"
)

// Permission mode constants for tools.permission_mode.
const (
	// PermModeAsk asks for user approval before each shell command and each file write.
	PermModeAsk = "ask"
	// PermModeAcceptEdits auto-approves file writes but still asks for shell commands.
	PermModeAcceptEdits = "accept_edits"
	// PermModeBypass skips all permission prompts (use only in fully trusted environments).
	PermModeBypass = "bypass"
)

// Tools is the YAML tools section (key tools).
type Tools struct {
	// PermissionMode controls when the agent asks for user approval before running tools.
	// Values: "ask" (default), "accept_edits", "bypass".
	PermissionMode   string   `yaml:"permission_mode"`
	CommandAllowlist []string `yaml:"command_allowlist"`

	// SSHConnectTimeout is the TCP dial timeout for SSH connections in seconds (default: 30).
	SSHConnectTimeout int `yaml:"ssh_connect_timeout"`

	// OutputLimits caps how many lines each tool result or error may return into
	// the LLM context. Enabled limits also activate the tooling-layer hard byte
	// safety ceiling so a huge single line cannot bypass them.
	OutputLimits ToolOutputLimits `yaml:"output_limits"`

	// Background bounds commands the agent runs as background tasks.
	Background ToolBackground `yaml:"background"`
}

// Defaults for tools.background.
const (
	BackgroundDefaultMaxConcurrent     = 5
	BackgroundDefaultTimeoutSeconds    = 900
	BackgroundDefaultMaxTimeoutSeconds = 3600
	BackgroundDefaultOutputBufferBytes = 262144
)

// ToolBackground is the YAML tools.background section. It governs run_command
// calls that ask to run detached, and the task pool that owns them.
type ToolBackground struct {
	// Enabled exposes the background option on run_command and the background
	// task tools. Unset means enabled; set it to false to keep every command in
	// the foreground.
	Enabled *bool `yaml:"enabled"`

	// MaxConcurrent is how many background tasks one session may run at once.
	MaxConcurrent int `yaml:"max_concurrent"`

	// DefaultTimeoutSeconds is the hard limit for a task whose caller gave
	// neither a timeout nor a duration estimate.
	DefaultTimeoutSeconds int `yaml:"default_timeout_seconds"`

	// MaxTimeoutSeconds caps whatever timeout the caller or the estimate asked
	// for, so no single task can outlive the ceiling the operator set.
	MaxTimeoutSeconds int `yaml:"max_timeout_seconds"`

	// OutputBufferBytes is how much of each task's output stays in memory for
	// the status ticker. The full log is still written to the session bundle.
	OutputBufferBytes int `yaml:"output_buffer_bytes"`
}

// ResolvedEnabled reports whether background execution is offered, defaulting to
// true when the field is unset.
func (b *ToolBackground) ResolvedEnabled() bool {
	if b == nil || b.Enabled == nil {
		return true
	}
	return *b.Enabled
}

// Resolved returns the section with every unset knob replaced by its default.
func (b *ToolBackground) Resolved() ToolBackground {
	out := ToolBackground{}
	if b != nil {
		out = *b
	}
	if out.MaxConcurrent <= 0 {
		out.MaxConcurrent = BackgroundDefaultMaxConcurrent
	}
	if out.DefaultTimeoutSeconds <= 0 {
		out.DefaultTimeoutSeconds = BackgroundDefaultTimeoutSeconds
	}
	if out.MaxTimeoutSeconds <= 0 {
		out.MaxTimeoutSeconds = BackgroundDefaultMaxTimeoutSeconds
	}
	if out.DefaultTimeoutSeconds > out.MaxTimeoutSeconds {
		out.DefaultTimeoutSeconds = out.MaxTimeoutSeconds
	}
	if out.OutputBufferBytes <= 0 {
		out.OutputBufferBytes = BackgroundDefaultOutputBufferBytes
	}
	return out
}

// validate rejects negative knobs; zero keeps meaning "use the default".
func (b *ToolBackground) validate() error {
	for name, v := range map[string]int{
		"max_concurrent":          b.MaxConcurrent,
		"default_timeout_seconds": b.DefaultTimeoutSeconds,
		"max_timeout_seconds":     b.MaxTimeoutSeconds,
		"output_buffer_bytes":     b.OutputBufferBytes,
	} {
		if v < 0 {
			return fmt.Errorf("tools.background.%s: must be >= 0", name)
		}
	}
	return nil
}

// Defaults for tools.output_limits (maximum lines a tool result or error may
// contribute to the LLM context; 0 disables both line and byte limits). Derived
// empirically from typical per-line token density.
const (
	OutputLimitDefaultRead          = 1000
	OutputLimitDefaultGrep          = 200
	OutputLimitDefaultGlob          = 300
	OutputLimitDefaultPrintTree     = 400
	OutputLimitDefaultRunCommand    = 500
	OutputLimitDefaultSSHRunCommand = 500
	OutputLimitDefaultWebFetch      = 800
	OutputLimitDefaultWebSearch     = 200
	OutputLimitDefaultDefault       = 1000
)

// ToolOutputLimits is the YAML tools.output_limits section: per-tool line
// ceilings for results and errors. Any positive limit also activates the shared
// hard byte ceiling. Pointer fields preserve unset versus explicit zero.
type ToolOutputLimits struct {
	Read          *int `yaml:"read"`
	Grep          *int `yaml:"grep"`
	Glob          *int `yaml:"glob"`
	PrintTree     *int `yaml:"print_tree"`
	RunCommand    *int `yaml:"run_command"`
	SSHRunCommand *int `yaml:"ssh_run_command"`
	WebFetch      *int `yaml:"webfetch"`
	WebSearch     *int `yaml:"websearch"`
	// Default applies to any tool not named above, including MCP tools.
	Default *int `yaml:"default"`
}

// outputLimitDefaultKey is the map key used for the Default limit (empty tool name).
const outputLimitDefaultKey = ""

// MaxLines returns the effective line ceiling for a tool, applying the built-in
// default when the field is unset. 0 disables both line and byte limits.
func (l *ToolOutputLimits) MaxLines(tool string) int {
	if l == nil {
		return 0
	}
	pick := func(p *int, def int) int {
		if p != nil {
			return *p
		}
		return def
	}
	switch tool {
	case "read":
		return pick(l.Read, OutputLimitDefaultRead)
	case "grep":
		return pick(l.Grep, OutputLimitDefaultGrep)
	case "glob":
		return pick(l.Glob, OutputLimitDefaultGlob)
	case "print_tree":
		return pick(l.PrintTree, OutputLimitDefaultPrintTree)
	case "run_command":
		return pick(l.RunCommand, OutputLimitDefaultRunCommand)
	case "ssh_run_command":
		return pick(l.SSHRunCommand, OutputLimitDefaultSSHRunCommand)
	case "webfetch":
		return pick(l.WebFetch, OutputLimitDefaultWebFetch)
	case "websearch":
		return pick(l.WebSearch, OutputLimitDefaultWebSearch)
	default:
		return pick(l.Default, OutputLimitDefaultDefault)
	}
}

// AsMap materializes the effective per-tool ceilings for the tool execution layer.
// The empty-string key carries the Default limit for unlisted (and MCP) tools.
func (l *ToolOutputLimits) AsMap() map[string]int {
	names := []string{
		"read", "grep", "glob", "print_tree",
		"run_command", "ssh_run_command", "webfetch", "websearch",
	}
	out := make(map[string]int, len(names)+1)
	for _, n := range names {
		out[n] = l.MaxLines(n)
	}
	out[outputLimitDefaultKey] = l.MaxLines(outputLimitDefaultKey)
	return out
}

// validate checks that no explicitly set limit is negative.
func (l *ToolOutputLimits) validate() error {
	for name, p := range map[string]*int{
		"read": l.Read, "grep": l.Grep, "glob": l.Glob, "print_tree": l.PrintTree,
		"run_command": l.RunCommand, "ssh_run_command": l.SSHRunCommand,
		"webfetch": l.WebFetch, "websearch": l.WebSearch, "default": l.Default,
	} {
		if p != nil && *p < 0 {
			return fmt.Errorf("tools.output_limits.%s: must be >= 0", name)
		}
	}
	return nil
}

// ResolvedPermMode returns PermissionMode with a safe default of PermModeAsk.
func (c *Tools) ResolvedPermMode() string {
	switch c.PermissionMode {
	case PermModeAsk, PermModeAcceptEdits, PermModeBypass:
		return c.PermissionMode
	default:
		return PermModeAsk
	}
}

// Validate trims allowlist entries in place and normalises PermissionMode.
func (c *Tools) Validate() error {
	if c.PermissionMode == "" {
		c.PermissionMode = PermModeAsk
	}
	for i := range c.CommandAllowlist {
		c.CommandAllowlist[i] = strings.TrimSpace(c.CommandAllowlist[i])
	}
	if c.SSHConnectTimeout <= 0 {
		c.SSHConnectTimeout = 30
	}
	if err := c.OutputLimits.validate(); err != nil {
		return err
	}
	return c.Background.validate()
}
