// Package config handles loading and validating agent configuration.
package config

// Config is the root configuration struct.
type Config struct {
	Models     ModelsConfig      `yaml:"models"`
	React      ReactConfig       `yaml:"react"`
	Prompts    PromptsConfig     `yaml:"prompts"`
	Skills     SkillsConfig      `yaml:"skills"`
	MCPServers []MCPServerConfig `yaml:"mcp_servers"`
	Tools      ToolsConfig       `yaml:"tools"`
	Log        LogConfig         `yaml:"log"`
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

// PromptsConfig selects a directory of system prompt templates.
type PromptsConfig struct {
	// Dir is a directory containing agent.md and plan.md (Go text/template).
	// If empty or whitespace, embedded built-in templates are used.
	Dir string `yaml:"dir"`
}

// SkillsConfig controls where skills and rules are loaded from.
type SkillsConfig struct {
	// Dirs is the list of directories the agent scans for skills and cursor rules.
	// Searched in order - project-level dirs take priority over global ones.
	Dirs []string `yaml:"dirs"`

	// InstallDir is used by `coddy skills install` / `coddy skills uninstall`.
	// Defaults to ~/.config/coddy-agent/skills if empty.
	InstallDir string `yaml:"install_dir"`

	// ExtraFiles are specific skill files to always load regardless of Dirs.
	ExtraFiles []string `yaml:"extra_files"`
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

// LogConfig controls logging behavior.
type LogConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}
