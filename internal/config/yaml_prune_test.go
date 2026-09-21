package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// Issue #265: a settings save used to write `key: null` for every optional field the
// operator never set, and to materialize every section applyDefaults fills in, so a
// file that kept those parts commented out came back annotated with nulls and default
// dumps. A save writes what the file had plus what actually differs from defaults.

const sparseConfig = `providers:
  - name: openai
    type: openai
    api_key: "k"
models:
  - model: openai/gpt-5.4
    max_tokens: 4096
agent:
  model: openai/gpt-5.4
`

// The reported shape: a sparse, hand-kept file must not come back with `null` values
// or with sections it never carried.
func TestMarshalConfigYAMLDropsUnsetKeysAndSections(t *testing.T) {
	cfg := loadForRewrite(t, sparseConfig)
	cfg.Agent.MaxTurns = 42
	saved := rewrite(t, cfg, sparseConfig)

	if strings.Contains(saved, ": null") {
		t.Errorf("saved config writes null for unset fields:\n%s", saved)
	}
	for _, absent := range []string{
		"output_limits", "tools:", "background:", "preview_server",
		"subagents:", "hooks:", "prompts:", "instructions:", "skills:",
		"rules:", "logger:", "compaction:", "memory:", "httpserver:",
		"swarm:", "scheduler:", "gateways:", "ui:",
	} {
		if strings.Contains(saved, absent) {
			t.Errorf("saved config materializes %q the file never had:\n%s", absent, saved)
		}
	}
	if !strings.Contains(saved, "max_turns: 42") {
		t.Errorf("saved config lost the value that changed:\n%s", saved)
	}
}

// The killer case from review: with no tools section in the file, setting one leaf
// writes exactly that leaf - the resolved defaults around it stay out.
func TestMarshalConfigYAMLWritesOnlyTheChangedLeaf(t *testing.T) {
	cfg := loadForRewrite(t, sparseConfig)
	cfg.Tools.OutputLimits.Grep = intPtr(50)
	saved := rewrite(t, cfg, sparseConfig)

	want := "tools:\n  output_limits:\n    grep: 50"
	if !strings.Contains(saved, want) {
		t.Errorf("saved config does not write only the changed leaf:\n%s", saved)
	}
	for _, sibling := range []string{"permission_mode", "ssh_connect_timeout", "background:", "websearch"} {
		if strings.Contains(saved, sibling) {
			t.Errorf("saved config materializes untouched sibling %q:\n%s", sibling, saved)
		}
	}
}

// Clearing an optional field must not leave `key: null` behind: absent and null load
// identically for every pointer field, so the line is dropped.
func TestMarshalConfigYAMLDropsAClearedOptionalField(t *testing.T) {
	const withLimits = sparseConfig + `tools:
  output_limits:
    read: 8000
`
	cfg := loadForRewrite(t, withLimits)
	cfg.Tools.OutputLimits.Read = nil // the operator cleared the field
	saved := rewrite(t, cfg, withLimits)

	if strings.Contains(saved, "read:") {
		t.Errorf("saved config keeps a cleared field:\n%s", saved)
	}
	if strings.Contains(saved, ": null") {
		t.Errorf("saved config writes null:\n%s", saved)
	}
}

// A comment belongs to the operator, not to the value next to it: an emptied section
// that carries a note is kept rather than dropped with the comment.
func TestMarshalConfigYAMLPreservesACommentOnAnEmptiedSection(t *testing.T) {
	const withComment = sparseConfig + `tools:
  # tune these when transcripts get long
  output_limits:
    read: 8000
`
	cfg := loadForRewrite(t, withComment)
	cfg.Tools.OutputLimits.Read = nil
	saved := rewrite(t, cfg, withComment)

	if !strings.Contains(saved, "# tune these when transcripts get long") {
		t.Errorf("saved config lost the comment on an emptied section:\n%s", saved)
	}
}

// An explicit 0 is a value (it disables the limit for that tool), not "unset".
func TestMarshalConfigYAMLKeepsAnExplicitZero(t *testing.T) {
	cfg := loadForRewrite(t, sparseConfig)
	cfg.Tools.OutputLimits.Read = intPtr(0)
	saved := rewrite(t, cfg, sparseConfig)

	if !strings.Contains(saved, "read: 0") {
		t.Errorf("saved config dropped an explicit zero:\n%s", saved)
	}
}

// An explicit empty list is a value too: `reasoning_levels: []` hides the selector,
// while an absent key auto-detects - the save must not collapse the two.
func TestMarshalConfigYAMLKeepsAnExplicitEmptyList(t *testing.T) {
	cfg := loadForRewrite(t, sparseConfig)
	empty := []string{}
	cfg.Models[0].ReasoningLevels = &empty
	saved := rewrite(t, cfg, sparseConfig)

	if !strings.Contains(saved, "reasoning_levels: []") {
		t.Errorf("saved config dropped the explicit empty reasoning_levels:\n%s", saved)
	}
}

