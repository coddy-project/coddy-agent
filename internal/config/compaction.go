package config

import (
	"fmt"
	"strings"
)

// Defaults for the compaction section when YAML omits values.
const (
	// CompactionDefaultThresholdPercent triggers auto-compaction when the estimated
	// context reaches this percent of the model's max_context_tokens.
	CompactionDefaultThresholdPercent = 80
	// CompactionDefaultKeepRecentTurns is how many recent user turns stay verbatim.
	CompactionDefaultKeepRecentTurns = 2
)

// Compaction is the YAML compaction section (key compaction): summarizing older
// conversation history so long sessions keep fitting the model context window.
type Compaction struct {
	// Enabled toggles compaction (the manual command and the automatic trigger).
	// A nil pointer means the default (true).
	Enabled *bool `yaml:"enabled"`
	// ThresholdPercent fires auto-compaction when the estimated context usage
	// reaches this percent of the effective model's max_context_tokens
	// (default 80, valid 1..100). Models without max_context_tokens skip
	// auto-compaction; the manual command still works.
	ThresholdPercent int `yaml:"threshold_percent"`
	// KeepRecentTurns is how many most recent user turns (each with the agent
	// replies and tool activity after it) stay verbatim; only history before
	// that boundary is summarized. A nil pointer means the default (2); an
	// explicit 0 summarizes the whole window.
	KeepRecentTurns *int `yaml:"keep_recent_turns"`
	// Model optionally selects the models[].model used for the summarization
	// call. Empty means the session's effective model.
	Model string `yaml:"model"`
}

// IsEnabled reports whether compaction is active. Defaults to true when unset.
func (c *Compaction) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// EffectiveThresholdPercent returns threshold_percent with the default applied
// (covers configs constructed without ApplyDefaults).
func (c *Compaction) EffectiveThresholdPercent() int {
	if c.ThresholdPercent <= 0 {
		return CompactionDefaultThresholdPercent
	}
	return c.ThresholdPercent
}

// EffectiveKeepRecentTurns returns keep_recent_turns with the default applied.
func (c *Compaction) EffectiveKeepRecentTurns() int {
	if c.KeepRecentTurns == nil {
		return CompactionDefaultKeepRecentTurns
	}
	return *c.KeepRecentTurns
}

// Normalize trims string fields in place.
func (c *Compaction) Normalize() {
	c.Model = strings.TrimSpace(c.Model)
}

// ApplyDefaults sets ThresholdPercent when it is zero.
func (c *Compaction) ApplyDefaults() {
	if c.ThresholdPercent == 0 {
		c.ThresholdPercent = CompactionDefaultThresholdPercent
	}
}

// Validate checks bounds after defaults.
func (c *Compaction) Validate() error {
	if c.ThresholdPercent < 1 || c.ThresholdPercent > 100 {
		return fmt.Errorf("compaction.threshold_percent: must be within 1..100")
	}
	if c.KeepRecentTurns != nil && *c.KeepRecentTurns < 0 {
		return fmt.Errorf("compaction.keep_recent_turns: must be >= 0")
	}
	return nil
}
