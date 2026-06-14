package config

import "strings"

// Reasoning level names exchanged between the UI, config, and providers.
// Providers map these to their own controls (OpenAI reasoning_effort, Anthropic thinking budget).
const (
	ReasoningMinimal = "minimal"
	ReasoningLow     = "low"
	ReasoningMedium  = "medium"
	ReasoningHigh    = "high"
)

// reasoningWithMinimal is the level set for models that support a minimal tier (OpenAI gpt-5 family).
var reasoningWithMinimal = []string{ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh}

// reasoningStandard is the level set for reasoning models without a minimal tier
// (OpenAI o-series, Anthropic extended-thinking models).
var reasoningStandard = []string{ReasoningLow, ReasoningMedium, ReasoningHigh}

// ResolvedReasoningLevels returns the reasoning levels offered for this model.
// An explicit ReasoningLevels (including an empty slice) overrides auto-detection;
// otherwise levels are inferred from the API model id. Returns nil when the model
// has no reasoning support.
func (m *ModelEntry) ResolvedReasoningLevels() []string {
	if m.ReasoningLevels != nil {
		if len(m.ReasoningLevels) == 0 {
			return nil
		}
		out := make([]string, len(m.ReasoningLevels))
		copy(out, m.ReasoningLevels)
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

// detectReasoningLevels infers reasoning levels from a provider API model id.
func detectReasoningLevels(apiModel string) []string {
	id := strings.ToLower(strings.TrimSpace(apiModel))
	switch {
	case strings.HasPrefix(id, "gpt-5"):
		return append([]string(nil), reasoningWithMinimal...)
	case isOpenAIOSeries(id):
		return append([]string(nil), reasoningStandard...)
	case isAnthropicThinking(id):
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
	for _, p := range []string{"claude-opus-4", "claude-sonnet-4", "claude-haiku-4", "claude-3-7"} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}
