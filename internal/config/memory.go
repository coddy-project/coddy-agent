package config

import (
	"path/filepath"
	"strings"
)

// MemoryConfig controls the optional long-term memory copilot (implementation in external/memory).
type MemoryConfig struct {
	Enabled bool `yaml:"enabled"`

	// Model selects cfg.models entry for the unified memory copilot (before the main agent). Empty uses agent.model.
	Model string `yaml:"model"`

	// Dir is the long-term memory root under Coddy home semantics. When empty, defaults to $CODDY_HOME/memory.
	Dir string `yaml:"dir"`

	// RecallMaxTurns and PersistMaxTurns bound the single RunBeforeTurn memory copilot loop; the implementation uses max(RecallMaxTurns, PersistMaxTurns) as the round cap.
	RecallMaxTurns int `yaml:"recall_max_turns"`

	// PersistMaxTurns caps LLM rounds together with RecallMaxTurns (see RecallMaxTurns).
	PersistMaxTurns int `yaml:"persist_max_turns"`

	// CopilotMaxTokens limits completion size for memory LLM calls.
	CopilotMaxTokens int `yaml:"copilot_max_tokens"`

	// MaxSearchHits is the maximum number of snippets returned by memory_search.
	MaxSearchHits int `yaml:"max_search_hits"`
}

// Normalize trims string fields in place.
func (m *MemoryConfig) Normalize(p Paths) {
	m.Model = strings.TrimSpace(m.Model)
	m.Dir = strings.TrimSpace(m.Dir)
	if m.Dir != "" {
		m.Dir = filepath.Clean(ExpandPathVars(m.Dir, p))
	}
}

// ApplyDefaults sets zero values to safe defaults.
func (m *MemoryConfig) ApplyDefaults() {
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
