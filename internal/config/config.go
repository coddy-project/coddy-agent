package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultSearchPaths are the locations searched for config.yaml.
var DefaultSearchPaths = []string{
	"~/.config/coddy-agent/config.yaml",
	"./config.yaml",
}

// Load reads config from the given path, or searches default locations.
// After loading, environment variable references (${VAR}) are resolved.
func Load(path string) (*Config, error) {
	if path != "" {
		return loadFile(path)
	}

	for _, p := range DefaultSearchPaths {
		expanded := expandHome(p)
		if _, err := os.Stat(expanded); err == nil {
			return loadFile(expanded)
		}
	}

	// No config found - return sensible defaults.
	return defaults(), nil
}

func loadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	// Expand environment variables in the raw YAML before parsing.
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	applyDefaults(&cfg)
	return &cfg, nil
}

// defaults returns a minimal working configuration.
func defaults() *Config {
	cfg := &Config{}
	applyDefaults(cfg)
	return cfg
}

func applyDefaults(cfg *Config) {
	if cfg.Agent.Name == "" {
		cfg.Agent.Name = "coddy-agent"
	}
	if cfg.Agent.Version == "" {
		cfg.Agent.Version = "0.1.0"
	}
	if cfg.React.MaxTurns == 0 {
		cfg.React.MaxTurns = 30
	}
	if cfg.React.MaxTokensPerTurn == 0 {
		cfg.React.MaxTokensPerTurn = 200000
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if len(cfg.Skills.Dirs) == 0 {
		cfg.Skills.Dirs = []string{
			"~/.config/coddy-agent/skills", // agent-specific global skills
			"~/.cursor/skills",
			"~/.cursor/skills-cursor",
		}
	}
	if cfg.Skills.InstallDir == "" {
		cfg.Skills.InstallDir = "~/.config/coddy-agent/skills"
	}
	if cfg.Models.Default == "" && len(cfg.Models.Defs) == 0 {
		// Try to build a default from environment.
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
			cfg.Models.Default = "openai/gpt-5.4"
			cfg.Models.Defs = []ModelDefinition{{
				ID:          "openai/gpt-5.4",
				Provider:    "openai",
				Model:       "gpt-5.4",
				APIKey:      key,
				MaxTokens:   16384,
				Temperature: 0.2,
			}}
		} else if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			cfg.Models.Default = "anthropic/claude-sonnet-4-6"
			cfg.Models.Defs = []ModelDefinition{{
				ID:          "anthropic/claude-sonnet-4-6",
				Provider:    "anthropic",
				Model:       "claude-sonnet-4-6",
				APIKey:      key,
				MaxTokens:   16384,
				Temperature: 0.2,
			}}
		}
	}
}

// FindModelDef returns the model definition for a given model ID.
func (c *Config) FindModelDef(id string) (*ModelDefinition, error) {
	for i := range c.Models.Defs {
		if c.Models.Defs[i].ID == id {
			return &c.Models.Defs[i], nil
		}
	}
	return nil, fmt.Errorf("model %q not found in config", id)
}

// ModelForMode returns the model ID to use for the given mode.
func (c *Config) ModelForMode(mode string) string {
	switch mode {
	case "agent":
		if c.Models.AgentMode != "" {
			return c.Models.AgentMode
		}
	case "plan":
		if c.Models.PlanMode != "" {
			return c.Models.PlanMode
		}
	}
	if c.Models.Default != "" {
		return c.Models.Default
	}
	if len(c.Models.Defs) > 0 {
		return c.Models.Defs[0].ID
	}
	return ""
}

// ExpandWorkspace resolves ${WORKSPACE} in a string to the given cwd.
func ExpandWorkspace(s, cwd string) string {
	return strings.ReplaceAll(s, "${WORKSPACE}", cwd)
}

// expandHome expands a leading ~ to the user's home directory.
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
