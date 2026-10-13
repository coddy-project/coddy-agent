package config

import (
	"encoding/json"
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

// descendant walks the properties of a JSON-schema-shaped document by name.
func descendant(t *testing.T, node map[string]interface{}, path ...string) map[string]interface{} {
	t.Helper()
	for _, name := range path {
		props, ok := node["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("no properties before %q", name)
		}
		next, ok := props[name].(map[string]interface{})
		if !ok {
			t.Fatalf("no property %q", name)
		}
		node = next
	}
	return node
}

// describedSchemas returns the two documents a person reads a key by: the
// published config.schema.json and the Settings form's UISchemaMap.
func describedSchemas(t *testing.T) map[string]map[string]interface{} {
	t.Helper()
	var schema map[string]interface{}
	if err := json.Unmarshal(ConfigSchemaJSON(), &schema); err != nil {
		t.Fatal(err)
	}
	return map[string]map[string]interface{}{"config.schema.json": schema, "UISchemaMap": UISchemaMap()}
}

// result_eviction also collapses the listings of a fixed set of tools, so the
// section no longer calls itself read/grep only and names each listing tool it
// takes.
func TestResultEvictionDescriptionsNameTheListingTools(t *testing.T) {
	for name, root := range describedSchemas(t) {
		section := descendant(t, root, "compaction", "result_eviction")
		title, _ := section["title"].(string)
		desc, _ := section["description"].(string)
		if strings.Contains(strings.ToLower(title), "read/grep") {
			t.Errorf("%s: the section still calls itself read/grep only: %q", name, title)
		}
		for _, tool := range []string{"glob", "print_tree", "websearch", "webfetch"} {
			if !strings.Contains(desc, tool) {
				t.Errorf("%s: the description of compaction.result_eviction does not name %s: %q", name, tool, desc)
			}
		}
	}
}

// The fold of a long turn's earlier steps and the recovery from a request the
// provider refused as too large are part of automatic compaction, with no
// switch of their own: compaction has no in_turn section, and the description
// of auto_enable, in both documents a person reads the key by, names both.
func TestAutoEnableDescriptionsNameTheInTurnFoldAndTheRecovery(t *testing.T) {
	for name, root := range describedSchemas(t) {
		section := descendant(t, root, "compaction")
		props, _ := section["properties"].(map[string]interface{})
		if _, ok := props["in_turn"]; ok {
			t.Errorf("%s: compaction still has an in_turn section", name)
		}
		desc, _ := descendant(t, section, "auto_enable")["description"].(string)
		for _, want := range []string{"earlier steps", "refuses"} {
			if !strings.Contains(desc, want) {
				t.Errorf("%s: the description of compaction.auto_enable does not mention %q: %q", name, want, desc)
			}
		}
	}
}
