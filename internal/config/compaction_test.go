package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompactionDefaults(t *testing.T) {
	var c Compaction
	c.ApplyDefaults()

	if !c.IsEnabled() {
		t.Fatal("compaction must be enabled by default")
	}
	if !c.IsAutoEnabled() {
		t.Fatal("automatic compaction must be enabled by default")
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

func TestCompactionCanDisableOnlyAutomation(t *testing.T) {
	off := false
	c := Compaction{AutoEnabled: &off}
	if !c.IsEnabled() || c.IsAutoEnabled() {
		t.Fatalf("manual and automatic switches are not independent: %+v", c)
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
  enable: true
  auto_enable: false
  threshold_percent: 70
  keep_recent_turns: 3
  model: fake/m
`
	cfg, err := parseValidateYAMLBytes(yaml, Paths{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Compaction.IsEnabled() || cfg.Compaction.IsAutoEnabled() {
		t.Fatal("yaml auto_enable: false ignored or manual compaction disabled")
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
		t.Fatal("explicit enable:false must disable")
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

func TestResultEvictionListingDefaults(t *testing.T) {
	var r ResultEviction
	want := []string{"glob", "print_tree", "websearch", "webfetch"}
	if got := r.EffectiveTools(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	if got := r.EffectiveKeepRecentSteps(); got != ResultEvictionDefaultKeepRecentSteps {
		t.Fatalf("keep_recent_steps = %d, want %d", got, ResultEvictionDefaultKeepRecentSteps)
	}
	if ResultEvictionDefaultKeepRecentSteps != 3 {
		t.Fatalf("the documented default is 3, the constant says %d", ResultEvictionDefaultKeepRecentSteps)
	}
}

// EffectiveTools hands out a copy: a caller sorting or trimming the answer must
// not rewrite the default every other session reads.
func TestResultEvictionEffectiveToolsIsACopy(t *testing.T) {
	var r ResultEviction
	first := r.EffectiveTools()
	first[0] = "tampered"
	if got := r.EffectiveTools()[0]; got != "glob" {
		t.Fatalf("mutating the returned default changed the next answer: %q", got)
	}

	set := []string{"glob"}
	r = ResultEviction{Tools: &set}
	got := r.EffectiveTools()
	got[0] = "tampered"
	if set[0] != "glob" {
		t.Fatalf("mutating the returned list changed the configured one: %v", set)
	}
}

func TestResultEvictionListingExplicitValues(t *testing.T) {
	empty := []string{}
	one := 1
	r := ResultEviction{Tools: &empty, KeepRecentSteps: &one}
	if got := r.EffectiveTools(); len(got) != 0 {
		t.Fatalf("an explicit empty list must mean no listing tools, got %v", got)
	}
	if got := r.EffectiveKeepRecentSteps(); got != 1 {
		t.Fatalf("keep_recent_steps = %d, want the explicit 1", got)
	}

	two := []string{"webfetch", "glob"}
	r = ResultEviction{Tools: &two}
	if got := r.EffectiveTools(); !reflect.DeepEqual(got, []string{"webfetch", "glob"}) {
		t.Fatalf("tools = %v", got)
	}
}

func TestResultEvictionListingValidate(t *testing.T) {
	neg := -1
	zero := 0
	bad := []string{"glob", "run_command"}
	twice := []string{"glob", "print_tree", "glob"}
	mcp := []string{"mcp__files__list"}
	ok := []string{"glob", "print_tree", "websearch", "webfetch"}
	empty := []string{}

	cases := []struct {
		name    string
		r       ResultEviction
		wantErr []string
	}{
		{name: "defaults", r: ResultEviction{}},
		{name: "every allowed name", r: ResultEviction{Tools: &ok}},
		{name: "explicit empty list", r: ResultEviction{Tools: &empty}},
		{name: "run_command is refused", r: ResultEviction{Tools: &bad},
			wantErr: []string{"compaction.result_eviction.tools", `"run_command"`, "glob, print_tree, websearch, webfetch"}},
		{name: "an MCP tool is refused", r: ResultEviction{Tools: &mcp},
			wantErr: []string{"compaction.result_eviction.tools", "mcp__files__list"}},
		{name: "a name twice is refused", r: ResultEviction{Tools: &twice},
			wantErr: []string{"compaction.result_eviction.tools", "duplicate", `"glob"`}},
		// A window of no steps would collapse the listing the model has just
		// asked for in the very next request, so the floor is one step.
		{name: "zero steps", r: ResultEviction{KeepRecentSteps: &zero},
			wantErr: []string{"compaction.result_eviction.keep_recent_steps", ">= 1"}},
		{name: "negative steps", r: ResultEviction{KeepRecentSteps: &neg},
			wantErr: []string{"compaction.result_eviction.keep_recent_steps", ">= 1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.r
			err := r.Validate()
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, w := range tc.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("error %q does not mention %q", err, w)
				}
			}
		})
	}

	// The section's own Validate is reached through the compaction one, so a
	// config with a bad name is refused at load.
	c := Compaction{ResultEviction: ResultEviction{Tools: &bad}}
	c.ApplyDefaults()
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "result_eviction.tools") {
		t.Fatalf("Compaction.Validate must surface result_eviction.tools errors, got %v", err)
	}
}

// An absent key and an explicit empty list are different answers: the first
// evicts the default listing tools, the second evicts read and grep only.
func TestResultEvictionYAMLToolsNilVersusEmpty(t *testing.T) {
	load := func(extra string) (*Config, error) {
		return parseValidateYAMLBytes(`
providers:
  - name: fake
    type: openai
    api_key: k
models:
  - model: fake/m
agent:
  model: fake/m
compaction:
  result_eviction:
`+extra, Paths{})
	}

	absent, err := load("    keep_recent: 1\n")
	if err != nil {
		t.Fatal(err)
	}
	if absent.Compaction.ResultEviction.Tools != nil {
		t.Fatalf("an absent key must stay nil, got %v", *absent.Compaction.ResultEviction.Tools)
	}
	if got := absent.Compaction.ResultEviction.EffectiveTools(); len(got) != 4 {
		t.Fatalf("an absent key must mean the default list, got %v", got)
	}

	empty, err := load("    tools: []\n")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Compaction.ResultEviction.Tools == nil {
		t.Fatal("an explicit empty list decoded as nil")
	}
	if got := empty.Compaction.ResultEviction.EffectiveTools(); len(got) != 0 {
		t.Fatalf("an explicit empty list must mean none, got %v", got)
	}

	set, err := load("    tools: [glob, webfetch]\n    keep_recent_steps: 1\n")
	if err != nil {
		t.Fatal(err)
	}
	re := set.Compaction.ResultEviction
	if !reflect.DeepEqual(re.EffectiveTools(), []string{"glob", "webfetch"}) {
		t.Fatalf("tools = %v", re.EffectiveTools())
	}
	if re.KeepRecentSteps == nil || *re.KeepRecentSteps != 1 {
		t.Fatalf("keep_recent_steps: 1 must stay an explicit 1, got %v", re.KeepRecentSteps)
	}

	// The loader refuses a window of no steps with the key named.
	if _, err := load("    keep_recent_steps: 0\n"); err == nil || !strings.Contains(err.Error(), "result_eviction.keep_recent_steps") || !strings.Contains(err.Error(), ">= 1") {
		t.Fatalf("a config with keep_recent_steps: 0 must be refused at load, got %v", err)
	}

	if _, err := load("    tools: [run_command]\n"); err == nil || !strings.Contains(err.Error(), "result_eviction.tools") {
		t.Fatalf("a config naming run_command must be refused at load, got %v", err)
	}
	if _, err := load("    tools: [glob, webfetch, glob]\n"); err == nil || !strings.Contains(err.Error(), "result_eviction.tools") || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("a config naming glob twice must be refused at load, got %v", err)
	}
}
