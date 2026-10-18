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

const toolsetsYAML = `providers:
  - name: local
    type: openai
    api_base: http://127.0.0.1:8080/v1
models:
  - model: local/qwen
    max_context_tokens: 49152
    toolset: coding
  - model: local/plain
toolsets:
  - name: coding
    tools:
      - " read "
      - grep
      - "ctx__*"
    disallowed_tools: [write, "  "]
  - name: no-net
    disallowed_tools: [webfetch]
agent:
  model: local/qwen
`

func loadToolsetsConfig(t *testing.T, body string) *Config {
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

// The presets load trimmed, survive the settings editor's JSON round trip and
// the YAML written back, and a model without a toolset gets no key at all.
func TestToolsetsSurviveEveryRoundTrip(t *testing.T) {
	cfg := loadToolsetsConfig(t, toolsetsYAML)
	coding := cfg.FindToolset("coding")
	if coding == nil || !sameStrings(coding.Tools, []string{"read", "grep", "ctx__*"}) || !sameStrings(coding.DisallowedTools, []string{"write"}) {
		t.Fatalf("loaded preset = %+v, want trimmed entries without blanks", coding)
	}
	if m := cfg.FindModelEntry("local/qwen"); m == nil || m.Toolset != "coding" {
		t.Fatalf("local/qwen = %+v, want toolset coding", m)
	}

	// Settings reads the document as JSON and PUTs it back.
	raw, err := json.Marshal(ConfigToJSONDTO(cfg))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"toolset":"coding"`, `{"name":"coding","tools":["read","grep","ctx__*"],"disallowed_tools":["write"]}`, `{"name":"no-net","disallowed_tools":["webfetch"]}`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("the settings document lacks %s:\n%s", key, raw)
		}
	}
	if strings.Count(string(raw), `"toolset":`) != 1 {
		t.Fatalf("a model without a toolset carries the key in the settings document:\n%s", raw)
	}
	back, err := ParseAndValidateConfigJSON(raw, cfg.Paths)
	if err != nil {
		t.Fatalf("ParseAndValidateConfigJSON: %v", err)
	}
	if ts := back.FindToolset("coding"); ts == nil || !sameStrings(ts.Tools, coding.Tools) || !sameStrings(ts.DisallowedTools, coding.DisallowedTools) {
		t.Fatalf("the JSON round trip changed the preset: %+v", ts)
	}

	// And the file the save writes.
	out, err := MarshalConfigYAML(back)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "toolsets:") || strings.Count(text, "toolset: coding") != 1 {
		t.Fatalf("the saved YAML lost the presets or the model's toolset:\n%s", text)
	}
	again := loadToolsetsConfig(t, text)
	if ts := again.FindToolset("coding"); ts == nil || !sameStrings(ts.Tools, coding.Tools) || again.FindModelEntry("local/qwen").Toolset != "coding" {
		t.Fatalf("the written YAML does not load back to the same presets: %+v", ts)
	}
}

// The self-configuration commands the configure-coddy skill documents add a
// preset and point a model at it.
func TestToolsetsCanBeStagedAsUCICommands(t *testing.T) {
	paths := testPathConfig(t, withModeline("providers:\n  - name: local\n    type: openai\nmodels:\n  - model: local/qwen\nagent:\n  model: local/qwen\n"))
	cmds := mustParseUCI(t,
		`add_list toolsets={"name":"coding","tools":["read","grep","edit"]}`,
		`set models[model=local/qwen].toolset=coding`,
	)
	if _, err := CommitUCICommands(paths, cmds); err != nil {
		t.Fatalf("CommitUCICommands: %v", err)
	}
	cfg, err := LoadWithPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	if ts := cfg.FindToolset("coding"); ts == nil || !sameStrings(ts.Tools, []string{"read", "grep", "edit"}) {
		t.Fatalf("staged preset = %+v", ts)
	}
	if m := cfg.FindModelEntry("local/qwen"); m == nil || m.Toolset != "coding" {
		t.Fatalf("staged model = %+v", m)
	}
	if err := DryRunUCICommands(paths, mustParseUCI(t, `set models[model=local/qwen].toolset=nothing`)); err == nil {
		t.Fatal("a model naming a toolset that does not exist was accepted")
	}
}

// A preset is named once, never default, and a model names one that exists.
func TestToolsetsValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(string) string
		want string
	}{
		{"a duplicate name", func(s string) string { return strings.Replace(s, "name: no-net", "name: coding", 1) }, `duplicate name "coding"`},
		{"the reserved name", func(s string) string { return strings.Replace(s, "name: no-net", "name: default", 1) }, "reserved"},
		{"a name with a space", func(s string) string { return strings.Replace(s, "name: no-net", "name: no net", 1) }, "letters, digits"},
		{"an unknown toolset on a model", func(s string) string { return strings.Replace(s, "toolset: coding", "toolset: codding", 1) }, `"codding": not found in toolsets (configured: coding, no-net)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.edit(toolsetsYAML)), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadFromCLI(CLIPaths{Config: path})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("load error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// What a session on a model is offered by: its own choice of a preset, every
// tool for default, else the toolset its model names.
func TestResolveToolset(t *testing.T) {
	cfg := loadToolsetsConfig(t, toolsetsYAML+"  # a preset with nothing in it\n")
	cfg.Toolsets = append(cfg.Toolsets, Toolset{Name: "empty"})
	name := func(ts *Toolset) string {
		if ts == nil {
			return "<none>"
		}
		return ts.Name
	}
	for _, tc := range []struct {
		model, choice, want string
	}{
		{"local/qwen", "", "coding"},
		{"local/qwen", "no-net", "no-net"},
		{"local/qwen", "default", "<none>"},
		{"local/qwen", "deleted-since", "coding"},
		{"local/plain", "", "<none>"},
		{"local/plain", "coding", "coding"},
		{"local/plain", "empty", "<none>"},
		{"unknown/model", "", "<none>"},
	} {
		if got := name(cfg.ResolveToolset(tc.model, tc.choice)); got != tc.want {
			t.Errorf("ResolveToolset(%s, %q) = %s, want %s", tc.model, tc.choice, got, tc.want)
		}
	}
	if got := strings.Join(cfg.ToolsetNames(), ","); got != "coding,no-net,empty" {
		t.Errorf("ToolsetNames = %s", got)
	}
	for choice, want := range map[string]bool{"default": true, "coding": true, "": false, "nothing": false} {
		if got := cfg.ValidToolsetChoice(choice); got != want {
			t.Errorf("ValidToolsetChoice(%q) = %v, want %v", choice, got, want)
		}
	}
}

func withToolCatalog(t *testing.T, names ...string) {
	t.Helper()
	RegisterToolCatalog(func() []string { return names })
	t.Cleanup(func() { RegisterToolCatalog(nil) })
}

func TestUnknownToolsetToolsNameEntriesThatMatchNothing(t *testing.T) {
	withToolCatalog(t, "read", "write", "grep", "coddy_docs_read", "coddy_docs_search")
	cfg := &Config{Toolsets: []Toolset{
		{Name: "coding",
			Tools:           []string{"read", "grep", "Read", "coddy_docs_*", "nothing_*", "ctx__*", "srv__tool", "*"},
			DisallowedTools: []string{"wirte", "write"}},
		{Name: "all"},
	}}
	var got []string
	for _, u := range cfg.UnknownToolsetTools() {
		got = append(got, u.Path()+" "+u.Name)
	}
	want := []string{"toolsets[coding].tools Read", "toolsets[coding].tools nothing_*", "toolsets[coding].disallowed_tools wirte"}
	if !sameStrings(got, want) {
		t.Fatalf("unknown = %q, want %q", got, want)
	}

	var buf bytes.Buffer
	cfg.LogUnknownToolsetTools(slog.New(slog.NewTextHandler(&buf, nil)))
	if lines := strings.Count(buf.String(), "\n"); lines != 3 || !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "tool=wirte") {
		t.Fatalf("startup log = %q, want a warning per unknown entry", buf.String())
	}

	// Without a catalog nothing can be called unknown.
	RegisterToolCatalog(nil)
	if got := cfg.UnknownToolsetTools(); len(got) != 0 {
		t.Fatalf("no catalog, yet unknown = %v", got)
	}
}

// coddy -t places the warning on the entry's own line, with the fix and the
// schema description, and does not fail the file.
func TestToolsetCheckWarnsAboutAnUnknownName(t *testing.T) {
	withToolCatalog(t, "read", "write", "grep", "webfetch")
	src := withModeline(toolsetsYAML)
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
	if hit.Line != wantLine || hit.Path != "toolsets[coding].tools" {
		t.Fatalf("finding at line %d path %q, want line %d, toolsets[coding].tools", hit.Line, hit.Path, wantLine)
	}
	if !strings.Contains(hit.Fix, "tool names") || !strings.Contains(hit.Doc, "Allowlist") {
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
// the wrong shape and a key a preset does not have.
func TestToolsetCheckValidatesTheShape(t *testing.T) {
	good := checkYAML(t, withModeline(toolsetsYAML))
	if !good.Valid() {
		t.Fatalf("a valid configuration failed: %+v", good.Findings)
	}
	for _, bad := range []string{
		strings.Replace(toolsetsYAML, "    disallowed_tools: [write, \"  \"]\n", "    disallowed_tools: write\n", 1),
		strings.Replace(toolsetsYAML, "  - name: no-net\n", "  - name: no-net\n    model: local/qwen\n", 1),
		strings.Replace(toolsetsYAML, "    toolset: coding\n", "    tools: [read]\n", 1),
	} {
		if rep := checkYAML(t, withModeline(bad)); rep.Valid() {
			t.Fatalf("a malformed configuration passed:\n%s", bad)
		}
	}
}
