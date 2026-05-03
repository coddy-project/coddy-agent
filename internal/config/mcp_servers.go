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
