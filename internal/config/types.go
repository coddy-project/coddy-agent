// Package config handles loading and validating agent configuration.
package config

import (
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/prompts"
)

// Config is the root configuration struct.
type Config struct {
	// Paths is set by LoadFromCLI / LoadWithPaths from CODDY_HOME, CODDY_CWD, and config path resolution.
	Paths Paths `yaml:"-"`

	Models     ModelsConfig      `yaml:"models"`
	React      ReactConfig       `yaml:"react"`
	Prompts    prompts.Config    `yaml:"prompts"`
	Skills     SkillsConfig      `yaml:"skills"`
	MCPServers []MCPServerConfig `yaml:"mcp_servers"`
	Tools      ToolsConfig       `yaml:"tools"`
	Logger     logger.Config `yaml:"logger"`

	// SessionsDir overrides the directory for persisted session bundles. Empty means <Paths.Home>/sessions.
	SessionsDir string `yaml:"sessions_dir"`
}

// ModelsConfig defines model selection and definitions.
type ModelsConfig struct {
	Default   string            `yaml:"default"`
	AgentMode string            `yaml:"agent_mode"`
	PlanMode  string            `yaml:"plan_mode"`
	Defs      []ModelDefinition `yaml:"definitions"`
}

// ModelDefinition describes a single LLM model configuration.
type ModelDefinition struct {
	ID          string  `yaml:"id"`
	Provider    string  `yaml:"provider"`
	Model       string  `yaml:"model"`
	APIKey      string  `yaml:"api_key"`
	BaseURL     string  `yaml:"base_url"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
}

// ReactConfig controls ReAct loop behavior.
type ReactConfig struct {
	MaxTurns         int `yaml:"max_turns"`
	MaxTokensPerTurn int `yaml:"max_tokens_per_turn"`
}

// SkillsConfig controls where skills and rules are loaded from.
type SkillsConfig struct {
	// Dirs lists directories scanned for SKILL.md and markdown rules. Order is search order.
	Dirs []string `yaml:"dirs"`

	// InstallDir is used by `coddy skills install` / `coddy skills uninstall`.
	// Defaults to $CODDY_HOME/skills when empty.
	InstallDir string `yaml:"install_dir"`
}

// MCPServerConfig defines an MCP server to connect to.
type MCPServerConfig struct {
	Type    string             `yaml:"type"`
	Name    string             `yaml:"name"`
	Command string             `yaml:"command"`
	Args    []string           `yaml:"args"`
	Env     []EnvVarConfig     `yaml:"env"`
	URL     string             `yaml:"url"`
	Headers []HTTPHeaderConfig `yaml:"headers"`
}

// EnvVarConfig is a name-value environment variable.
type EnvVarConfig struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// HTTPHeaderConfig is a name-value HTTP header.
type HTTPHeaderConfig struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// ToolsConfig controls tool behavior.
type ToolsConfig struct {
	RequirePermissionForCommands bool `yaml:"require_permission_for_commands"`
	RequirePermissionForWrites   bool `yaml:"require_permission_for_writes"`
	RestrictToCWD                bool `yaml:"restrict_to_cwd"`

	// CommandAllowlist is a list of shell command prefixes or exact commands
	// that are always allowed without asking the user for permission.
	// Supports exact match and prefix match (e.g. "go test" matches "go test ./...").
	// Examples: ["go build", "go test", "make", "npm run", "git status"]
	CommandAllowlist []string `yaml:"command_allowlist"`
}
