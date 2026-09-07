package config

// MCPServerConfig defines an MCP server to connect to (YAML key mcp_servers).
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
