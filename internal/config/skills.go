package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Skills is the YAML skills section (key skills).
type Skills struct {
	Dirs       []string `yaml:"dirs"`
	InstallDir string   `yaml:"install_dir"`
}

// ApplyDefaults fills empty InstallDir and Dirs during config load.
func (c *Skills) ApplyDefaults(coddyHome string, expandCODDYHome func(string) string) {
	if strings.TrimSpace(c.InstallDir) == "" {
		if coddyHome != "" {
			c.InstallDir = filepath.Join(coddyHome, "skills")
		} else {
			c.InstallDir = expandSkillsHome("~/.coddy/skills")
		}
	} else {
		c.InstallDir = filepath.Clean(expandCODDYHome(c.InstallDir))
	}

	if len(c.Dirs) == 0 {
		c.Dirs = []string{
			"${CODDY_HOME}/skills",
			"${CWD}/.skills",
			"~/.cursor/skills",
			"~/.claude/skills",
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
