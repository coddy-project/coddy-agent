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

// ReasoningOffOffered reports whether thinking can really be turned off for
// this model entry, so "off" may be offered next to its levels.
//
// Off is only honest where the provider has a switch for it: the chat-template
// flag of Qwen3 on an OpenAI-compatible server, Anthropic's thinking block, the
// Codex backend's "none" tier, and any model whose configured levels include
// "none". A gpt-5 model's "minimal" still reasons, and the o-series and
// gpt-oss have no switch at all, so off is not offered there. A model with no
// levels (none detected, or reasoning_levels: []) has nothing to switch.
func (c *Config) ReasoningOffOffered(ent *ModelEntry) bool {
	levels := c.ReasoningLevelsFor(ent)
	if len(levels) == 0 {
		return false
	}
	for _, lv := range levels {
		if lv == ReasoningNone {
			return true
		}
	}
	providerType := ""
	if c != nil {
		if prov := c.FindProvider(ent.ProviderName()); prov != nil {
			providerType = prov.Type
		}
	}
	switch providerType {
	case "anthropic", "codex":
		return true
	case "openai", "neuraldeep":
		return isQwenThinking(strings.ToLower(strings.TrimSpace(ent.APIModel())))
	default:
		return false
	}
}

// ReasoningChoicesFor returns what a session may select for this model entry:
// its levels, then "off" when the provider can turn thinking off.
func (c *Config) ReasoningChoicesFor(ent *ModelEntry) []string {
	levels := c.ReasoningLevelsFor(ent)
	if len(levels) == 0 {
		return nil
	}
	if c.ReasoningOffOffered(ent) {
		levels = append(levels, ReasoningOff)
	}
	return levels
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
func detectReasoningLevels(apiModel string) []string {
	id := strings.ToLower(strings.TrimSpace(apiModel))
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
		"claude-opus-5", "claude-sonnet-5", "claude-haiku-5", "claude-fable-5",
	} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// isQwenThinking matches Qwen3-family hybrid thinking models (qwen3, qwen3.5,
// qwen3.6, qwen3.8, qwen3-vl, ...). Qwen2.5 has no thinking mode.
func isQwenThinking(id string) bool {
	return strings.HasPrefix(id, "qwen3")
}
