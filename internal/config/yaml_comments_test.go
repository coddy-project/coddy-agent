package config

import (
	"encoding/json"
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

// --- a settings save keeps what the file says (issue #359) ---

// spelledConfig spells its values the way an operator writes them: paths under
// ${CODDY_HOME}, ~ and ${CWD}, a key and a directory from the environment, quotes, a
// flow list, an explicit false, and list entries that name only the fields their
// author cared about.
const spelledConfig = `# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: local
    type: openai
    api_base: "http://127.0.0.1:8080/v1"
    api_key: ${CODDY_TEST_LOCAL_KEY}
  - name: spare
    type: openai
    api_key: "sk-spare" # rotated monthly
models:
  - model: local/qwen
    max_tokens: 4096
  - model: spare/tiny
    max_tokens: 1024
    multimodal: false
agent:
  model: local/qwen
  max_turns: 40
memory:
  dir: ${CODDY_HOME}/memory
skills:
  dirs:
    - ~/.agents/skills
    - ${CODDY_HOME}/skills
    - ${CWD}/.coddy/skills
    - ${CODDY_TEST_TEAM}/skills
subagents:
  dirs:
    - ${CODDY_HOME}/agents
hooks:
  files:
    - ${CODDY_HOME}/hooks.json
instructions:
  files:
    - ${CODDY_HOME}/NOTES.md
sessions:
  dir: ${CODDY_HOME}/sessions
logger:
  outputs: [stderr, file]
  file: ${CODDY_HOME}/coddy.log
`

// settingsSaveFixture writes raw as the config file of a fresh home and loads it the
// way coddy serve does. CODDY_HOME is kept out of the environment, as for a server
// started without it, so only Paths knows what ${CODDY_HOME} stands for; {{home}} in
// raw is that home, spelled the way the loader substitutes it.
func settingsSaveFixture(t *testing.T, raw string) (*Config, string) {
	t.Helper()
	t.Setenv(EnvCODDYHome, "")
	t.Setenv("CODDY_TEST_LOCAL_KEY", "sk-local")
	t.Setenv("CODDY_TEST_TEAM", "/opt/team")
	dir := t.TempDir()
	paths := Paths{
		Home:       filepath.Join(dir, "home"),
		CWD:        filepath.Join(dir, "work"),
		ConfigPath: filepath.Join(dir, "home", "config.yaml"),
	}
	if err := os.MkdirAll(paths.Home, 0o755); err != nil {
		t.Fatal(err)
	}
	raw = strings.ReplaceAll(raw, "{{home}}", yamlSafePath(paths.Home))
	if err := os.WriteFile(paths.ConfigPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	live, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return live, raw
}

// saveFromSettings is one save from the settings screen against live, the running
// configuration: GET /coddy/config renders it as JSON, edit changes that document the
// way the form would (nil sends it back as served), and PUT parses it and renders it
// over the config file.
func saveFromSettings(t *testing.T, live *Config, edit func(doc map[string]any)) string {
	t.Helper()
	body, err := json.Marshal(ConfigToJSONDTO(live))
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		edit(doc)
		if body, err = json.Marshal(doc); err != nil {
			t.Fatal(err)
		}
	}
	next, err := ParseConfigJSONPreservingSecrets(body, live.Paths, live)
	if err != nil {
		t.Fatalf("parse the saved document: %v", err)
	}
	out, err := MarshalConfigYAMLForEdit(next, live, live, live.Paths.ConfigPath)
	if err != nil {
		t.Fatalf("render the save: %v", err)
	}
	return string(out)
}

// object returns doc[key] as a JSON object, for an edit of the served document.
func object(t *testing.T, doc map[string]any, key string) map[string]any {
	t.Helper()
	obj, ok := doc[key].(map[string]any)
	if !ok {
		t.Fatalf("the served document has no %q object: %#v", key, doc[key])
	}
	return obj
}

// The acceptance check of issue #359: GET and PUT with no edit in between leave the
// file byte for byte as it was. Every ${CODDY_HOME}, ~ and ${VAR} used to come back as
// the absolute path or the value it loads as, each list entry gained a zero for every
// field it never named, and the quotes and the flow list were rewritten.
func TestSettingsSaveWithoutEditsLeavesTheFileAsItWas(t *testing.T) {
	live, raw := settingsSaveFixture(t, spelledConfig)
	if got := saveFromSettings(t, live, nil); got != raw {
		t.Errorf("a save without edits rewrote the file:\n%s\nwant it as it was:\n%s", got, raw)
	}
}

// A value the operator changed is written the way they typed it, in a single value and
// in a list alike; the values around it keep their spelling.
func TestSettingsSaveWritesAChangedPathAsTyped(t *testing.T) {
	live, raw := settingsSaveFixture(t, spelledConfig)
	got := saveFromSettings(t, live, func(doc map[string]any) {
		object(t, doc, "memory")["dir"] = "/srv/memory"
		dirs := object(t, doc, "skills")["dirs"].([]any)
		dirs[1] = "/srv/skills"
	})
	want := strings.NewReplacer(
		"dir: ${CODDY_HOME}/memory", "dir: "+filepath.Clean("/srv/memory"),
		"- ${CODDY_HOME}/skills", "- /srv/skills",
	).Replace(raw)
	if got != want {
		t.Errorf("saved config:\n%s\nwant:\n%s", got, want)
	}
}

// Entries of a list of values are matched by what their spelling loads as, not by
// their position or their text, so removing one and adding another leaves the
// spelling of the rest alone.
func TestSettingsSaveKeepsSpellingsAroundAnAddedAndARemovedListEntry(t *testing.T) {
	live, raw := settingsSaveFixture(t, spelledConfig)
	got := saveFromSettings(t, live, func(doc map[string]any) {
		skills := object(t, doc, "skills")
		dirs := skills["dirs"].([]any)
		skills["dirs"] = append(append([]any{}, dirs[1:]...), "/srv/extra")
	})
	want := strings.NewReplacer(
		"    - ~/.agents/skills\n", "",
		"    - ${CODDY_TEST_TEAM}/skills\n", "    - ${CODDY_TEST_TEAM}/skills\n    - /srv/extra\n",
	).Replace(raw)
	if got != want {
		t.Errorf("saved config:\n%s\nwant:\n%s", got, want)
	}
}

// A list entry is written with the fields its previous version named and the ones the
// operator set, not with a zero for every field of the struct; a field the operator
// wrote stays even at its zero, and an entry the file did not have yet is written whole.
func TestSettingsSaveLeavesOutFieldsAListEntryNeverNamed(t *testing.T) {
	live, raw := settingsSaveFixture(t, spelledConfig)
	got := saveFromSettings(t, live, func(doc map[string]any) {
		models := doc["models"].([]any)
		models[1].(map[string]any)["max_tokens"] = 2048
		doc["providers"] = append(doc["providers"].([]any), map[string]any{
			"name": "third", "type": "openai", "api_key": "k3",
		})
	})
	for _, want := range []string{
		"  - model: local/qwen\n    max_tokens: 4096\n  - model: spare/tiny\n",
		"  - model: spare/tiny\n    max_tokens: 2048\n    multimodal: false\nagent:\n",
		"  - name: spare\n    type: openai\n    api_key: \"sk-spare\" # rotated monthly\n  - name: third\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("saved config lacks %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, raw[strings.Index(raw, "agent:\n"):]) {
		t.Errorf("the save rewrote sections it did not touch:\n%s", got)
	}
}

// coddy serve adjusts the configuration it runs: it spells out the relay's listen
// address and applies its flags over the file. The settings screen is served that live
// configuration, and a value it sends back unchanged is not an edit, so the file keeps
// its own - and when the operator does change one, that change is written.
func TestSettingsSaveKeepsTheFileValueOfWhatTheProcessChanged(t *testing.T) {
	live, raw := settingsSaveFixture(t, spelledConfig)
	live.Swarm.Host = live.Swarm.EffectiveHost()
	live.Logger.Level = LogLevelDebug
	if got := saveFromSettings(t, live, nil); got != raw {
		t.Errorf("a save wrote what the process changed into the file:\n%s\nwant it as it was:\n%s", got, raw)
	}

	got := saveFromSettings(t, live, func(doc map[string]any) {
		object(t, doc, "swarm")["host"] = "127.0.0.1"
	})
	if !strings.HasSuffix(got, "swarm:\n  host: 127.0.0.1\n") {
		t.Errorf("the relay address the operator set was not written:\n%s", got)
	}
}

// A secret the process took from its environment or its flags - a relay pairing token
// here - reaches the live configuration, and GET /coddy/config does not return it; the
// save restores it from the live configuration so that it is not lost. It must not be
// written into a file that never had it.
func TestSettingsSaveDoesNotWriteAPairingTokenTheFileNeverHad(t *testing.T) {
	live, _ := settingsSaveFixture(t, spelledConfig)
	live.Swarm.PairingTokens = append(live.Swarm.PairingTokens, "tok-from-the-environment")
	if got := saveFromSettings(t, live, nil); strings.Contains(got, "tok-from-the-environment") {
		t.Errorf("a save wrote the pairing token of the environment into the file:\n%s", got)
	}
}

// The file changed on disk after the settings screen read the configuration. The save
// writes the operator's edit and leaves the value they did not touch as the file now
// has it, instead of putting back what the form was served.
func TestSettingsSaveKeepsAnOutsideEditOfAValueTheFormDidNotTouch(t *testing.T) {
	live, raw := settingsSaveFixture(t, spelledConfig)
	onDisk := strings.Replace(raw, "max_turns: 40", "max_turns: 60", 1)
	if err := os.WriteFile(live.Paths.ConfigPath, []byte(onDisk), 0o600); err != nil {
		t.Fatal(err)
	}
	got := saveFromSettings(t, live, func(doc map[string]any) {
		object(t, doc, "memory")["dir"] = "/srv/memory"
	})
	want := strings.Replace(onDisk, "dir: ${CODDY_HOME}/memory", "dir: "+filepath.Clean("/srv/memory"), 1)
	if got != want {
		t.Errorf("saved config:\n%s\nwant:\n%s", got, want)
	}
}

// Two entries that load as one directory keep their own spelling in their own place.
func TestSettingsSaveKeepsTwoSpellingsOfOneDirectoryInPlace(t *testing.T) {
	live, raw := settingsSaveFixture(t, "skills:\n  dirs:\n    - ${CODDY_HOME}/skills\n    - {{home}}/skills\n")
	if got := saveFromSettings(t, live, nil); got != "# yaml-language-server: $schema=https://coddy.dev/config.schema.json\n"+raw {
		t.Errorf("saved config:\n%s\nwant the entries as they were:\n%s", got, raw)
	}
}

// When the file on disk no longer loads (a hand edit broke another section), the save
// cannot ask the loader what a spelling stands for. A ${CODDY_HOME} path still keeps
// its spelling: the home is the one of Paths, whether or not CODDY_HOME is in the
// environment.
func TestSettingsSaveKeepsCODDYHomeWhenThePreviousFileDoesNotLoad(t *testing.T) {
	t.Setenv(EnvCODDYHome, "")
	paths := Paths{Home: t.TempDir()}
	cfg, err := parseValidateYAMLBytes(expandConfigBody("memory:\n  dir: ${CODDY_HOME}/memory\n", paths), paths)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	existing := "memory:\n  dir: ${CODDY_HOME}/memory\nlogger:\n  level: shout\n"
	if _, err := parseValidateYAMLBytes(expandConfigBody(existing, paths), paths); err == nil {
		t.Fatal("the previous file must fail to load for this case")
	}
	saved := rewrite(t, cfg, existing)
	if !strings.Contains(saved, "dir: ${CODDY_HOME}/memory") {
		t.Errorf("saved config expanded ${CODDY_HOME}:\n%s", saved)
	}
}

// A file that no longer loads - a hand edit broke a section after the process read it -
// or is gone altogether says nothing a save could keep. The save writes the configuration
// the server runs, whole, as it always did: restoring "what the file says" would write
// the defaults of an empty document over everything the server still holds. What the
// broken file still spells with ${CODDY_HOME} or ~ keeps that spelling.
func TestSettingsSaveOverAFileThatDoesNotLoadWritesTheRunningConfig(t *testing.T) {
	for name, onDisk := range map[string]func(raw string) string{
		"broken": func(raw string) string {
			return strings.Replace(raw, "logger:\n", "logger:\n  level: shout\n", 1)
		},
		"missing": func(string) string { return "" },
	} {
		t.Run(name, func(t *testing.T) {
			live, raw := settingsSaveFixture(t, spelledConfig)
			live.Swarm.Host = live.Swarm.EffectiveHost()
			disk := onDisk(raw)
			if disk == "" {
				if err := os.Remove(live.Paths.ConfigPath); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := parseValidateYAMLBytes(expandConfigBody(disk, live.Paths), live.Paths); err == nil {
					t.Fatal("the edited file must fail to load for this case")
				}
				if err := os.WriteFile(live.Paths.ConfigPath, []byte(disk), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := saveFromSettings(t, live, nil)
			for _, want := range []string{"name: local", "name: spare", "model: local/qwen", "model: spare/tiny", "max_turns: 40"} {
				if !strings.Contains(got, want) {
					t.Errorf("the save lost %q of the running config:\n%s", want, got)
				}
			}
			if disk != "" {
				for _, want := range []string{"dir: ${CODDY_HOME}/memory", "- ~/.agents/skills", "- ${CODDY_HOME}/skills"} {
					if !strings.Contains(got, want) {
						t.Errorf("the save over a broken file lost the spelling %q:\n%s", want, got)
					}
				}
			}
		})
	}
}

// A reference in a field that is not a string - an integer, a boolean - is a string in
// the file that loads as a number or a flag. It keeps that spelling too, not an explicit
// !!int or !!bool tag in front of it. (Quoted, such a reference would load as a string
// and the field would refuse it, so only the bare form exists.)
func TestSettingsSaveKeepsReferencesInNumberAndBooleanFields(t *testing.T) {
	t.Setenv("CODDY_TEST_TURNS", "40")
	t.Setenv("CODDY_TEST_WAIT", "true")
	withTyped := strings.Replace(spelledConfig, "  max_turns: 40\n",
		"  max_turns: ${CODDY_TEST_TURNS}\n  wait_for_limit_reset: ${CODDY_TEST_WAIT}\n", 1)
	live, raw := settingsSaveFixture(t, withTyped)
	if live.Agent.MaxTurns != 40 || !live.Agent.WaitForLimitReset {
		t.Fatalf("the references did not load: max_turns=%d wait=%v", live.Agent.MaxTurns, live.Agent.WaitForLimitReset)
	}
	if got := saveFromSettings(t, live, nil); got != raw {
		t.Errorf("a save without edits rewrote the typed references:\n%s\nwant it as it was:\n%s", got, raw)
	}
}

// An empty list the loader fills with its defaults (skills.dirs: [] reads as the three
// standard directories) is still an empty list in the file after a save that did not
// touch it.
func TestSettingsSaveKeepsAListTheLoaderFillsIn(t *testing.T) {
	withEmpty := strings.Replace(spelledConfig,
		"skills:\n  dirs:\n    - ~/.agents/skills\n    - ${CODDY_HOME}/skills\n    - ${CWD}/.coddy/skills\n    - ${CODDY_TEST_TEAM}/skills\n",
		"skills:\n  dirs: []\n", 1)
	live, raw := settingsSaveFixture(t, withEmpty)
	if len(live.Skills.Dirs) == 0 {
		t.Fatal("the loader no longer fills an empty skills.dirs; this case needs another list")
	}
	if got := saveFromSettings(t, live, nil); got != raw {
		t.Errorf("a save without edits filled in the empty list:\n%s\nwant it as it was:\n%s", got, raw)
	}
}

// A provider renamed on the screen matches no entry of the file by name, and the note
// written about the old one does not follow it. Its place in the list still says which
// entry it was: the key it takes from the environment stays a reference instead of being
// written out resolved, its quotes stay, and the fields it never named stay out. The
// models renamed with it keep theirs the same way.
func TestSettingsSaveKeepsTheSpellingOfARenamedListEntry(t *testing.T) {
	live, raw := settingsSaveFixture(t, spelledConfig)
	got := saveFromSettings(t, live, func(doc map[string]any) {
		providers := doc["providers"].([]any)
		providers[0].(map[string]any)["name"] = "primary"
		providers[1].(map[string]any)["name"] = "backup"
		models := doc["models"].([]any)
		models[0].(map[string]any)["model"] = "primary/qwen"
		models[1].(map[string]any)["model"] = "backup/tiny"
		object(t, doc, "agent")["model"] = "primary/qwen"
	})
	want := strings.NewReplacer(
		"  - name: local\n", "  - name: primary\n",
		"  - name: spare\n", "  - name: backup\n",
		`    api_key: "sk-spare" # rotated monthly`+"\n", `    api_key: "sk-spare"`+"\n",
		"  - model: local/qwen\n", "  - model: primary/qwen\n",
		"  - model: spare/tiny\n", "  - model: backup/tiny\n",
		"agent:\n  model: local/qwen\n", "agent:\n  model: primary/qwen\n",
	).Replace(raw)
	if got != want {
		t.Errorf("saved config:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "sk-local") {
		t.Errorf("a rename wrote the key of the environment into the file:\n%s", got)
	}
}

// Another save landed between this client's read and its save. The fields the client
// did not touch are measured against what it read, not against the configuration now
// live, so they keep what the file says now instead of putting back what the client
// was shown; the field it changed is written.
func TestSettingsSaveKeepsWhatAnotherSaveWroteAfterTheRead(t *testing.T) {
	read, raw := settingsSaveFixture(t, spelledConfig)
	stale, err := json.Marshal(ConfigToJSONDTO(read))
	if err != nil {
		t.Fatal(err)
	}

	// Someone else raises max_turns and the server reloads.
	onDisk := strings.Replace(raw, "max_turns: 40", "max_turns: 60", 1)
	if err := os.WriteFile(read.Paths.ConfigPath, []byte(onDisk), 0o600); err != nil {
		t.Fatal(err)
	}
	live, err := LoadWithPaths(read.Paths)
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(stale, &doc); err != nil {
		t.Fatal(err)
	}
	object(t, doc, "memory")["dir"] = "/srv/memory"
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	next, err := ParseConfigJSONPreservingSecrets(body, live.Paths, live)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MarshalConfigYAMLForEdit(next, read, live, live.Paths.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(onDisk, "dir: ${CODDY_HOME}/memory", "dir: "+filepath.Clean("/srv/memory"), 1)
	if string(out) != want {
		t.Errorf("saved config:\n%s\nwant:\n%s", out, want)
	}
}
