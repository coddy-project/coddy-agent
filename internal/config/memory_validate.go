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
	if m.WaitSeconds != nil && *m.WaitSeconds < 0 {
		return fmt.Errorf("memory.wait_seconds must be 0 or more, got %d", *m.WaitSeconds)
	}
	if m.TimeoutSeconds < 0 {
		return fmt.Errorf("memory.timeout_seconds must be 0 or more, got %d", m.TimeoutSeconds)
	}
	if m.KeepRuns != nil && *m.KeepRuns < 0 {
		return fmt.Errorf("memory.keep_runs must be 0 or more, got %d", *m.KeepRuns)
	}
	return nil
}
