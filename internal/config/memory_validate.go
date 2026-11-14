//go:build memory

package config

import "fmt"

// Validate checks memory settings when enabled.
func (m *MemoryConfig) Validate(cfg *Config) error {
	if !m.Enabled {
		return nil
	}
	if m.Model != "" && cfg.FindModelEntry(m.Model) == nil {
		return fmt.Errorf("memory.model %q not found in models list", m.Model)
	}
	if m.MaxNoteChars != nil && *m.MaxNoteChars < 0 {
		return fmt.Errorf("memory.max_note_chars must be 0 or more, got %d", *m.MaxNoteChars)
	}
	if m.AdditionalPromptMaxChars < 0 {
		return fmt.Errorf("memory.additional_prompt_max_chars must be 0 or more, got %d", m.AdditionalPromptMaxChars)
	}
	return nil
}
