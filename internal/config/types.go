// Package config handles loading and validating agent configuration.
package config

// Config is the root configuration struct.
type Config struct {
	Paths Paths `yaml:"-"`

	Providers    []ProviderConfig `yaml:"providers"`
	Models       []ModelEntry     `yaml:"models"`
	Agent        Agent            `yaml:"agent"`
	Supervisor   Supervisor       `yaml:"supervisor"`
	Prompts      Prompts          `yaml:"prompts"`
	Instructions Instructions     `yaml:"instructions"`
	Skills       Skills           `yaml:"skills"`
	Rules        Rules            `yaml:"rules"`
	// MCP servers are declared in <home>/mcp.json and the project's
	// .coddy/mcp.json, never here; an old mcp_servers key is moved into
	// <home>/mcp.json on load (legacy_keys.go).
	MCP        MCP              `yaml:"mcp"`
	Tools      Tools            `yaml:"tools"`
	Subagents  Subagents        `yaml:"subagents"`
	Hooks      Hooks            `yaml:"hooks"`
	Logger     Logger           `yaml:"logger"`
	Sessions   Sessions         `yaml:"sessions"`
	Compaction Compaction       `yaml:"compaction"`
	Memory     MemoryConfig     `yaml:"memory"`
	Decisions  DecisionsConfig  `yaml:"decisions"`
	HTTPServer HTTPServerConfig `yaml:"httpserver"`
	Swarm      SwarmConfig      `yaml:"swarm"`
	UI         UIConfig         `yaml:"ui"`
	Scheduler  SchedulerConfig  `yaml:"scheduler"`
	Gateways   GatewayConfig    `yaml:"gateways"`
}
