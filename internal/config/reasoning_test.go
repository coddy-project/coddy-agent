package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		{"neuraldeep/qwen3.6-35b-a3b", []string{"low", "medium", "high"}},
		{"neuraldeep/qwen3.8-27b", []string{"low", "medium", "high"}},
		{"together/qwen3-32b", []string{"low", "medium", "high"}},
		{"neuraldeep/gpt-oss-120b", []string{"low", "medium", "high"}},
		{"neuraldeep/gpt-oss-20b", []string{"low", "medium", "high"}},
		// Non-reasoning models: no levels.
		{"openai/gpt-4o", nil},
		{"openai/gpt-4o-mini", nil},
		{"anthropic/claude-3-5-sonnet", nil},
		{"openai/qwen2.5-72b-instruct", nil},
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
	m := config.ModelEntry{Model: "openai/gpt-4o", ReasoningLevels: &[]string{"low", "high"}}
	if got := m.ResolvedReasoningLevels(); !reflect.DeepEqual(got, []string{"low", "high"}) {
		t.Errorf("explicit override = %v, want [low high]", got)
	}
	// Explicit empty list disables reasoning even for a reasoning-capable model.
	d := config.ModelEntry{Model: "openai/gpt-5", ReasoningLevels: &[]string{}}
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

// TestReasoningLevelsForCodexProvider pins the provider-aware level list: the
// Codex backend serves gpt-5* ids but rejects the "minimal" tier they normally
// imply, so a codex-backed model must offer "none" instead.
func TestReasoningLevelsForCodexProvider(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "codex", Type: "codex"},
			{Name: "openai", Type: "openai"},
		},
		Models: []config.ModelEntry{
			{Model: "codex/gpt-5.5"},
			{Model: "openai/gpt-5"},
			{Model: "codex/gpt-5.4", ReasoningLevels: &[]string{"minimal", "high"}, ReasoningDefault: "minimal"},
		},
	}
	cases := []struct {
		model     string
		want      []string
		wantOwner string
	}{
		{"codex/gpt-5.5", []string{"none", "low", "medium", "high"}, "codex"},
		{"openai/gpt-5", []string{"minimal", "low", "medium", "high"}, "openai"},
		{"codex/gpt-5.4", []string{"none", "high"}, "codex"},
	}
	for _, c := range cases {
		ent := cfg.FindModelEntry(c.model)
		if ent == nil {
			t.Fatalf("model %q not found", c.model)
		}
		if got := cfg.ReasoningLevelsFor(ent); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ReasoningLevelsFor = %v, want %v", c.model, got, c.want)
		}
	}
	// An explicit "minimal" default follows the same remap, so it stays selectable.
	ent := cfg.FindModelEntry("codex/gpt-5.4")
	if got := cfg.DefaultReasoningLevelFor(ent); got != "none" {
		t.Errorf("codex default = %q, want none", got)
	}
	openaiEnt := cfg.FindModelEntry("openai/gpt-5")
	if got := cfg.ReasoningLevelsFor(openaiEnt); got[0] != config.ReasoningMinimal {
		t.Errorf("non-codex provider must keep minimal, got %v", got)
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

// TestReasoningLevelsForProviderType pins the caller-supplied provider type the
// settings form sends while a provider row is unsaved: the Codex remap follows
// that type, not whatever the config on disk says for the same prefix.
func TestReasoningLevelsForProviderType(t *testing.T) {
	cases := []struct {
		name         string
		entry        config.ModelEntry
		providerType string
		want         []string
	}{
		{"codex remaps minimal", config.ModelEntry{Model: "any/gpt-5.5"}, "codex", []string{"none", "low", "medium", "high"}},
		{"openai keeps minimal", config.ModelEntry{Model: "any/gpt-5.5"}, "openai", []string{"minimal", "low", "medium", "high"}},
		{"empty type keeps minimal", config.ModelEntry{Model: "any/gpt-5.5"}, "", []string{"minimal", "low", "medium", "high"}},
		{"codex explicit override remaps too", config.ModelEntry{Model: "any/gpt-5.4", ReasoningLevels: &[]string{"minimal", "high"}}, "codex", []string{"none", "high"}},
		{"codex non-reasoning stays empty", config.ModelEntry{Model: "any/gpt-4o"}, "codex", nil},
		{"explicit opt-out stays empty", config.ModelEntry{Model: "any/gpt-5", ReasoningLevels: &[]string{}}, "codex", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := config.ReasoningLevelsForProviderType(&tc.entry, tc.providerType)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("levels = %v, want %v", got, tc.want)
			}
		})
	}
	if got := config.ReasoningLevelsForProviderType(nil, "codex"); got != nil {
		t.Fatalf("nil entry = %v, want nil", got)
	}
}

// --- settings save round trips ---

// saveThroughSettings replays what PUT /coddy/config does to a config file:
// parse the JSON body the settings UI sent, serialize the whole config back to
// YAML, write it, and reload. Every assertion below runs through the write and
// the reload, because that is where the nil / empty distinction used to be lost.
func saveThroughSettings(t *testing.T, cfgPath string, body []byte) *config.Config {
	t.Helper()
	cur, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load before save: %v", err)
	}
	next, err := config.ParseConfigJSONPreservingSecrets(body, cur.Paths, cur)
	if err != nil {
		t.Fatalf("parse settings body: %v", err)
	}
	yb, err := config.MarshalConfigYAML(next)
	if err != nil {
		t.Fatalf("marshal yaml: %v", err)
	}
	if err := os.WriteFile(cfgPath, yb, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	return reloaded
}

