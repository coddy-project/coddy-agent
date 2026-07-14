package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Skills is the YAML skills section (key skills).
type Skills struct {
	Dirs []string `yaml:"dirs"`

	// Sources lists remote skill sources to install from (GitHub repos, git URLs,
	// or an http(s) URL to an agents-standard marketplace.json). Fetched on demand
	// via `coddy skills sync` (never automatically), materialized into ManagedDir.
	Sources []string `yaml:"sources"`
}

// ManagedDir returns the directory used for coddy-managed skills (enable/disable state,
// UI-installed skills). Always resolves to ${CODDY_HOME}/skills or ~/.coddy/skills.
func (c *Skills) ManagedDir(coddyHome string) string {
	if coddyHome != "" {
		return filepath.Join(coddyHome, "skills")
	}
	return expandSkillsHome("~/.coddy/skills")
}

// ApplyDefaults fills empty Dirs during config load.
func (c *Skills) ApplyDefaults(coddyHome string, expandCODDYHome func(string) string) {
	if len(c.Dirs) == 0 {
		c.Dirs = []string{
			"~/.agents/skills",
			"${CODDY_HOME}/skills",
			"${CWD}/.coddy/skills",
		}
		return
	}
	for i := range c.Dirs {
		c.Dirs[i] = expandCODDYHome(c.Dirs[i])
	}
}

// Validate accepts any layout produced by ApplyDefaults.
func (c *Skills) Validate() error {
	return nil
}

func expandSkillsHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
