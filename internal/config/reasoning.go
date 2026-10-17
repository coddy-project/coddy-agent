package config

import "strings"

// Reasoning level names exchanged between the UI, config, and providers.
// Providers map these to their own controls (OpenAI reasoning_effort, Anthropic thinking budget).
const (
	ReasoningMinimal = "minimal"
	ReasoningLow     = "low"
	ReasoningMedium  = "medium"
	ReasoningHigh    = "high"
	// ReasoningNone is the lowest tier of the Codex backend, which serves gpt-5*
	// ids but rejects the "minimal" name for the same idea.
	ReasoningNone = "none"
	// ReasoningOff is the pseudo level that turns a model's thinking off. It is
	// not a tier of any provider: each one maps it to its own switch, and it is
	// offered only where such a switch exists (ReasoningOffOffered).
	ReasoningOff = "off"
	// ReasoningDefault names "the model's default level" in a command; it is
	// never stored, choosing it clears the session's own selection.
	ReasoningDefault = "default"
)

// reasoningWithMinimal is the level set for models that support a minimal tier
// (the OpenAI gpt-5 and gpt-6 families).
var reasoningWithMinimal = []string{ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh}

// reasoningStandard is the level set for reasoning models without a minimal tier
// (OpenAI o-series, Anthropic extended-thinking models).
var reasoningStandard = []string{ReasoningLow, ReasoningMedium, ReasoningHigh}

// ResolvedReasoningLevels returns the reasoning levels offered for this model.
// An explicit ReasoningLevels (including an empty list) overrides auto-detection;
// otherwise levels are inferred from the API model id. Returns nil when the model
// has no reasoning support.
func (m *ModelEntry) ResolvedReasoningLevels() []string {
	if m.ReasoningLevels != nil {
		if len(*m.ReasoningLevels) == 0 {
			return nil
		}
		out := make([]string, len(*m.ReasoningLevels))
		copy(out, *m.ReasoningLevels)
		return out
	}
	return detectReasoningLevels(m.APIModel())
}

// DefaultReasoningLevel returns ReasoningDefault when it is one of the resolved levels, else "".
func (m *ModelEntry) DefaultReasoningLevel() string {
	d := strings.TrimSpace(m.ReasoningDefault)
	if d == "" {
		return ""
	}
	for _, lv := range m.ResolvedReasoningLevels() {
		if lv == d {
			return d
		}
	}
	return ""
}

// ReasoningLevelsFor returns the levels offered for one model entry, adjusted for
// the provider that serves it. Level detection is model-id based, which is right
// for OpenAI and Anthropic but wrong for Codex: it serves gpt-5* ids yet accepts
// only none/low/medium/high/xhigh, so "minimal" becomes "none" there.
func (c *Config) ReasoningLevelsFor(ent *ModelEntry) []string {
	if ent == nil {
		return nil
	}
	providerType := ""
	if c != nil {
		if prov := c.FindProvider(ent.ProviderName()); prov != nil {
			providerType = prov.Type
		}
	}
	return ReasoningLevelsForProviderType(ent, providerType)
}

// ReasoningLevelsForProviderType is ReasoningLevelsFor with the provider type
// supplied by the caller instead of looked up in the saved config. The settings
// form needs that while a provider row is still being edited: the type the
// operator has picked, not the one on disk, decides whether the Codex remap
// applies. An empty or non-codex type leaves the detected list untouched.
func ReasoningLevelsForProviderType(ent *ModelEntry, providerType string) []string {
	if ent == nil {
		return nil
	}
	levels := ent.ResolvedReasoningLevels()
	if len(levels) == 0 || providerType != "codex" {
		return levels
	}
	return remapMinimalToNone(levels)
}

// ReasoningOffOffered reports whether the operator enabled the off pseudo-level
// for this model entry. The setting attests that this deployment honours its
// provider-specific request; Coddy does not infer that capability from a model
// name or provider type. A model with no reasoning levels has nothing to switch,
// and a Claude model the Anthropic API serves with thinking always on
// (ReasoningAlwaysOn) never offers it, whatever the setting says: every
// surface checks a level against these choices, so "off" is refused before it
// reaches the provider.
func (c *Config) ReasoningOffOffered(ent *ModelEntry) bool {
	if ent == nil || !ent.AllowReasoningOff {
		return false
	}
	if c.ReasoningAlwaysOn(ent) {
		return false
	}
	levels := c.ReasoningLevelsFor(ent)
	return len(levels) > 0
}

// ReasoningAlwaysOn reports a model entry an anthropic provider serves whose
// thinking cannot be turned off (ClaudeThinkingFor AlwaysOn): the API refuses
// thinking.type "disabled" for it. Another provider type serving the same id
// keeps its own mapping of "off".
func (c *Config) ReasoningAlwaysOn(ent *ModelEntry) bool {
	if c == nil || ent == nil {
		return false
	}
	prov := c.FindProvider(ent.ProviderName())
	return prov != nil && prov.Type == "anthropic" && ClaudeThinkingFor(ent.APIModel()).AlwaysOn
}

// ReasoningChoicesFor returns what a session may select for this model entry:
// its levels, then "off" when the model configuration permits it.
func (c *Config) ReasoningChoicesFor(ent *ModelEntry) []string {
	levels := c.ReasoningLevelsFor(ent)
	choices := make([]string, 0, len(levels)+1)
	for _, level := range levels {
		if level != ReasoningOff {
			choices = append(choices, level)
		}
	}
	if len(choices) == 0 {
		return nil
	}
	if c.ReasoningOffOffered(ent) {
		choices = append(choices, ReasoningOff)
	}
	return choices
}

