package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modelToolsYAML = `providers:
  - name: local
    type: openai
    api_base: http://127.0.0.1:8080/v1
models:
  - model: local/qwen
    max_context_tokens: 49152
    tools:
      - " read "
      - grep
      - "ctx__*"
    disallowed_tools: [write, "  "]
  - model: local/plain
agent:
  model: local/qwen
`

func loadModelToolsConfig(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromCLI(CLIPaths{Config: path})
	if err != nil {
		t.Fatalf("LoadFromCLI: %v", err)
	}
	return cfg
}

func sameStrings(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// The lists load trimmed, survive the settings editor's JSON round trip and
// the YAML written back, and a row without them gets no key at all.
func TestModelToolListsSurviveEveryRoundTrip(t *testing.T) {
	cfg := loadModelToolsConfig(t, modelToolsYAML)
	qwen := cfg.FindModelEntry("local/qwen")
	if qwen == nil {
		t.Fatal("local/qwen missing")
	}
	if !sameStrings(qwen.Tools, []string{"read", "grep", "ctx__*"}) || !sameStrings(qwen.DisallowedTools, []string{"write"}) {
		t.Fatalf("loaded lists = %q / %q, want trimmed entries without blanks", qwen.Tools, qwen.DisallowedTools)
	}
	if plain := cfg.FindModelEntry("local/plain"); plain == nil || len(plain.Tools) != 0 || len(plain.DisallowedTools) != 0 {
		t.Fatalf("a row without lists got %+v", plain)
	}

	// Settings reads the document as JSON and PUTs it back.
	raw, err := json.Marshal(ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"tools":["read","grep","ctx__*"]`, `"disallowed_tools":["write"]`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("the settings document lacks %s:\n%s", key, raw)
		}
	}
	back, err := ParseAndValidateConfigJSON(raw, cfg.Paths)
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}
	if e := back.FindModelEntry("local/qwen"); e == nil || !sameStrings(e.Tools, qwen.Tools) || !sameStrings(e.DisallowedTools, qwen.DisallowedTools) {
		t.Fatalf("the JSON round trip changed the lists: %+v", e)
	}
	if strings.Count(string(raw), `"tools":[`) != 1 {
		t.Fatalf("a row without lists carries the key in the settings document:\n%s", raw)
	}

	// And the file the save writes.
	out, err := MarshalConfigYAML(back)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "tools:") || !strings.Contains(text, "disallowed_tools:") {
		t.Fatalf("the saved YAML dropped a list:\n%s", text)
	}
	if strings.Count(text, "tools:") != 2 { // tools: and disallowed_tools: of the one row
		t.Fatalf("a row without lists was given the keys:\n%s", text)
	}
	again := loadModelToolsConfig(t, text)
	if e := again.FindModelEntry("local/qwen"); e == nil || !sameStrings(e.Tools, qwen.Tools) || !sameStrings(e.DisallowedTools, qwen.DisallowedTools) {
		t.Fatalf("the written YAML does not load back to the same lists: %+v", e)
	}
}

// The self-configuration commands the configure-coddy skill documents stage
// and commit the lists, and a bad shape is refused at staging time.
func TestModelToolListsCanBeStagedAsUCICommands(t *testing.T) {
	paths := testPathConfig(t, withModeline("providers:\n  - name: local\n    type: openai\nmodels:\n  - model: local/qwen\nagent:\n  model: local/qwen\n"))
	cmds := mustParseUCI(t,
		`set models[model=local/qwen].tools=["read","grep","edit"]`,
		`add_list models[model=local/qwen].disallowed_tools=edit`,
	)
	if _, err := CommitUCICommands(paths, cmds); err != nil {
		t.Fatalf("CommitUCICommands: %v", err)
	}
	cfg, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.FindModelEntry("local/qwen")
	if e == nil || !sameStrings(e.Tools, []string{"read", "grep", "edit"}) || !sameStrings(e.DisallowedTools, []string{"edit"}) {
		t.Fatalf("staged lists = %+v", e)
	}
	if err := DryRunUCICommands(paths, mustParseUCI(t, `set models[model=local/qwen].tools="read"`)); err == nil {
		t.Fatal("a string where the schema wants a list was accepted")
	}
}

// An explicit empty list is the same as none: it is not kept, and it
// restricts nothing.
func TestModelToolListsEmptyIsAbsent(t *testing.T) {
	cfg := loadModelToolsConfig(t, strings.Replace(modelToolsYAML, "    tools:\n      - \" read \"\n      - grep\n      - \"ctx__*\"\n    disallowed_tools: [write, \"  \"]\n", "    tools: []\n    disallowed_tools: [\" \"]\n", 1))
	e := cfg.FindModelEntry("local/qwen")
	if e == nil || len(e.Tools) != 0 || len(e.DisallowedTools) != 0 {
		t.Fatalf("empty lists loaded as %+v", e)
	}
	out, err := MarshalConfigYAML(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "tools:") {
		t.Fatalf("empty lists were written back:\n%s", out)
	}
}

func withToolCatalog(t *testing.T, names ...string) {
	t.Helper()
	RegisterToolCatalog(func() []string { return names })
	t.Cleanup(func() { RegisterToolCatalog(nil) })
}

func TestUnknownModelToolsNameEntriesThatMatchNothing(t *testing.T) {
	withToolCatalog(t, "read", "write", "grep", "coddy_docs_read", "coddy_docs_search")
	cfg := &Config{Models: []ModelEntry{
		{Model: "local/qwen",
			Tools:           []string{"read", "grep", "Read", "coddy_docs_*", "nothing_*", "ctx__*", "srv__tool", "*"},
			DisallowedTools: []string{"wirte", "write"}},
		{Model: "local/plain"},
	}}
	var got []string
	for _, u := range cfg.UnknownModelTools() {
		got = append(got, u.Path()+" "+u.Name)
	}
	want := []string{"models[local/qwen].tools Read", "models[local/qwen].tools nothing_*", "models[local/qwen].disallowed_tools wirte"}
	if !sameStrings(got, want) {
		t.Fatalf("unknown = %q, want %q", got, want)
	}

	var buf bytes.Buffer
	cfg.LogUnknownModelTools(slog.New(slog.NewTextHandler(&buf, nil)))
	if lines := strings.Count(buf.String(), "\n"); lines != 3 || !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "tool=wirte") {
		t.Fatalf("startup log = %q, want a warning per unknown entry", buf.String())
	}

	// Without a catalog nothing can be called unknown.
	RegisterToolCatalog(nil)
	if got := cfg.UnknownModelTools(); len(got) != 0 {
		t.Fatalf("no catalog, yet unknown = %v", got)
	}
}

// coddy -t places the warning on the entry's own line, with the fix and the
// schema description, and does not fail the file.
func TestModelToolCheckWarnsAboutAnUnknownName(t *testing.T) {
	withToolCatalog(t, "read", "write", "grep")
	src := withModeline(modelToolsYAML)
	src = strings.Replace(src, `      - grep`, `      - gerp`, 1)
	rep := checkYAML(t, src)
	if !rep.Valid() {
		t.Fatalf("an unknown tool name failed the check: %+v", rep.Findings)
	}
	var hit *Finding
	for _, f := range warningsOf(rep) {
		if strings.Contains(f.Message, `"gerp"`) {
			cp := f
			hit = &cp
		}
	}
	if hit == nil {
		t.Fatalf("no warning about gerp in %+v", rep.Findings)
	}
	wantLine := 0
	for i, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "gerp") {
			wantLine = i + 1
		}
	}
	if hit.Line != wantLine || hit.Path != "models[local/qwen].tools" {
		t.Fatalf("finding at line %d path %q, want line %d, models[local/qwen].tools", hit.Line, hit.Path, wantLine)
	}
	if !strings.Contains(hit.Fix, "tool names") || !strings.Contains(hit.Doc, "Allowlist of the tools") {
		t.Fatalf("finding = %+v, want a fix and the schema description", *hit)
	}
	// ctx__* and the entries that are real tools are not reported.
	for _, f := range warningsOf(rep) {
		if strings.Contains(f.Message, "ctx__") || strings.Contains(f.Message, `"read"`) || strings.Contains(f.Message, `"write"`) {
			t.Fatalf("a known entry was reported: %+v", f)
		}
	}
}

// The schema the check validates against accepts the keys and rejects a list of
// the wrong shape.
func TestModelToolCheckValidatesTheListShape(t *testing.T) {
	good := checkYAML(t, withModeline(modelToolsYAML))
	if !good.Valid() {
		t.Fatalf("a valid model row failed: %+v", good.Findings)
	}
	bad := checkYAML(t, withModeline(strings.Replace(modelToolsYAML, "    disallowed_tools: [write, \"  \"]\n", "    disallowed_tools: write\n", 1)))
	if bad.Valid() {
		t.Fatalf("a string where a list belongs passed: %+v", bad.Findings)
	}
}
