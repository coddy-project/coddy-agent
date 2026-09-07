package config

import (
	"strings"
	"testing"
)

func TestCompactionDefaults(t *testing.T) {
	var c Compaction
	c.ApplyDefaults()

	if !c.IsEnabled() {
		t.Fatal("compaction must be enabled by default")
	}
	if c.ThresholdPercent != CompactionDefaultThresholdPercent {
		t.Fatalf("threshold = %d, want %d", c.ThresholdPercent, CompactionDefaultThresholdPercent)
	}
	if got := c.EffectiveKeepRecentTurns(); got != CompactionDefaultKeepRecentTurns {
		t.Fatalf("keep_recent_turns = %d, want %d", got, CompactionDefaultKeepRecentTurns)
	}
	if (&Compaction{}).EffectiveThresholdPercent() != CompactionDefaultThresholdPercent {
		t.Fatal("EffectiveThresholdPercent must default without ApplyDefaults")
	}
}

func TestCompactionExplicitValuesSurviveDefaults(t *testing.T) {
	off := false
	zero := 0
	c := Compaction{Enabled: &off, ThresholdPercent: 55, KeepRecentTurns: &zero, Model: " fake/model "}
	c.ApplyDefaults()
	c.Normalize()

	if c.IsEnabled() {
		t.Fatal("explicit enabled=false must survive defaults")
	}
	if c.ThresholdPercent != 55 {
		t.Fatalf("threshold = %d, want 55", c.ThresholdPercent)
	}
	if got := c.EffectiveKeepRecentTurns(); got != 0 {
		t.Fatalf("keep_recent_turns = %d, want explicit 0", got)
	}
	if c.Model != "fake/model" {
		t.Fatalf("model = %q, want trimmed", c.Model)
	}
}

func TestCompactionValidate(t *testing.T) {
	neg := -1
	cases := []struct {
		name    string
		c       Compaction
		wantErr string
	}{
		{name: "defaults are valid", c: Compaction{}},
		{name: "threshold over 100", c: Compaction{ThresholdPercent: 101}, wantErr: "threshold_percent"},
		{name: "threshold negative", c: Compaction{ThresholdPercent: -5}, wantErr: "threshold_percent"},
		{name: "keep negative", c: Compaction{KeepRecentTurns: &neg}, wantErr: "keep_recent_turns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.c
			c.ApplyDefaults()
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want mention of %q", err, tc.wantErr)
			}
		})
	}
}

func TestCompactionYAMLSectionParsed(t *testing.T) {
	yaml := `
providers:
  - name: fake
    type: openai
    api_key: k
models:
  - model: fake/m
agent:
  model: fake/m
compaction:
  enabled: false
  threshold_percent: 70
  keep_recent_turns: 3
  model: fake/m
`
	cfg, err := parseValidateYAMLBytes(yaml, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Compaction.IsEnabled() {
		t.Fatal("yaml enabled: false ignored")
	}
	if cfg.Compaction.ThresholdPercent != 70 {
		t.Fatalf("threshold = %d", cfg.Compaction.ThresholdPercent)
	}
	if got := cfg.Compaction.EffectiveKeepRecentTurns(); got != 3 {
		t.Fatalf("keep = %d", got)
	}
	if cfg.Compaction.Model != "fake/m" {
		t.Fatalf("model = %q", cfg.Compaction.Model)
	}
}

func TestResultEvictionDefaults(t *testing.T) {
	var r ResultEviction
	if !r.IsEnabled() {
		t.Fatal("result eviction must be enabled by default")
	}
	if got := r.EffectiveKeepRecent(); got != ResultEvictionDefaultKeepRecent {
		t.Fatalf("keep_recent = %d, want %d", got, ResultEvictionDefaultKeepRecent)
	}
	if got := r.EffectiveMinResultBytes(); got != ResultEvictionDefaultMinResultBytes {
		t.Fatalf("min_result_bytes = %d, want %d", got, ResultEvictionDefaultMinResultBytes)
	}
}

func TestResultEvictionExplicitValues(t *testing.T) {
	off := false
	zero := 0
	r := ResultEviction{Enabled: &off, KeepRecent: &zero, MinResultBytes: &zero}
	if r.IsEnabled() {
		t.Fatal("explicit enabled:false must disable")
	}
	if r.EffectiveKeepRecent() != 0 {
		t.Fatalf("keep_recent = %d, want 0", r.EffectiveKeepRecent())
	}
	if r.EffectiveMinResultBytes() != 0 {
		t.Fatalf("min_result_bytes = %d, want 0", r.EffectiveMinResultBytes())
	}
}

func TestResultEvictionValidate(t *testing.T) {
	neg := -1
	if err := (&ResultEviction{KeepRecent: &neg}).Validate(); err == nil {
		t.Fatal("expected error for negative keep_recent")
	}
	if err := (&ResultEviction{MinResultBytes: &neg}).Validate(); err == nil {
		t.Fatal("expected error for negative min_result_bytes")
	}
	if err := (&Compaction{ResultEviction: ResultEviction{KeepRecent: &neg}}).Validate(); err == nil {
		t.Fatal("Compaction.Validate must surface result_eviction errors")
	}
}