// DefaultReasoningLevelFor returns the pre-selected level for one model entry
// under the same provider-aware remap as ReasoningLevelsFor.
func (c *Config) DefaultReasoningLevelFor(ent *ModelEntry) string {
	if ent == nil {
		return ""
	}
	def := ent.DefaultReasoningLevel()
	if def == "" || c == nil || !c.providerTypeFor(ent) {
		return def
	}
	if def == ReasoningMinimal {
		return ReasoningNone
	}
	return def
}

// providerTypeFor reports whether this entry is served by a codex provider.
func (c *Config) providerTypeFor(ent *ModelEntry) bool {
	prov := c.FindProvider(ent.ProviderName())
	return prov != nil && prov.Type == "codex"
}

// remapMinimalToNone swaps the "minimal" tier for "none", keeping order and
// dropping a duplicate when both names are configured.
func remapMinimalToNone(levels []string) []string {
	out := make([]string, 0, len(levels))
	seen := make(map[string]struct{}, len(levels))
	for _, lv := range levels {
		if lv == ReasoningMinimal {
			lv = ReasoningNone
		}
		if _, dup := seen[lv]; dup {
			continue
		}
		seen[lv] = struct{}{}
		out = append(out, lv)
	}
	return out
}

// detectReasoningLevels infers reasoning levels from a provider API model id.
// The vendor an aggregator or a Hugging Face id puts in front of the model
// (qwen/qwen3.8-flash on OpenRouter or RouterAI, Qwen/Qwen3-32B on vLLM,
// openai/o3-mini) is not part of the model's name, and the levels follow the
// model behind it (issue #258).
func detectReasoningLevels(apiModel string) []string {
	id := strings.ToLower(strings.TrimSpace(apiModel))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	switch {
	case strings.HasPrefix(id, "gpt-5"), strings.HasPrefix(id, "gpt-6"):
		return append([]string(nil), reasoningWithMinimal...)
	case isOpenAIOSeries(id), strings.HasPrefix(id, "gpt-oss"):
		return append([]string(nil), reasoningStandard...)
	case isAnthropicThinking(id):
		return append([]string(nil), reasoningStandard...)
	case isQwenThinking(id):
		return append([]string(nil), reasoningStandard...)
	default:
		return nil
	}
}

// isOpenAIOSeries matches OpenAI reasoning models named o1/o3/o4 (optionally with a suffix like -mini).
func isOpenAIOSeries(id string) bool {
	for _, p := range []string{"o1", "o3", "o4"} {
		if id == p || strings.HasPrefix(id, p+"-") {
			return true
		}
	}
	return false
}

// isAnthropicThinking matches Claude families that support extended thinking.
func isAnthropicThinking(id string) bool {
	for _, p := range []string{
		"claude-opus-4", "claude-sonnet-4", "claude-haiku-4", "claude-3-7",
		"claude-opus-5", "claude-sonnet-5", "claude-haiku-5", "claude-fable-5", "claude-mythos-5",
	} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// ClaudeThinking is how an Anthropic model takes thinking on the Messages API.
// The zero value is the extended-thinking contract of Claude 3.7 to 4.6: a
// thinking.budget_tokens budget turns it on, no thinking field leaves it off.
type ClaudeThinking struct {
	// Adaptive models refuse thinking.type "enabled" with a budget (HTTP 400):
	// thinking is {type: "adaptive"} and output_config.effort sets its depth.
	Adaptive bool
	// OnByDefault models think when the request names no thinking at all.
	OnByDefault bool
	// AlwaysOn models refuse thinking.type "disabled" (HTTP 400), so their
	// thinking cannot be turned off.
	AlwaysOn bool
}

// ClaudeThinkingFor returns the thinking contract of an Anthropic API model id
// (claude-opus-5-5, or a gateway's anthropic/claude-opus-5-5). Ids it does not
// know, older Claude models and non-Claude models get the zero value.
func ClaudeThinkingFor(apiModel string) ClaudeThinking {
	id := strings.ToLower(strings.TrimSpace(apiModel))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	hasPrefix := func(prefixes ...string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(id, p) {
				return true
			}
		}
		return false
	}
	switch {
	// Thinking is always on: "disabled" is refused at every effort level.
	case hasPrefix("claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5", "claude-mythos-5"):
		return ClaudeThinking{Adaptive: true, OnByDefault: true, AlwaysOn: true}
	// On by default, "disabled" accepted (Opus 5 and Haiku 5.5 up to effort high).
	case hasPrefix("claude-opus-5", "claude-sonnet-5", "claude-haiku-5"):
		return ClaudeThinking{Adaptive: true, OnByDefault: true}
	// Off unless the request turns adaptive thinking on.
	case hasPrefix("claude-opus-4-7", "claude-opus-4-8"):
		return ClaudeThinking{Adaptive: true}
	default:
		return ClaudeThinking{}
	}
}

// isQwenThinking matches Qwen3-family hybrid thinking models (qwen3, qwen3.5,
// qwen3.6, qwen3.8, qwen3-vl, ...). Qwen2.5 has no thinking mode.
func isQwenThinking(id string) bool {
	return strings.HasPrefix(id, "qwen3")
}
