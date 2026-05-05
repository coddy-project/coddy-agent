// Package config handles loading and validating agent configuration.
package config

// Config is the root configuration struct.
type Config struct {
	Paths Paths `yaml:"-"`

	Providers  []ProviderConfig  `yaml:"providers"`
	Models     []ModelEntry      `yaml:"models"`
	Agent      Agent             `yaml:"agent"`
	Prompts    Prompts           `yaml:"prompts"`
	Skills     Skills            `yaml:"skills"`
	MCPServers []MCPServerConfig `yaml:"mcp_servers"`
	Tools      Tools             `yaml:"tools"`
	Logger     Logger            `yaml:"logger"`
	Sessions   Sessions          `yaml:"sessions"`
	Memory     MemoryConfig      `yaml:"memory"`
	HTTPServer HTTPServerConfig  `yaml:"httpserver"`
}
