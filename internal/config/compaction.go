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

// Defaults for the compaction.result_eviction subsection.
const (
	// ResultEvictionDefaultKeepRecent is how many most recent evictable tool
	// results (read pages, grep dumps) stay intact as a working window.
	// 2, not 1: a window of 1 cannot hold a read plus a grep at the same time, so
	// a model comparing the two keeps re-fetching whichever the other just evicted
	// (the loop guard does not catch it, because alternating calls are not
	// identical). 2 keeps the common working pair live for one extra result.
	ResultEvictionDefaultKeepRecent = 2
	// ResultEvictionDefaultMinResultBytes is the size at or below which a tool
	// result is never evicted (too small to be worth a placeholder).
	ResultEvictionDefaultMinResultBytes = 2000
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
	// ResultEviction controls pruning of superseded read/grep tool results from
	// the LLM projection (the persisted transcript is never rewritten).
	ResultEviction ResultEviction `yaml:"result_eviction"`
}

// ResultEviction is the YAML compaction.result_eviction section: collapsing
// unmarked read/grep results to short placeholders when building the LLM request,
// so paging a large file or a wide search cannot pin dead lines in every later turn.
type ResultEviction struct {
	// Enabled toggles the projection. A nil pointer means the default (true).
	Enabled *bool `yaml:"enabled"`
	// KeepRecent is how many most recent evictable results stay intact as a
	// working window. A nil pointer means the default (1); 0 keeps none.
	KeepRecent *int `yaml:"keep_recent"`
	// MinResultBytes is the size at or below which a result is never evicted.
	// A nil pointer means the default (2000); 0 makes every result a candidate.
	MinResultBytes *int `yaml:"min_result_bytes"`
}

// IsEnabled reports whether result eviction is active. Defaults to true when unset.
func (r *ResultEviction) IsEnabled() bool {
	return r.Enabled == nil || *r.Enabled
}

// EffectiveKeepRecent returns keep_recent with the default applied.
func (r *ResultEviction) EffectiveKeepRecent() int {
	if r.KeepRecent == nil {
		return ResultEvictionDefaultKeepRecent
	}
	return *r.KeepRecent
}

// EffectiveMinResultBytes returns min_result_bytes with the default applied.
func (r *ResultEviction) EffectiveMinResultBytes() int {
	if r.MinResultBytes == nil {
		return ResultEvictionDefaultMinResultBytes
	}
	return *r.MinResultBytes
}

// Validate checks bounds on explicitly set fields.
func (r *ResultEviction) Validate() error {
	if r.KeepRecent != nil && *r.KeepRecent < 0 {
		return fmt.Errorf("compaction.result_eviction.keep_recent: must be >= 0")
	}
	if r.MinResultBytes != nil && *r.MinResultBytes < 0 {
		return fmt.Errorf("compaction.result_eviction.min_result_bytes: must be >= 0")
	}
	return nil
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
	if err := c.ResultEviction.Validate(); err != nil {
		return err
	}
	return nil
}
