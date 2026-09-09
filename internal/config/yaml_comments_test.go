package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// documentedConfig is the shape of a hand-maintained config: a leading block with the
// schema modeline, a note above a section, a note above one sequence entry, an inline
// note, a commented-out key kept for later, and a key order of the operator's choosing
// (agent before models).
const documentedConfig = `# yaml-language-server: $schema=https://coddy.dev/config.schema.json
# Workstation setup - the vault fills the keys in.

# every provider this box can reach
providers:
  # the llama.cpp box in the basement
  - name: valera
    type: openai
    # api_key_command: "vault read -field=key coddy/valera"
    api_key: "k" # rotated every friday
  - name: nikolay
    type: openai
    api_key: "n" # the spare box
agent:
  model: valera/qwen3.8-27b
models:
  - model: valera/qwen3.8-27b # the workhorse
    max_tokens: 4096
`

func loadForRewrite(t *testing.T, yml string) *Config {
	t.Helper()
	cfg, err := parseValidateYAMLBytes(yml, Paths{Home: t.TempDir()})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return cfg
}

func rewrite(t *testing.T, cfg *Config, existing string) string {
	t.Helper()
	out, err := MarshalConfigYAMLPreservingComments(cfg, []byte(existing))
	if err != nil {
		t.Fatalf("marshal over existing config: %v", err)
	}
	return string(out)
}

// A save is a rewrite of a file someone maintains by hand, so everything that is not a
// value has to survive it: the notes, the order of the keys, and the schema modeline.
func TestMarshalConfigYAMLKeepsCommentsAndKeyOrder(t *testing.T) {
	cfg := loadForRewrite(t, documentedConfig)
	cfg.Agent.MaxTurns = 42
	saved := rewrite(t, cfg, documentedConfig)

	for _, want := range []string{
		"# Workstation setup - the vault fills the keys in.",
		"# every provider this box can reach",
		"# the llama.cpp box in the basement",
		`# api_key_command: "vault read -field=key coddy/valera"`,
		"# rotated every friday",
		"# the spare box",
		"# the workhorse",
	} {
		if !strings.Contains(saved, want) {
			t.Errorf("saved config lost the comment %q:\n%s", want, saved)
		}
	}
	if !strings.Contains(saved, "max_turns: 42") {
		t.Errorf("saved config lost the value that changed:\n%s", saved)
	}
	// The operator put agent before models; a save must not reshuffle the file.
	providers, agent, models := strings.Index(saved, "\nproviders:"), strings.Index(saved, "\nagent:"), strings.Index(saved, "\nmodels:")
	if providers < 0 || agent < 0 || models < 0 || providers >= agent || agent >= models {
		t.Errorf("saved config reordered the top-level keys (providers=%d agent=%d models=%d):\n%s",
			providers, agent, models, saved)
	}
	// Keys the file never carried land after the ones it did.
	if idx := strings.Index(saved, "\ntools:"); idx >= 0 && idx < models {
		t.Errorf("a key the previous file did not carry jumped ahead of the ones it did:\n%s", saved)
	}
}

