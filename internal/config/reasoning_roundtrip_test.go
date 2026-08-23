package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

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
