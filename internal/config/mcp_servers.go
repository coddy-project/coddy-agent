package config

// MCPServerConfig is one MCP server declaration as the runtime holds it: an
// entry of <home>/mcp.json or the project's .coddy/mcp.json, or one an ACP
// client sent. The yaml tags read the old mcp_servers key of config.yaml when
// it is moved into <home>/mcp.json (legacy_keys.go).
type MCPServerConfig struct {
	Type    string             `yaml:"type"`
	Name    string             `yaml:"name"`
	Command string             `yaml:"command"`
	Args    []string           `yaml:"args"`
	Env     []EnvVarConfig     `yaml:"env"`
	URL     string             `yaml:"url"`
	Headers []HTTPHeaderConfig `yaml:"headers"`
	// Disabled skips connecting this server without removing its definition.
	Disabled bool `yaml:"disabled"`
	// DisabledTools hides individual tools of this server from the agent.
	DisabledTools []string `yaml:"disabled_tools"`
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