// A save is applied to the file the previous save produced, so a second pass over an
// unchanged config must be a no-op - otherwise every save shows up as a diff.
func TestMarshalConfigYAMLRewriteIsStable(t *testing.T) {
	cfg := loadForRewrite(t, documentedConfig)
	first := rewrite(t, cfg, documentedConfig)
	second := rewrite(t, cfg, first)
	if first != second {
		t.Errorf("rewriting an unchanged config changed the file again:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if _, err := parseValidateYAMLBytes(second, Paths{Home: t.TempDir()}); err != nil {
		t.Fatalf("the rewritten config no longer loads: %v\n%s", err, second)
	}
}

// Comments belong to the entry they describe, not to its position: reordering the
// providers must carry each note along, and a renamed entry must not adopt one.
func TestMarshalConfigYAMLMatchesSequenceEntriesByIdentity(t *testing.T) {
	cfg := loadForRewrite(t, documentedConfig)
	cfg.Providers[0], cfg.Providers[1] = cfg.Providers[1], cfg.Providers[0]
	saved := rewrite(t, cfg, documentedConfig)

	valera := strings.Index(saved, "name: valera")
	nikolay := strings.Index(saved, "name: nikolay")
	basement := strings.Index(saved, "# the llama.cpp box in the basement")
	if valera < 0 || nikolay < 0 || basement < 0 {
		t.Fatalf("saved config lost an entry or its comment:\n%s", saved)
	}
	if nikolay >= basement || basement >= valera {
		t.Errorf("the note did not follow its provider (nikolay=%d note=%d valera=%d):\n%s",
			nikolay, basement, valera, saved)
	}

	renamed := loadForRewrite(t, documentedConfig)
	renamed.Providers[0].Name = "vasily"
	savedRenamed := rewrite(t, renamed, documentedConfig)
	if strings.Contains(savedRenamed, "# the llama.cpp box in the basement") {
		t.Errorf("a renamed provider inherited the previous entry's note:\n%s", savedRenamed)
	}
}

// The modeline is what connects the file to the schema, so a config Coddy writes always
// carries one - and an operator who pointed the file somewhere else keeps their choice.
func TestMarshalConfigYAMLSchemaModeline(t *testing.T) {
	cfg := loadForRewrite(t, documentedConfig)

	fresh, err := MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatalf("marshal fresh config: %v", err)
	}
	if first, _, _ := strings.Cut(string(fresh), "\n"); first != SchemaModeline() {
		t.Errorf("a freshly rendered config starts with %q, wanted the schema modeline %q", first, SchemaModeline())
	}

	const localSchema = "# yaml-language-server: $schema=./config.schema.json\n"
	saved := rewrite(t, cfg, localSchema+documentedConfig[strings.Index(documentedConfig, "providers:"):])
	if !strings.Contains(saved, "$schema=./config.schema.json") {
		t.Errorf("the operator's own schema reference was replaced:\n%s", saved)
	}
	if got := strings.Count(saved, schemaModelineMarker); got != 1 {
		t.Errorf("saved config carries %d schema modelines, want 1:\n%s", got, saved)
	}
}

// A previous file that cannot be parsed carries nothing worth keeping, and a save must
// still go through: refusing to write would leave the operator stuck with the broken file.
func TestMarshalConfigYAMLIgnoresAnUnreadablePreviousFile(t *testing.T) {
	cfg := loadForRewrite(t, documentedConfig)
	for name, existing := range map[string]string{
		"broken":   "providers: [\n  - name: valera\n",
		"empty":    "",
		"a scalar": "just a string\n",
	} {
		saved := rewrite(t, cfg, existing)
		if !strings.HasPrefix(saved, SchemaModeline()) {
			t.Errorf("%s previous file: saved config lost the schema modeline:\n%s", name, saved)
		}
		if !strings.Contains(saved, "name: valera") {
			t.Errorf("%s previous file: saved config lost its values:\n%s", name, saved)
		}
	}
}

// The file-shaped entry point is what the save paths call; a config file that does not
// exist yet is the first-run case and must produce a documented file, not an error.
func TestMarshalConfigYAMLForFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := loadForRewrite(t, documentedConfig)

	missing, err := MarshalConfigYAMLForFile(cfg, path)
	if err != nil {
		t.Fatalf("marshal for a missing file: %v", err)
	}
	if !strings.HasPrefix(string(missing), SchemaModeline()) {
		t.Errorf("a config written where none existed lacks the schema modeline:\n%s", missing)
	}

	if err := os.WriteFile(path, []byte(documentedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	over, err := MarshalConfigYAMLForFile(cfg, path)
	if err != nil {
		t.Fatalf("marshal over the existing file: %v", err)
	}
	if !strings.Contains(string(over), "# the llama.cpp box in the basement") {
		t.Errorf("marshalling over the file on disk dropped its comments:\n%s", over)
	}
}
