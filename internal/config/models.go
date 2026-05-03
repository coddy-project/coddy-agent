package config

// ModelsConfig defines model selection and definitions (YAML key models).
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
