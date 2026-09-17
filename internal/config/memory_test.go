package config

import (
	"strings"
	"testing"
)

// The memory subagent knobs: the defaults, the explicit zeros that mean
// something, and the round cap the two turn keys share.
func TestMemoryConfigDefaultsAndExplicitZeros(t *testing.T) {
	var m MemoryConfig
	m.ApplyDefaults()
	if m.EffectiveWaitSeconds() != MemoryDefaultWaitSeconds {
		t.Fatalf("wait = %d, want the default %d", m.EffectiveWaitSeconds(), MemoryDefaultWaitSeconds)
	}
	if m.EffectiveTimeoutSeconds() != MemoryDefaultTimeoutSeconds || m.TimeoutSeconds != MemoryDefaultTimeoutSeconds {
		t.Fatalf("timeout = %d / %d, want the default %d", m.EffectiveTimeoutSeconds(), m.TimeoutSeconds, MemoryDefaultTimeoutSeconds)
	}
	if m.EffectiveKeepRuns() != MemoryDefaultKeepRuns {
		t.Fatalf("keep_runs = %d, want the default %d", m.EffectiveKeepRuns(), MemoryDefaultKeepRuns)
	}
	if m.EffectiveMaxTurns() != 12 {
		t.Fatalf("max turns = %d, want 12 (the larger of 6 and 12)", m.EffectiveMaxTurns())
	}

	zero := 0
	m = MemoryConfig{WaitSeconds: &zero, KeepRuns: &zero, RecallMaxTurns: 9, PersistMaxTurns: 3}
	m.ApplyDefaults()
	if m.EffectiveWaitSeconds() != 0 {
		t.Fatalf("an explicit wait_seconds: 0 must mean never wait, got %d", m.EffectiveWaitSeconds())
	}
	if m.EffectiveKeepRuns() != 0 {
		t.Fatalf("an explicit keep_runs: 0 must keep every run, got %d", m.EffectiveKeepRuns())
	}
	if m.EffectiveMaxTurns() != 9 {
		t.Fatalf("max turns = %d, want 9", m.EffectiveMaxTurns())
	}
}

func TestMemoryConfigValidateRejectsNegativeBounds(t *testing.T) {
	if !memoryBuild {
		t.Skip("memory validation is compiled with -tags memory")
	}
	neg := -1
	cases := map[string]MemoryConfig{
		"wait":    {Enabled: true, WaitSeconds: &neg},
		"timeout": {Enabled: true, TimeoutSeconds: -5},
		"keep":    {Enabled: true, KeepRuns: &neg},
	}
	for name, m := range cases {
		err := m.Validate(&Config{})
		if err == nil || !strings.Contains(err.Error(), "memory.") {
			t.Errorf("%s: Validate = %v, want an error naming the key", name, err)
		}
	}
	ok := MemoryConfig{Enabled: true}
	if err := ok.Validate(&Config{}); err != nil {
		t.Fatalf("a memory block without bounds must validate, got %v", err)
	}
}
