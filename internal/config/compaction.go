package config

import (
	"fmt"
	"slices"
	"strings"
)

// Defaults for the compaction section when YAML omits values.
const (
	// CompactionDefaultThresholdPercent triggers auto-compaction when the estimated
	// context reaches this percent of the model's context window.
	CompactionDefaultThresholdPercent = 80
	// CompactionDefaultKeepRecentTurns is how many recent user turns stay verbatim.
	CompactionDefaultKeepRecentTurns = 2
)

// Defaults for the compaction.result_eviction subsection.
const (
	// ResultEvictionDefaultKeepRecent is how many most recent evictable read and
	// grep results (read pages, grep dumps) stay intact as a working window.
	// 2, not 1: a window of 1 cannot hold a read plus a grep at the same time, so
	// a model comparing the two keeps re-fetching whichever the other just evicted
	// (the loop guard does not catch it, because alternating calls are not
	// identical). 2 keeps the common working pair live for one extra result.
	ResultEvictionDefaultKeepRecent = 2
	// ResultEvictionDefaultMinResultBytes is the size at or below which a tool
	// result is never evicted (too small to be worth a placeholder).
	ResultEvictionDefaultMinResultBytes = 2000
	// ResultEvictionDefaultKeepRecentSteps is how many of the most recent steps
	// that hold a listing result (glob, print_tree, websearch, webfetch) keep
	// all of those results intact. A step is one assistant message with every
	// result of its tool calls, so ten parallel calls are one step: counting
	// single results would let one fan-out fill the whole window by itself.
	ResultEvictionDefaultKeepRecentSteps = 3
	// ResultEvictionDefaultStartPercent is the share of the model's context
	// window the conversation must reach before eviction starts rewriting it.
	// Below it the history is sent untouched, because every placeholder that
	// appears mid-history is a byte the provider's prompt cache keyed the rest
	// of the conversation on: a sliding window would reprocess the whole
	// transcript on almost every step to save a few thousand tokens nobody was
	// short of yet.
	ResultEvictionDefaultStartPercent = 50
)

// Defaults for the compaction.in_turn subsection.
const (
	// InTurnDefaultKeepRecentSteps is the most steps of the turn being answered
	// that an in-turn fold leaves verbatim. It is a cap, not a promise: the fold
	// keeps fewer when the kept steps would not leave the next request room under
	// the threshold, and always folds at least the steps before the latest one.
	InTurnDefaultKeepRecentSteps = 4
)

// ResultEvictionListingTools is the closed set of read-only listing tools that
// compaction.result_eviction.tools may name, in the order the default list
// carries them. Their result can be asked for again by repeating the call and
// says nothing the call does not. run_command and MCP tools are deliberately
// not here: a shell command or a remote server may not give the same answer
// twice, or may have changed something by running, so a placeholder would
// withhold what cannot be fetched again. Callers must not modify the slice.
var ResultEvictionListingTools = []string{"glob", "print_tree", "websearch", "webfetch"}

// Compaction is the YAML compaction section (key compaction): summarizing older
// conversation history so long sessions keep fitting the model context window.
type Compaction struct {
	// Enabled toggles compaction (the manual command and the automatic trigger).
	// A nil pointer means the default (true).
	Enabled *bool `yaml:"enable"`
	// AutoEnabled turns off only the threshold trigger; manual compaction stays available.
	AutoEnabled *bool `yaml:"auto_enable"`
	// ThresholdPercent fires auto-compaction when the estimated context usage
	// reaches this percent of the effective model's context window (default
	// 80, valid 1..100): its max_context_tokens, else the window its
	// provider's model listing reports, else DefaultContextWindowTokens.
	ThresholdPercent int `yaml:"threshold_percent"`
	// KeepRecentTurns is how many most recent user turns (each with the agent
	// replies and tool activity after it) stay verbatim; only history before
	// that boundary is summarized. A nil pointer means the default (2); an
	// explicit 0 summarizes the whole window. When the window holds no more
	// user turns than this, a compaction keeps fewer: the automatic trigger
	// down to the prompt being answered, the manual command down to none.
	KeepRecentTurns *int `yaml:"keep_recent_turns"`
	// Model optionally selects the models[].model used for the summarization
	// call. Empty means the session's effective model.
	Model string `yaml:"model"`
	// FallbackModels are the models[].model ids tried, in order, when the
	// summarizer above them fails. A compaction is what a session out of room
	// has left, so one unreachable or overloaded model must not be the end of
	// it; the session's own model is always the last resort, whether or not it
	// is listed here (issue #247).
	FallbackModels []string `yaml:"fallback_models"`
	// InTurn controls the fold of the turn being answered: the automatic
	// trigger's fallback when there is no earlier turn to fold, and the
	// recovery from a request the provider refused as too large.
	InTurn InTurn `yaml:"in_turn"`
	// ResultEviction controls pruning of superseded tool results (read pages,
	// grep dumps, and the listings of glob, print_tree, websearch and webfetch)
	// from the LLM projection (the persisted transcript is never rewritten).
	ResultEviction ResultEviction `yaml:"result_eviction"`
}

// InTurn is the YAML compaction.in_turn section: folding the earlier steps of
// the turn being answered. The automatic trigger folds earlier user turns; when
// the window holds only the turn in progress it has nothing of that kind to fold,
// and a long turn (one prompt, dozens of tool steps) grows past the model's
// window. With the section on, the trigger then folds the turn's own earlier
// steps into a summary row that starts with the prompt itself, and a request the
// provider refuses as larger than its window is compacted that way once and sent
// again.
type InTurn struct {
	// Enabled toggles both. A nil pointer means the default (true); false
	// restores the behaviour before the section existed.
	Enabled *bool `yaml:"enable"`
	// KeepRecentSteps caps how many of the latest steps of the turn (an
	// assistant message with the tool results that follow it) the fold leaves
	// verbatim.
	// A nil pointer means the default (4); valid from 1, so the latest step
	// always stays. The fold keeps fewer when the kept steps would not leave
	// the next request room under the threshold.
	KeepRecentSteps *int `yaml:"keep_recent_steps"`
}

