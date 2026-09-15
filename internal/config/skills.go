package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
)

// SkillsAutoDiscoveryFlagName is the CLI flag (on `coddy acp` / `coddy serve`)
// that overrides skills.auto_discovery.
const SkillsAutoDiscoveryFlagName = "skills-auto-discovery"

// ApplySkillsAutoDiscoveryFlag overrides skills.auto_discovery only when the
// -skills-auto-discovery flag was explicitly provided on fs; otherwise the config
// value (which defaults to true) is left untouched. Shared by the acp and http
// command entrypoints so both behave identically.
func ApplySkillsAutoDiscoveryFlag(fs *flag.FlagSet, cfg *Config, val *bool) {
	if fs == nil || cfg == nil || val == nil {
		return
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == SkillsAutoDiscoveryFlagName {
			v := *val
			cfg.Skills.AutoDiscovery = &v
		}
	})
}

// DefaultSkillsSource is the marketplace Coddy is configured with out of the
// box: the catalogue that publishes the skills of the standard delivery plus
// the rest of the same collection. It is only an address - nothing is fetched
// from it until `coddy skills sync` asks - and an operator who removes it keeps
// it removed, because a source list that exists in the file is taken as given.
const DefaultSkillsSource = "EvilFreelancer/rpa-skills"

// Skills is the YAML skills section (key skills).
type Skills struct {
	Dirs []string `yaml:"dirs"`

	// Sources lists remote skill sources to install from (GitHub repos, git URLs,
	// or an http(s) URL to an agents-standard marketplace.json). Fetched on demand
	// via `coddy skills sync` (never automatically), materialized into ManagedDir.
	Sources []string `yaml:"sources"`

	// AutoDiscovery enables the model-driven load_skill tool: the agent may pull a
	// catalogued skill's full instructions into a turn on its own when the request
	// matches, instead of requiring an explicit /name invocation. Defaults to true.
	AutoDiscovery *bool `yaml:"auto_discovery"`
}

// ManagedDir returns the directory used for coddy-managed skills (enable/disable state,
// UI-installed skills). Always resolves to ${CODDY_HOME}/skills or ~/.coddy/skills.
func (c *Skills) ManagedDir(coddyHome string) string {
	if coddyHome != "" {
		return filepath.Join(coddyHome, "skills")
	}
	return expandSkillsHome("~/.coddy/skills")
}

// ApplyDefaults fills empty Dirs during config load, and connects the default
// marketplace when the file says nothing about sources at all. An explicit
// (even empty) sources list is the operator's answer and is left alone, which
// is how removing the default marketplace stays removed.
func (c *Skills) ApplyDefaults(coddyHome string, expandCODDYHome func(string) string) {
	if c.AutoDiscovery == nil {
		v := true
		c.AutoDiscovery = &v
	}
	if c.Sources == nil {
		c.Sources = []string{DefaultSkillsSource}
	}
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

// AutoDiscoveryEnabled reports whether the model-driven load_skill tool is offered.
func (c *Skills) AutoDiscoveryEnabled() bool {
	if c.AutoDiscovery == nil {
		return true
	}
	return *c.AutoDiscovery
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
