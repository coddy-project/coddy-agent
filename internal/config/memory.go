package config

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Defaults of the memory subagent knobs that have one.
const (
	// MemoryDefaultWaitSeconds is how long a user turn waits for the memory
	// subagent's report before its first model call.
	MemoryDefaultWaitSeconds = 20
	// MemoryDefaultTimeoutSeconds is the hard limit of one memory run.
	MemoryDefaultTimeoutSeconds = 300
	// MemoryDefaultKeepRuns is how many finished memory runs a session keeps.
	MemoryDefaultKeepRuns = 20
)

// MemoryConfig controls the optional long-term memory subagent (implementation in external/memory).
type MemoryConfig struct {
	Enabled bool `yaml:"enable"`

	// Model selects the models[].model entry the memory subagent runs on. Empty uses the session's model.
	Model string `yaml:"model"`

	// FallbackModels are the models[].model ids the memory subagent tries, in
	// order, when the model above them fails before answering. The session's
	// own model is the last resort whether or not it is listed, so one
	// unreachable deployment does not take the memory run down with it (issue #247).
	FallbackModels []string `yaml:"fallback_models"`

	// Dir is the long-term memory root under Coddy home semantics. When empty, defaults to $CODDY_HOME/memory.
	Dir string `yaml:"dir"`

	// WaitSeconds is how long a user turn waits for the memory subagent's
	// report before its first model call. A nil pointer means the default
	// (20); an explicit 0 never waits, so the report can only reach the turn
	// through a later step.
	WaitSeconds *int `yaml:"wait_seconds"`

	// TimeoutSeconds is the hard limit of one memory run, capped by
	// tools.background.max_timeout_seconds like every task of the pool. Zero
	// means the default (300).
	TimeoutSeconds int `yaml:"timeout_seconds"`

	// KeepRuns bounds the finished memory runs a session keeps, task record
	// and child bundle alike. A nil pointer means the default (20); an
	// explicit 0 keeps every run.
	KeepRuns *int `yaml:"keep_runs"`

	// RecallMaxTurns and PersistMaxTurns bound the memory subagent's ReAct
	// rounds; the child's cap is the larger of the two (EffectiveMaxTurns).
	RecallMaxTurns int `yaml:"recall_max_turns"`

	// PersistMaxTurns caps the rounds together with RecallMaxTurns (see RecallMaxTurns).
	PersistMaxTurns int `yaml:"persist_max_turns"`

	// CopilotMaxTokens limits the completion size of the memory model's calls.
	CopilotMaxTokens int `yaml:"copilot_max_tokens"`

	// MaxSearchHits is the maximum number of snippets returned by memory_search.
	MaxSearchHits int `yaml:"max_search_hits"`

	// AdditionalPrompt is the operator's own instructions for the memory
	// subagent: a section of its system prompt and nothing else reads it
	// (issue #266). Empty adds nothing.
	AdditionalPrompt string `yaml:"additional_prompt"`

	// AdditionalPromptMaxChars caps additional_prompt in characters; a longer
	// text is cut there, the launch says so in the agent log and the config
	// check reports it. 0 means no cap.
	AdditionalPromptMaxChars int `yaml:"additional_prompt_max_chars"`
}

// Normalize trims string fields in place.
func (m *MemoryConfig) Normalize(p Paths) {
	m.Model = strings.TrimSpace(m.Model)
	for i := range m.FallbackModels {
		m.FallbackModels[i] = strings.TrimSpace(m.FallbackModels[i])
	}
	m.Dir = strings.TrimSpace(m.Dir)
	if m.Dir != "" {
		m.Dir = filepath.Clean(ExpandPathVars(m.Dir, p))
	}
	m.AdditionalPrompt = strings.TrimSpace(m.AdditionalPrompt)
}

// ApplyDefaults sets zero values to safe defaults. The pointer fields stay
// nil: an explicit 0 means something for them, and the Effective* readers
// apply the default when nothing was set.
func (m *MemoryConfig) ApplyDefaults() {
	if m.TimeoutSeconds <= 0 {
		m.TimeoutSeconds = MemoryDefaultTimeoutSeconds
	}
	if m.RecallMaxTurns <= 0 {
		m.RecallMaxTurns = 6
	}
	if m.PersistMaxTurns <= 0 {
		m.PersistMaxTurns = 12
	}
	if m.CopilotMaxTokens <= 0 {
		m.CopilotMaxTokens = 4096
	}
	if m.MaxSearchHits <= 0 {
		m.MaxSearchHits = 8
	}
}

// EffectiveWaitSeconds returns wait_seconds with the default applied; an
// explicit 0 means the turn never waits.
func (m *MemoryConfig) EffectiveWaitSeconds() int {
	if m.WaitSeconds == nil {
		return MemoryDefaultWaitSeconds
	}
	return max(*m.WaitSeconds, 0)
}

// EffectiveTimeoutSeconds returns timeout_seconds with the default applied.
func (m *MemoryConfig) EffectiveTimeoutSeconds() int {
	if m.TimeoutSeconds <= 0 {
		return MemoryDefaultTimeoutSeconds
	}
	return m.TimeoutSeconds
}

// EffectiveKeepRuns returns keep_runs with the default applied; an explicit
// 0 keeps every run.
func (m *MemoryConfig) EffectiveKeepRuns() int {
	if m.KeepRuns == nil {
		return MemoryDefaultKeepRuns
	}
	return max(*m.KeepRuns, 0)
}

// EffectiveAdditionalPrompt is additional_prompt as the memory subagent
// reads it: trimmed, and cut at additional_prompt_max_chars characters when
// a cap is set. The second result says whether the cap cut anything.
func (m *MemoryConfig) EffectiveAdditionalPrompt() (string, bool) {
	text := strings.TrimSpace(m.AdditionalPrompt)
	if m.AdditionalPromptMaxChars <= 0 || utf8.RuneCountInString(text) <= m.AdditionalPromptMaxChars {
		return text, false
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:m.AdditionalPromptMaxChars])), true
}

// EffectiveMaxTurns is the memory subagent's ReAct round cap: the larger of
// recall_max_turns and persist_max_turns, the bound the copilot loop had.
func (m *MemoryConfig) EffectiveMaxTurns() int {
	return max(m.RecallMaxTurns, m.PersistMaxTurns, 1)
}
