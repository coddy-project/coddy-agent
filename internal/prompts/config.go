package prompts

import (
	"os"
	"path/filepath"
	"strings"
)

// Config selects a directory of system prompt templates (agent.md, plan.md).
// Empty Dir means embedded built-in templates.
type Config struct {
	// Dir is a directory containing agent.md and plan.md (Go text/template).
	// If empty after trim, embedded defaults are used. Supports ${CWD} and ~.
	Dir string `yaml:"dir" json:"dir"`
}

// Validate normalises the config in place. Prompts currently accept any Dir
// string; empty means use embedded templates.
func (c *Config) Validate() error {
	c.Dir = strings.TrimSpace(c.Dir)
	return nil
}

// ResolvedDir returns the prompts directory with ~ and ${CWD} expanded for
// the given session cwd. Empty Dir (after trim) means callers should pass ""
// to Render (embedded defaults).
func (c *Config) ResolvedDir(sessionCWD string) string {
	d := strings.TrimSpace(c.Dir)
	if d == "" {
		return ""
	}
	return filepath.Clean(expandCWD(d, sessionCWD))
}

func expandCWD(s, cwd string) string {
	s = strings.ReplaceAll(s, "${CWD}", cwd)
	return expandHome(s)
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
