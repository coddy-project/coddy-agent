package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/logger"
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
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	p := cfg.Paths

	if cfg.React.MaxTurns == 0 {
		cfg.React.MaxTurns = 30
	}
	if cfg.React.MaxTokensPerTurn == 0 {
		cfg.React.MaxTokensPerTurn = 200000
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = logger.LevelInfo
	}
	// Legacy: log.file without outputs used to be stored but unused; route to stderr + file.
	if strings.TrimSpace(cfg.Log.File) != "" && len(cfg.Log.Outputs) == 0 {
		cfg.Log.Outputs = []string{logger.OutputStderr, logger.OutputFile}
	}

	if strings.TrimSpace(cfg.SessionsDir) != "" {
		cfg.SessionsDir = filepath.Clean(ExpandCODDYHomeOnly(cfg.SessionsDir, p))
	}

	if cfg.Skills.InstallDir == "" {
		if p.Home != "" {
			cfg.Skills.InstallDir = filepath.Join(p.Home, "skills")
		} else {
			cfg.Skills.InstallDir = expandHome("~/.coddy/skills")
		}
	} else {
		cfg.Skills.InstallDir = filepath.Clean(ExpandCODDYHomeOnly(cfg.Skills.InstallDir, p))
	}

	if len(cfg.Skills.Dirs) == 0 {
		cfg.Skills.Dirs = []string{
			"${CODDY_HOME}/skills",
			"${CWD}/.skills",
			"~/.cursor/skills",
			"~/.claude/skills",
		}
	} else {
		for i := range cfg.Skills.Dirs {
			cfg.Skills.Dirs[i] = ExpandCODDYHomeOnly(cfg.Skills.Dirs[i], p)
		}
	}

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
	if strings.TrimSpace(c.SessionsDir) != "" {
		return filepath.Clean(c.SessionsDir)
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

// ResolvedPromptsDir returns the prompts directory with ~ and ${CWD} expanded for the given session cwd.
// Empty config Dir means callers should pass "" to prompts.Render (embedded defaults).
func (p PromptsConfig) ResolvedPromptsDir(sessionCWD string) string {
	d := strings.TrimSpace(p.Dir)
	if d == "" {
		return ""
	}
	return filepath.Clean(ExpandCWD(d, sessionCWD))
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
