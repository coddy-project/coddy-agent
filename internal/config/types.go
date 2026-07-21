// Package config handles loading and validating agent configuration.
package config

// Config is the root configuration struct.
type Config struct {
	Paths Paths `yaml:"-"`

	Providers    []ProviderConfig  `yaml:"providers"`
	Models       []ModelEntry      `yaml:"models"`
	Agent        Agent             `yaml:"agent"`
	Prompts      Prompts           `yaml:"prompts"`
	Instructions Instructions      `yaml:"instructions"`
	Skills       Skills            `yaml:"skills"`
	Rules        Rules             `yaml:"rules"`
	MCPServers   []MCPServerConfig `yaml:"mcp_servers"`
	Tools        Tools             `yaml:"tools"`
	Logger       Logger            `yaml:"logger"`
	Sessions     Sessions          `yaml:"sessions"`
	Compaction   Compaction        `yaml:"compaction"`
	Memory       MemoryConfig      `yaml:"memory"`
	HTTPServer   HTTPServerConfig  `yaml:"httpserver"`
	UI           UIConfig          `yaml:"ui"`
	Scheduler    SchedulerConfig   `yaml:"scheduler"`
	Gateways     GatewayConfig     `yaml:"gateways"`
}
