package config

import "strings"

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
	return nil
}
