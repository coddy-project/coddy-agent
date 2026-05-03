package config

import "fmt"

// Defaults for the ReAct loop when YAML omits zero values.
const (
	AgentDefaultMaxTurns         = 30
	AgentDefaultMaxTokensPerTurn = 200000
)

// Agent is the YAML agent section (key agent) for ReAct loop settings.
type Agent struct {
	// Model is the models[].id used for LLM calls until the session overrides the model in the client.
	Model            string `yaml:"model"`
	MaxTurns         int    `yaml:"max_turns"`
	MaxTokensPerTurn int    `yaml:"max_tokens_per_turn"`
}

// ApplyDefaults sets MaxTurns and MaxTokensPerTurn when they are zero.
func (c *Agent) ApplyDefaults() {
	if c.MaxTurns == 0 {
		c.MaxTurns = AgentDefaultMaxTurns
	}
	if c.MaxTokensPerTurn == 0 {
		c.MaxTokensPerTurn = AgentDefaultMaxTokensPerTurn
	}
}

// Validate checks bounds after defaults.
func (c *Agent) Validate() error {
	if c.MaxTurns < 0 {
		return fmt.Errorf("agent.max_turns: must be >= 0")
	}
	if c.MaxTokensPerTurn < 0 {
		return fmt.Errorf("agent.max_tokens_per_turn: must be >= 0")
	}
	return nil
}
