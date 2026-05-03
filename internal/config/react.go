package config

import "fmt"

// Defaults for the ReAct loop when YAML omits zero values.
const (
	ReactDefaultMaxTurns         = 30
	ReactDefaultMaxTokensPerTurn = 200000
)

// React is the YAML react section (key react).
type React struct {
	MaxTurns         int `yaml:"max_turns"`
	MaxTokensPerTurn int `yaml:"max_tokens_per_turn"`
}

// ApplyDefaults sets MaxTurns and MaxTokensPerTurn when they are zero.
func (c *React) ApplyDefaults() {
	if c.MaxTurns == 0 {
		c.MaxTurns = ReactDefaultMaxTurns
	}
	if c.MaxTokensPerTurn == 0 {
		c.MaxTokensPerTurn = ReactDefaultMaxTokensPerTurn
	}
}

// Validate checks bounds after defaults.
func (c *React) Validate() error {
	if c.MaxTurns < 0 {
		return fmt.Errorf("react.max_turns: must be >= 0")
	}
	if c.MaxTokensPerTurn < 0 {
		return fmt.Errorf("react.max_tokens_per_turn: must be >= 0")
	}
	return nil
}
