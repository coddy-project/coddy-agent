package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestResolvedReasoningLevelsAutoDetect(t *testing.T) {
	cases := []struct {
		model string
		want  []string
	}{
		{"openai/gpt-5", []string{"minimal", "low", "medium", "high"}},
		{"openai/gpt-5-mini", []string{"minimal", "low", "medium", "high"}},
		{"openai/o3", []string{"low", "medium", "high"}},
		{"openai/o4-mini", []string{"low", "medium", "high"}},
		{"openai/o1", []string{"low", "medium", "high"}},
		{"anthropic/claude-sonnet-4-5", []string{"low", "medium", "high"}},
		{"anthropic/claude-opus-4-1", []string{"low", "medium", "high"}},
		{"anthropic/claude-3-7-sonnet", []string{"low", "medium", "high"}},
		// Non-reasoning models: no levels.
		{"openai/gpt-4o", nil},
		{"openai/gpt-4o-mini", nil},
		{"anthropic/claude-3-5-sonnet", nil},
	}
	for _, c := range cases {
		m := config.ModelEntry{Model: c.model}
		got := m.ResolvedReasoningLevels()
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ResolvedReasoningLevels() = %v, want %v", c.model, got, c.want)
		}
	}
}

func TestResolvedReasoningLevelsExplicitOverride(t *testing.T) {
	// Explicit list wins over name-based detection.
	m := config.ModelEntry{Model: "openai/gpt-4o", ReasoningLevels: []string{"low", "high"}}
	if got := m.ResolvedReasoningLevels(); !reflect.DeepEqual(got, []string{"low", "high"}) {
		t.Errorf("explicit override = %v, want [low high]", got)
	}
	// Explicit empty list disables reasoning even for a reasoning-capable model.
	d := config.ModelEntry{Model: "openai/gpt-5", ReasoningLevels: []string{}}
	if got := d.ResolvedReasoningLevels(); len(got) != 0 {
		t.Errorf("explicit empty list = %v, want disabled (empty)", got)
	}
}

func TestDefaultReasoningLevel(t *testing.T) {
	// Valid default within resolved levels is returned.
	m := config.ModelEntry{Model: "openai/gpt-5", ReasoningDefault: "high"}
	if got := m.DefaultReasoningLevel(); got != "high" {
		t.Errorf("DefaultReasoningLevel() = %q, want high", got)
	}
	// Default outside the resolved levels is ignored.
	bad := config.ModelEntry{Model: "openai/o3", ReasoningDefault: "minimal"}
	if got := bad.DefaultReasoningLevel(); got != "" {
		t.Errorf("invalid default = %q, want empty", got)
	}
	// Unset default yields empty (provider decides).
	none := config.ModelEntry{Model: "openai/gpt-5"}
	if got := none.DefaultReasoningLevel(); got != "" {
		t.Errorf("unset default = %q, want empty", got)
	}
	// Non-reasoning model never has a default.
	plain := config.ModelEntry{Model: "openai/gpt-4o", ReasoningDefault: "high"}
	if got := plain.DefaultReasoningLevel(); got != "" {
		t.Errorf("non-reasoning default = %q, want empty", got)
	}
}

func TestModelEntryReasoningParsedFromYAML(t *testing.T) {
	dir := t.TempDir()
	yaml := `
providers:
  - name: openai
    type: openai
    api_key: test-key
models:
  - model: openai/gpt-5
    reasoning_default: high
  - model: openai/gpt-4o
    reasoning_levels: [low, medium]
agent:
  model: openai/gpt-5
`
	f := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(f, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Models[0].DefaultReasoningLevel(); got != "high" {
		t.Errorf("models[0] default = %q, want high", got)
	}
	if got := cfg.Models[1].ResolvedReasoningLevels(); !reflect.DeepEqual(got, []string{"low", "medium"}) {
		t.Errorf("models[1] levels = %v, want [low medium]", got)
	}
}