// IsEnabled reports whether the in-turn fold and the overflow recovery are
// active. Defaults to true when unset.
func (i *InTurn) IsEnabled() bool {
	return i.Enabled == nil || *i.Enabled
}

// EffectiveKeepRecentSteps returns keep_recent_steps with the default applied.
func (i *InTurn) EffectiveKeepRecentSteps() int {
	if i.KeepRecentSteps == nil {
		return InTurnDefaultKeepRecentSteps
	}
	return *i.KeepRecentSteps
}

// Validate checks bounds on explicitly set fields.
func (i *InTurn) Validate() error {
	if i.KeepRecentSteps != nil && *i.KeepRecentSteps < 1 {
		return fmt.Errorf("compaction.in_turn.keep_recent_steps: must be >= 1")
	}
	return nil
}

// ResultEviction is the YAML compaction.result_eviction section: collapsing
// superseded tool results to short placeholders when building the LLM request,
// so paging a large file, a wide search or a fan-out of directory listings cannot
// pin dead lines in every later turn. Unmarked read and grep results go by a
// window of results (KeepRecent); the listing tools in Tools go by a window of
// whole steps (KeepRecentSteps).
type ResultEviction struct {
	// Enabled toggles the projection. A nil pointer means the default (true).
	Enabled *bool `yaml:"enable"`
	// KeepRecent is how many most recent evictable read and grep results stay
	// intact as a working window. A nil pointer means the default (2); 0 keeps
	// none. Listing results do not count toward it.
	KeepRecent *int `yaml:"keep_recent"`
	// Tools names the read-only listing tools whose results are evicted besides
	// read and grep; the allowed names are ResultEvictionListingTools. A nil
	// pointer (key omitted) means all of them; a pointer to an empty list (an
	// explicit []) means none, so only read and grep are evicted. The pointer
	// keeps those two apart through a settings round trip, as
	// ModelEntry.ReasoningLevels does: a plain slice would write an empty list
	// for every default configuration, or lose the opt-out.
	Tools *[]string `yaml:"tools,omitempty"`
	// KeepRecentSteps is how many of the most recent steps holding a listing
	// result keep all of their listing results intact. A nil pointer means the
	// default (3); 0 keeps none.
	KeepRecentSteps *int `yaml:"keep_recent_steps"`
	// MinResultBytes is the size at or below which a result is never evicted.
	// A nil pointer means the default (2000); 0 makes every result a candidate.
	MinResultBytes *int `yaml:"min_result_bytes"`
	// StartPercent is the share of the effective model's max_context_tokens the
	// estimated context must reach before eviction starts (default 50, valid
	// 0..100). 0 evicts from the first result, which is what the projection did
	// before prompt caching was accounted for. A model without
	// max_context_tokens cannot be measured and evicts from the start.
	StartPercent *int `yaml:"start_percent"`
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

// EffectiveStartPercent returns start_percent with the default applied.
func (r *ResultEviction) EffectiveStartPercent() int {
	if r.StartPercent == nil {
		return ResultEvictionDefaultStartPercent
	}
	return *r.StartPercent
}

// EffectiveTools returns the listing tools to evict with the default applied.
// The slice is a copy: callers may sort or trim it without touching the default
// or the configured list.
func (r *ResultEviction) EffectiveTools() []string {
	if r.Tools == nil {
		return append([]string(nil), ResultEvictionListingTools...)
	}
	return append([]string(nil), (*r.Tools)...)
}

// EffectiveKeepRecentSteps returns keep_recent_steps with the default applied.
func (r *ResultEviction) EffectiveKeepRecentSteps() int {
	if r.KeepRecentSteps == nil {
		return ResultEvictionDefaultKeepRecentSteps
	}
	return *r.KeepRecentSteps
}

// Validate checks bounds on explicitly set fields.
func (r *ResultEviction) Validate() error {
	if r.KeepRecent != nil && *r.KeepRecent < 0 {
		return fmt.Errorf("compaction.result_eviction.keep_recent: must be >= 0")
	}
	if r.MinResultBytes != nil && *r.MinResultBytes < 0 {
		return fmt.Errorf("compaction.result_eviction.min_result_bytes: must be >= 0")
	}
	if r.StartPercent != nil && (*r.StartPercent < 0 || *r.StartPercent > 100) {
		return fmt.Errorf("compaction.result_eviction.start_percent: must be between 0 and 100")
	}
	if r.KeepRecentSteps != nil && *r.KeepRecentSteps < 0 {
		return fmt.Errorf("compaction.result_eviction.keep_recent_steps: must be >= 0")
	}
	if r.Tools != nil {
		for _, name := range *r.Tools {
			if !slices.Contains(ResultEvictionListingTools, name) {
				return fmt.Errorf("compaction.result_eviction.tools: unknown tool %q (allowed: %s)",
					name, strings.Join(ResultEvictionListingTools, ", "))
			}
		}
	}
	return nil
}

// IsEnabled reports whether compaction is active. Defaults to true when unset.
func (c *Compaction) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// IsAutoEnabled reports whether the automatic trigger may run.
func (c *Compaction) IsAutoEnabled() bool {
	return c.IsEnabled() && (c.AutoEnabled == nil || *c.AutoEnabled)
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
	if err := c.InTurn.Validate(); err != nil {
		return err
	}
	if err := c.ResultEviction.Validate(); err != nil {
		return err
	}
	return nil
}
