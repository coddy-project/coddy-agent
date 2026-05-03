package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultSearchPaths are legacy fallbacks when <Home>/config.yaml is missing.
var DefaultSearchPaths = []string{
	"~/.coddy/config.yaml",
	"~/.config/coddy-agent/config.yaml",
	"./config.yaml",
}

// LoadFromCLI resolves paths, searches legacy config locations when needed, and loads YAML.
func LoadFromCLI(cli CLIPaths) (*Config, error) {
	paths, err := Resolve(cli)
	if err != nil {
		return nil, err
	}
	explicitConfig := strings.TrimSpace(cli.Config) != ""
	if !explicitConfig {
		if _, err := os.Stat(paths.ConfigPath); errors.Is(err, os.ErrNotExist) {
			for _, try := range DefaultSearchPaths {
				candidate := filepath.Clean(ExpandPathVars(strings.TrimSpace(try), paths))
				if _, err := os.Stat(candidate); err == nil {
					paths.ConfigPath = candidate
					break
				}
			}
		}
	}
	return readConfigFile(paths, explicitConfig)
}

// Load reads config from the given path, or searches default locations.
// If path is non-empty, that file must exist. If path is empty, resolution uses env and ~/.coddy/config.yaml.
func Load(path string) (*Config, error) {
	return LoadFromCLI(CLIPaths{Config: strings.TrimSpace(path)})
}

// LoadWithPaths loads YAML from paths.ConfigPath (explicit path semantics: file must exist).
func LoadWithPaths(paths Paths) (*Config, error) {
	return readConfigFile(paths, true)
}

func readConfigFile(paths Paths, explicitFile bool) (*Config, error) {
	data, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		if explicitFile {
			return nil, fmt.Errorf("read config %s: %w", paths.ConfigPath, err)
		}
		if errors.Is(err, os.ErrNotExist) {
			cfg := &Config{Paths: paths}
			applyDefaults(cfg)
			if err := validateSubconfigs(cfg); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("read config %s: %w", paths.ConfigPath, err)
	}

	expanded := os.ExpandEnv(ExpandPathVars(string(data), paths))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", paths.ConfigPath, err)
	}
	cfg.Paths = paths

	applyDefaults(&cfg)
	if err := validateSubconfigs(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validateSubconfigs(cfg *Config) error {
	if err := cfg.Logger.Validate(); err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	if err := cfg.Prompts.Validate(); err != nil {
		return fmt.Errorf("prompts: %w", err)
	}
	if err := cfg.React.Validate(); err != nil {
		return fmt.Errorf("react: %w", err)
	}
	if err := cfg.Skills.Validate(); err != nil {
		return fmt.Errorf("skills: %w", err)
	}
	if err := cfg.Tools.Validate(); err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	if err := cfg.Sessions.Validate(); err != nil {
		return fmt.Errorf("sessions: %w", err)
	}
	return nil
}

func applyDefaults(cfg *Config) {
	p := cfg.Paths

	cfg.React.ApplyDefaults()
	if cfg.Logger.Level == "" {
		cfg.Logger.Level = LogLevelInfo
	}
	// Legacy: logger.file without outputs used to be stored but unused; route to stderr + file.
	if strings.TrimSpace(cfg.Logger.File) != "" && len(cfg.Logger.Outputs) == 0 {
		cfg.Logger.Outputs = []string{LogOutputStderr, LogOutputFile}
	}

	if d := strings.TrimSpace(cfg.Sessions.Dir); d != "" {
		cfg.Sessions.Dir = filepath.Clean(ExpandCODDYHomeOnly(d, p))
	} else {
		cfg.Sessions.Dir = ""
	}

	cfg.Skills.ApplyDefaults(p.Home, func(s string) string {
		return ExpandCODDYHomeOnly(s, p)
	})

	if cfg.Models.Default == "" && len(cfg.Models.Defs) == 0 {
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

// ResolvedSessionsRoot returns the filesystem root for persisted sessions.
func (c *Config) ResolvedSessionsRoot() string {
	if d := strings.TrimSpace(c.Sessions.Dir); d != "" {
		return filepath.Clean(d)
	}
	if c.Paths.Home != "" {
		return filepath.Join(c.Paths.Home, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".coddy", "sessions")
	}
	return filepath.Join(home, ".coddy", "sessions")
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

// ExpandCWD replaces ${CWD} in s, then expands ~, using the given session or process cwd.
func ExpandCWD(s, cwd string) string {
	s = strings.ReplaceAll(s, "${CWD}", cwd)
	return expandHome(s)
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
