package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MemoryConfig controls the optional long-term memory copilot (build tag memory).
type MemoryConfig struct {
	Enabled bool `yaml:"enabled"`

	// Model selects cfg.models entry for recall and persist LLM calls. Empty uses agent.model.
	Model string `yaml:"model"`

	// Dir is the long-term memory root under Coddy home semantics. When empty, defaults to $CODDY_HOME/memory.
	Dir string `yaml:"dir"`

	// RecallMaxTurns caps tool rounds for the recall sub-agent.
	RecallMaxTurns int `yaml:"recall_max_turns"`

	// PersistMaxTurns caps tool rounds after the judge approves saving (normally 1).
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
		m.Dir = filepath.Clean(ExpandCODDYHomeOnly(m.Dir, p))
	}
}

// ApplyDefaults sets zero values to safe defaults.
func (m *MemoryConfig) ApplyDefaults() {
	if m.RecallMaxTurns <= 0 {
		m.RecallMaxTurns = 6
	}
	if m.PersistMaxTurns <= 0 {
		m.PersistMaxTurns = 4
	}
	if m.CopilotMaxTokens <= 0 {
		m.CopilotMaxTokens = 4096
	}
	if m.MaxSearchHits <= 0 {
		m.MaxSearchHits = 8
	}
}

// Validate checks memory settings when enabled.
func (m *MemoryConfig) Validate(cfg *Config) error {
	if !m.Enabled {
		return nil
	}
	if !MemoryFeatureCompiled() {
		return fmt.Errorf("memory.enabled is true but this binary was built without long-term memory support; rebuild with -tags memory (see Makefile target build-memory)")
	}
	if m.Model != "" && cfg.FindModelEntry(m.Model) == nil {
		return fmt.Errorf("memory.model %q not found in models list", m.Model)
	}
	return nil
}
