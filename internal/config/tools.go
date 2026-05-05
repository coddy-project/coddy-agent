package config

import "strings"

// Tools is the YAML tools section (key tools).
type Tools struct {
	RequirePermissionForCommands bool     `yaml:"require_permission_for_commands"`
	RequirePermissionForWrites   bool     `yaml:"require_permission_for_writes"`
	RestrictToCWD                bool     `yaml:"restrict_to_cwd"`
	CommandAllowlist             []string `yaml:"command_allowlist"`
	// PermissionMasterKey when non-empty bypasses all tool permission prompts (dangerous; use only in trusted environments).
	PermissionMasterKey string `yaml:"permission_master_key"`
}

// Validate trims allowlist entries in place.
func (c *Tools) Validate() error {
	c.PermissionMasterKey = strings.TrimSpace(c.PermissionMasterKey)
	for i := range c.CommandAllowlist {
		c.CommandAllowlist[i] = strings.TrimSpace(c.CommandAllowlist[i])
	}
	return nil
}