// writeConfig seeds a config file and returns its path.
func writeConfig(t *testing.T, yml string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvCODDYHome, home)
	p := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// reasoningYAML builds a one-provider, one-model config whose model entry carries
// the given extra keys (already indented for models[0]).
func reasoningYAML(modelExtras string) string {
	return `providers:
  - name: valera
    type: openai
    api_base: "http://127.0.0.1:9/v1"
    api_key: "k"
models:
  - model: "valera/qwen3.8-27b"
    max_tokens: 4096
    temperature: 0.2
` + modelExtras + `agent:
  model: "valera/qwen3.8-27b"
`
}

// TestAddedModelKeepsAutoDetectionThroughSave is the regression for the reported
// bug: a logical model added in Settings offered no reasoning selector. The UI
// seeding an empty reasoning_levels was only half of it - MarshalConfigYAML also
// wrote "reasoning_levels: []" for a nil slice, so the very first save turned
// auto-detection off even for a body that omitted the key.
func TestAddedModelKeepsAutoDetectionThroughSave(t *testing.T) {
	p := writeConfig(t, reasoningYAML(""))

	// The settings body the UI sends for a freshly added model: no reasoning_levels.
	body := []byte(`{"providers":[{"name":"valera","type":"openai","api_base":"http://127.0.0.1:9/v1","api_key":"k"}],` +
		`"models":[{"model":"valera/qwen3.8-27b","max_tokens":4096,"temperature":0.2}],` +
		`"agent":{"model":"valera/qwen3.8-27b"}}`)

	reloaded := saveThroughSettings(t, p, body)
	ent := reloaded.FindModelEntry("valera/qwen3.8-27b")
	if ent == nil {
		t.Fatal("model entry lost in the save round trip")
	}
	if ent.ReasoningLevels != nil {
		t.Fatalf("omitted reasoning_levels became explicit %v after one save", *ent.ReasoningLevels)
	}
	if got := ent.ResolvedReasoningLevels(); len(got) == 0 {
		t.Fatal("qwen3.8 lost reasoning auto-detection after one settings save")
	}

	// Pin the root cause too: the key must be absent from the file on disk, not
	// written as an empty list that reads back as the explicit opt-out.
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "reasoning_levels") {
		t.Fatalf("saved YAML must omit reasoning_levels for an auto-detected model:\n%s", raw)
	}
}

// TestExplicitEmptyReasoningLevelsSurvivesSave pins the opposite direction: an
// operator who wrote "reasoning_levels: []" by hand to hide the selector keeps
// that opt-out after opening Settings and saving.
func TestExplicitEmptyReasoningLevelsSurvivesSave(t *testing.T) {
	p := writeConfig(t, reasoningYAML("    reasoning_levels: []\n"))

	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	ent := cfg.FindModelEntry("valera/qwen3.8-27b")
	if ent == nil || ent.ReasoningLevels == nil || len(*ent.ReasoningLevels) != 0 {
		t.Fatalf("precondition: hand-written [] should load as an explicit empty list, got %v", ent)
	}
	if got := ent.ResolvedReasoningLevels(); len(got) != 0 {
		t.Fatalf("precondition: explicit [] should disable reasoning, got %v", got)
	}

	// GET /coddy/config must carry the explicit [] out to the UI, or the save
	// below cannot send it back.
	body, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"reasoning_levels":[]`) {
		t.Fatalf("GET /coddy/config dropped the explicit opt-out:\n%s", body)
	}

	reloaded := saveThroughSettings(t, p, body)
	ent2 := reloaded.FindModelEntry("valera/qwen3.8-27b")
	if ent2 == nil || ent2.ReasoningLevels == nil {
		t.Fatal("explicit reasoning_levels: [] opt-out was lost in the save round trip")
	}
	if got := ent2.ResolvedReasoningLevels(); len(got) != 0 {
		t.Fatalf("opt-out flipped back to auto-detect: %v", got)
	}
}

// TestExplicitReasoningLevelsSurviveSave covers the ordinary override so the
// pointer type is not only exercised at its two edge values.
func TestExplicitReasoningLevelsSurviveSave(t *testing.T) {
	p := writeConfig(t, reasoningYAML("    reasoning_levels: [low, high]\n    reasoning_default: high\n"))

	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(config.ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}

	reloaded := saveThroughSettings(t, p, body)
	ent := reloaded.FindModelEntry("valera/qwen3.8-27b")
	if ent == nil {
		t.Fatal("model entry lost")
	}
	got := ent.ResolvedReasoningLevels()
	if len(got) != 2 || got[0] != "low" || got[1] != "high" {
		t.Fatalf("override = %v, want [low high]", got)
	}
	if ent.DefaultReasoningLevel() != "high" {
		t.Fatalf("reasoning_default = %q, want high", ent.DefaultReasoningLevel())
	}
}
