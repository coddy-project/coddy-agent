package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Prompts is the YAML prompts section (key prompts).
type Prompts struct {
	Dir string `yaml:"dir" json:"dir"`
}

// Validate normalises the prompts section in place.
func (c *Prompts) Validate() error {
	c.Dir = strings.TrimSpace(c.Dir)
	return nil
}

// ResolvedDir returns the prompts directory with ~ and ${CWD} expanded for session cwd.
func (c *Prompts) ResolvedDir(sessionCWD string) string {
	d := strings.TrimSpace(c.Dir)
	if d == "" {
		return ""
	}
	return filepath.Clean(expandPromptsCWD(d, sessionCWD))
}

func expandPromptsCWD(s, cwd string) string {
	s = strings.ReplaceAll(s, "${CWD}", cwd)
	return expandPromptsHome(s)
}

func expandPromptsHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