// A fresh write (no previous file) carries the same rule: no nulls for unset
// fields, and no default sections either - a first `coddy mcp add` or a save
// after the file was deleted must not materialize the whole default dump, or
// every key written that day counts as "the file had it" and stays forever.
func TestMarshalConfigYAMLWritesNoNullsWithoutAPreviousFile(t *testing.T) {
	cfg := loadForRewrite(t, sparseConfig)
	fresh, err := MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatalf("marshal fresh config: %v", err)
	}
	if strings.Contains(string(fresh), ": null") {
		t.Errorf("a freshly written config still emits null:\n%s", fresh)
	}
	for _, absent := range []string{
		"output_limits", "tools:", "background:", "subagents:", "hooks:",
		"prompts:", "instructions:", "skills:", "rules:", "logger:",
		"compaction:", "memory:", "httpserver:", "swarm:", "scheduler:",
		"gateways:", "ui:",
	} {
		if strings.Contains(string(fresh), absent) {
			t.Errorf("a freshly written config materializes %q:\n%s", absent, fresh)
		}
	}
}

// The defaults pipeline expands ${CODDY_HOME} through ExpandCODDYHomeOnly, which
// substitutes the raw home - backslashes on Windows - while the saved document
// keeps the placeholder spelling. The compare must still read both as one
// directory, or every path-bearing section materializes on Windows (the
// windows-latest job caught exactly that).
func TestMarshalConfigYAMLDropsSectionsWithRawSeparatorHomes(t *testing.T) {
	cfg, err := parseValidateYAMLBytes(sparseConfig, Paths{Home: `C:\Users\ops`, CWD: `C:\work`})
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	fresh, err := MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatalf("marshal fresh config: %v", err)
	}
	for _, absent := range []string{
		"output_limits", "tools:", "subagents:", "hooks:", "prompts:",
		"instructions:", "skills:", "rules:", "logger:", "compaction:",
		"memory:", "httpserver:", "swarm:", "scheduler:", "gateways:", "ui:",
	} {
		if strings.Contains(string(fresh), absent) {
			t.Errorf("config with a backslash home materializes %q:\n%s", absent, fresh)
		}
	}
}

// ${CWD} is a per-session placeholder, not this process's cwd: an absolute path
// that happens to equal the process-cwd expansion of a ${CWD}-spelled default is
// a deliberate value, and dropping it would silently turn a fixed path into a
// per-session one.
func TestMarshalConfigYAMLKeepsAbsolutePathsMatchingTheCWDExpansion(t *testing.T) {
	cfg := loadForRewrite(t, sparseConfig)
	cfg.Paths.CWD = t.TempDir()
	cfg.Hooks.Files = []string{
		"${CODDY_HOME}/hooks.json",
		filepath.Join(cfg.Paths.CWD, ".claude/settings.json"),
		filepath.Join(cfg.Paths.CWD, ".claude/settings.local.json"),
		filepath.Join(cfg.Paths.CWD, ".coddy/hooks.json"),
	}
	saved := rewrite(t, cfg, sparseConfig)

	if !strings.Contains(saved, "files:") || !strings.Contains(saved, "hooks.json") {
		t.Errorf("saved config dropped hooks.files for matching the ${CWD} default expansion:\n%s", saved)
	}
}

// Providers and models injected from the environment belong to the process, not to
// the file: a save must not persist what applyDefaults conjured from OPENAI_API_KEY.
func TestMarshalConfigYAMLDoesNotPersistEnvInjectedProviders(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env-injected")
	const onlyAgent = `agent:
  model: openai/gpt-5.4
`
	cfg := loadForRewrite(t, onlyAgent)
	if len(cfg.Providers) == 0 {
		t.Skip("environment provider injection did not run")
	}
	saved := rewrite(t, cfg, onlyAgent)

	if strings.Contains(saved, "providers:") || strings.Contains(saved, "models:") {
		t.Errorf("saved config persists env-injected providers/models:\n%s", saved)
	}
}

// A ${VAR} reference is a spelling the operator chose; pruning must not disturb the
// merge that keeps it.
func TestMarshalConfigYAMLKeepsEnvironmentReferenceSpelling(t *testing.T) {
	const withRef = `providers:
  - name: openai
    type: openai
    api_key: ${CODDY_TEST_KEY}
models:
  - model: openai/gpt-5.4
    max_tokens: 4096
agent:
  model: openai/gpt-5.4
`
	t.Setenv("CODDY_TEST_KEY", "resolved-value")
	cfg := loadForRewrite(t, withRef)
	saved := rewrite(t, cfg, withRef)

	if !strings.Contains(saved, "api_key: ${CODDY_TEST_KEY}") {
		t.Errorf("saved config lost the ${VAR} spelling:\n%s", saved)
	}
}
