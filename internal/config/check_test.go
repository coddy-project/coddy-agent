package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkYAML writes body as <home>/config.yaml and checks it the way -t does.
func checkYAML(t *testing.T, body string) *CheckReport {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Check(CLIPaths{Home: home, Config: path})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return rep
}

// withModeline prefixes a fixture with the schema header, so the tests below
// see only the finding they are about.
func withModeline(body string) string { return SchemaModeline() + "\n" + body }

func errorsOf(rep *CheckReport) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if f.Severity == SeverityError {
			out = append(out, f)
		}
	}
	return out
}

func warningsOf(rep *CheckReport) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if f.Severity == SeverityWarning {
			out = append(out, f)
		}
	}
	return out
}

func onlyError(t *testing.T, rep *CheckReport) Finding {
	t.Helper()
	errs := errorsOf(rep)
	if len(errs) != 1 {
		t.Fatalf("want exactly one error, got %d: %+v", len(errs), rep.Findings)
	}
	return errs[0]
}

func TestCheckAcceptsAValidConfig(t *testing.T) {
	rep := checkYAML(t, withModeline(`providers:
  - name: local
    type: openai
    api_base: http://127.0.0.1:11434/v1
models:
  - model: local/qwen
    max_tokens: 4096
    stream: null
agent:
  model: local/qwen
httpserver:
  port:
compaction:
  enable: null
`))
	if !rep.Valid() || len(rep.Findings) != 0 {
		t.Fatalf("a valid config must produce no findings, got %+v", rep.Findings)
	}
}

func TestCheckUnknownKeySuggestsTheClosestOne(t *testing.T) {
	rep := checkYAML(t, withModeline("httpserver:\n  host: 127.0.0.1\n  enabled: true\n"))
	f := onlyError(t, rep)
	if f.Line != 4 || f.Column != 3 {
		t.Errorf("position %d:%d, want 4:3", f.Line, f.Column)
	}
	if f.Path != "httpserver.enabled" {
		t.Errorf("path %q, want httpserver.enabled", f.Path)
	}
	if !strings.Contains(f.Message, `unknown key "enabled"`) {
		t.Errorf("message %q does not name the key", f.Message)
	}
	if !strings.Contains(f.Fix, `did you mean "enable"`) {
		t.Errorf("fix %q does not suggest enable", f.Fix)
	}
	if !strings.Contains(f.Fix, "allow_insecure") || !strings.Contains(f.Fix, "remotes") {
		t.Errorf("fix %q does not list the keys allowed under httpserver", f.Fix)
	}
}

func TestCheckUnknownKeyInsideAListEntry(t *testing.T) {
	rep := checkYAML(t, withModeline("providers:\n  - name: a\n    type: openai\n  - name: b\n    type: openai\n    api_bas: x\n"))
	f := onlyError(t, rep)
	if f.Path != "providers[1].api_bas" {
		t.Errorf("path %q, want providers[1].api_bas", f.Path)
	}
	if f.Line != 7 {
		t.Errorf("line %d, want 7", f.Line)
	}
	if !strings.Contains(f.Fix, `did you mean "api_base"`) {
		t.Errorf("fix %q", f.Fix)
	}
}

func TestCheckEnumListsTheAllowedValues(t *testing.T) {
	rep := checkYAML(t, withModeline("logger:\n  level: verbose\n"))
	f := onlyError(t, rep)
	if f.Line != 3 || f.Column != 10 {
		t.Errorf("position %d:%d, want 3:10", f.Line, f.Column)
	}
	if !strings.Contains(f.Message, `"verbose"`) {
		t.Errorf("message %q does not quote the value", f.Message)
	}
	if !strings.Contains(f.Fix, "debug, info, warn, warning, error") {
		t.Errorf("fix %q does not list the allowed values", f.Fix)
	}
	if !strings.Contains(f.Doc, "Minimum severity") {
		t.Errorf("doc %q does not carry the schema description", f.Doc)
	}
}

func TestCheckEnumSuggestsTheClosestValue(t *testing.T) {
	rep := checkYAML(t, withModeline("tools:\n  permission_mode: bypas\n"))
	f := onlyError(t, rep)
	if !strings.Contains(f.Fix, `did you mean "bypass"`) {
		t.Errorf("fix %q", f.Fix)
	}
}

func TestCheckWrongTypeExplainsTheFix(t *testing.T) {
	t.Run("quoted number", func(t *testing.T) {
		rep := checkYAML(t, withModeline("agent:\n  max_turns: \"40\"\n"))
		f := onlyError(t, rep)
		if !strings.Contains(f.Message, "integer") || !strings.Contains(f.Message, `"40"`) {
			t.Errorf("message %q", f.Message)
		}
		if !strings.Contains(f.Fix, "quotes") {
			t.Errorf("fix %q should tell the operator to drop the quotes", f.Fix)
		}
	})
	t.Run("word for a number", func(t *testing.T) {
		rep := checkYAML(t, withModeline("agent:\n  max_turns: many\n"))
		f := onlyError(t, rep)
		if !strings.Contains(f.Fix, "max_turns: 30") {
			t.Errorf("fix %q should show the schema default as an example", f.Fix)
		}
	})
	t.Run("string for a boolean", func(t *testing.T) {
		rep := checkYAML(t, withModeline("subagents:\n  enable: enabled\n"))
		f := onlyError(t, rep)
		if !strings.Contains(f.Message, "boolean") {
			t.Errorf("message %q", f.Message)
		}
		if !strings.Contains(f.Fix, "true or false") {
			t.Errorf("fix %q", f.Fix)
		}
	})
	t.Run("list for a string", func(t *testing.T) {
		rep := checkYAML(t, withModeline("agent:\n  model: [a/b]\n"))
		f := onlyError(t, rep)
		if !strings.Contains(f.Message, "string") || !strings.Contains(f.Message, "list") {
			t.Errorf("message %q", f.Message)
		}
	})
}

func TestCheckYAML11BooleansAreAWarning(t *testing.T) {
	rep := checkYAML(t, withModeline("subagents:\n  enable: yes\n"))
	if !rep.Valid() {
		t.Fatalf("yes is read as a boolean by the loader, so it must not fail the check: %+v", rep.Findings)
	}
	warns := warningsOf(rep)
	if len(warns) != 1 {
		t.Fatalf("want one warning, got %+v", rep.Findings)
	}
	if warns[0].Line != 3 || !strings.Contains(warns[0].Fix, "true or false") {
		t.Errorf("warning %+v", warns[0])
	}
}

func TestCheckIntegralFloatForAnIntegerIsAWarning(t *testing.T) {
	rep := checkYAML(t, withModeline("agent:\n  max_turns: 40.0\n"))
	if !rep.Valid() || len(warningsOf(rep)) != 1 {
		t.Fatalf("40.0 loads as 40; want a warning only, got %+v", rep.Findings)
	}
	if got := errorsOf(checkYAML(t, withModeline("agent:\n  max_turns: 40.5\n"))); len(got) != 1 {
		t.Fatalf("40.5 cannot become an integer; want an error, got %+v", got)
	}
}

func TestCheckMissingRequiredKey(t *testing.T) {
	rep := checkYAML(t, withModeline("providers:\n  - name: a\n    type: openai\n  - name: b\n    api_base: http://x\n"))
	f := onlyError(t, rep)
	if f.Line != 5 {
		t.Errorf("line %d, want the line of the entry (5)", f.Line)
	}
	if f.Path != "providers[1]" {
		t.Errorf("path %q, want providers[1]", f.Path)
	}
	if !strings.Contains(f.Message, `missing required key "type"`) {
		t.Errorf("message %q", f.Message)
	}
	if !strings.Contains(f.Fix, "openai") || !strings.Contains(f.Fix, "anthropic") {
		t.Errorf("fix %q should list the values type takes", f.Fix)
	}
}

func TestCheckPatternViolation(t *testing.T) {
	rep := checkYAML(t, withModeline("providers:\n  - name: 1bad\n    type: openai\n"))
	f := onlyError(t, rep)
	if f.Path != "providers[0].name" || f.Line != 3 {
		t.Errorf("finding %+v", f)
	}
	if !strings.Contains(f.Message, "1bad") {
		t.Errorf("message %q does not quote the value", f.Message)
	}
	if !strings.Contains(f.Doc, "ASCII letters") {
		t.Errorf("doc %q should explain the allowed shape", f.Doc)
	}
}

func TestCheckMinimumAndMaximum(t *testing.T) {
	high := onlyError(t, checkYAML(t, withModeline("compaction:\n  threshold_percent: 150\n")))
	if !strings.Contains(high.Message, "at most 100") {
		t.Errorf("message %q", high.Message)
	}
	low := onlyError(t, checkYAML(t, withModeline("compaction:\n  threshold_percent: 0\n")))
	if !strings.Contains(low.Message, "at least 1") {
		t.Errorf("message %q", low.Message)
	}
}

func TestCheckDuplicateKeyIsReported(t *testing.T) {
	rep := checkYAML(t, withModeline("httpserver:\n  port: 1\n  host: x\n  port: 2\n"))
	f := onlyError(t, rep)
	if f.Line != 5 {
		t.Errorf("line %d, want 5 (the second port)", f.Line)
	}
	if !strings.Contains(f.Message, `duplicate key "port"`) || !strings.Contains(f.Message, "line 3") {
		t.Errorf("message %q should name the key and the first definition", f.Message)
	}
}

func TestCheckYAMLSyntaxErrorCarriesTheLine(t *testing.T) {
	rep := checkYAML(t, withModeline("agent:\n\tmax_turns: 3\n"))
	f := onlyError(t, rep)
	if f.Line != 3 {
		t.Errorf("line %d, want 3", f.Line)
	}
	if !strings.Contains(strings.ToLower(f.Fix), "tab") {
		t.Errorf("fix %q should mention tabs", f.Fix)
	}
}

func TestCheckListExpected(t *testing.T) {
	rep := checkYAML(t, withModeline("skills:\n  dirs: ~/skills\n"))
	f := onlyError(t, rep)
	if !strings.Contains(f.Message, "list") {
		t.Errorf("message %q", f.Message)
	}
	if !strings.Contains(f.Fix, "- ") {
		t.Errorf("fix %q should show list syntax", f.Fix)
	}
}

func TestCheckObjectExpected(t *testing.T) {
	rep := checkYAML(t, withModeline("logger: debug\n"))
	f := onlyError(t, rep)
	if !strings.Contains(f.Message, "section") {
		t.Errorf("message %q", f.Message)
	}
	if !strings.Contains(f.Fix, "level") {
		t.Errorf("fix %q should name keys the section takes", f.Fix)
	}
}

func TestCheckLoaderErrorsGetALine(t *testing.T) {
	rep := checkYAML(t, withModeline(`providers:
  - name: local
    type: openai
models:
  - model: nope/qwen
agent:
  model: nope/qwen
`))
	f := onlyError(t, rep)
	if !strings.Contains(f.Message, `unknown provider "nope"`) {
		t.Errorf("message %q", f.Message)
	}
	if f.Line != 6 {
		t.Errorf("line %d, want 6 (the model that names the provider)", f.Line)
	}
	if !strings.Contains(f.Fix, "local") {
		t.Errorf("fix %q should name the providers that exist", f.Fix)
	}
}

func TestCheckLoaderErrorFallsBackToTheSection(t *testing.T) {
	rep := checkYAML(t, withModeline("logger:\n  outputs: [file]\n"))
	f := onlyError(t, rep)
	if !strings.Contains(f.Message, "logger.file") {
		t.Errorf("message %q", f.Message)
	}
	if f.Line != 2 {
		t.Errorf("line %d, want 2 (the logger section)", f.Line)
	}
}

func TestCheckMissingFileIsAnError(t *testing.T) {
	home := t.TempDir()
	rep, err := Check(CLIPaths{Home: home, CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	if rep.File != filepath.Join(home, "config.yaml") {
		t.Errorf("file %q", rep.File)
	}
	f := onlyError(t, rep)
	if !strings.Contains(f.Message, "not found") {
		t.Errorf("message %q", f.Message)
	}
	if !strings.Contains(f.Fix, "--config") || !strings.Contains(f.Fix, "--home") {
		t.Errorf("fix %q should name the flags that pick another file", f.Fix)
	}
}

func TestCheckFallsBackToTheWorkingDirectoryConfig(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	cwdCfg := filepath.Join(cwd, "config.yaml")
	if err := os.WriteFile(cwdCfg, []byte(withModeline("agent:\n  max_turns: 3\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Check(CLIPaths{Home: home, CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if rep.File != cwdCfg {
		t.Errorf("file %q, want the working directory fallback %q", rep.File, cwdCfg)
	}
	if !rep.Valid() {
		t.Errorf("findings %+v", rep.Findings)
	}
}

func TestCheckExpandsEnvironmentReferences(t *testing.T) {
	t.Setenv("CODDY_CHECK_TEST_PORT", "8080")
	rep := checkYAML(t, withModeline("httpserver:\n  port: ${CODDY_CHECK_TEST_PORT}\n"))
	if !rep.Valid() || len(rep.Findings) != 0 {
		t.Fatalf("an environment reference expands before the check: %+v", rep.Findings)
	}
}

func TestCheckRedactsSecretValues(t *testing.T) {
	rep := checkYAML(t, withModeline("swarm:\n  pairing_tokens: supersecret\n"))
	f := onlyError(t, rep)
	if strings.Contains(f.Message, "supersecret") || strings.Contains(f.Fix, "supersecret") {
		t.Errorf("a token must not be echoed: %+v", f)
	}
	// The rule is the loader's own notion of a secret, not a substring match:
	// max_tokens is a number the operator wants to see.
	f = onlyError(t, checkYAML(t, withModeline("providers:\n  - name: a\n    type: openai\nmodels:\n  - model: a/b\n    max_tokens: \"4096\"\nagent:\n  model: a/b\n")))
	if !strings.Contains(f.Message, `"4096"`) || !strings.Contains(f.Fix, "max_tokens: 4096") {
		t.Errorf("max_tokens is not a secret: %+v", f)
	}
}

func TestCheckWarnsWhenTheSchemaModelineIsMissing(t *testing.T) {
	rep := checkYAML(t, "agent:\n  max_turns: 3\n")
	if !rep.Valid() {
		t.Fatalf("a missing modeline is not an error: %+v", rep.Findings)
	}
	warns := warningsOf(rep)
	if len(warns) != 1 || !strings.Contains(warns[0].Fix, SchemaURL) {
		t.Fatalf("want one warning naming the schema URL, got %+v", rep.Findings)
	}
}

func TestCheckDoesNotRecoverFromTheBackup(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	broken := withModeline("agent:\n  max_turns: \"many\"\n")
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BackupPath(path), []byte("agent:\n  max_turns: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Check(CLIPaths{Home: home, Config: path})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid() {
		t.Fatal("the broken file must be reported, not replaced by the backup")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != broken {
		t.Fatalf("config.yaml was rewritten:\n%s", raw)
	}
}

func TestCheckReportText(t *testing.T) {
	rep := checkYAML(t, "httpserver:\n  enabled: true\n")
	var buf bytes.Buffer
	rep.Write(&buf)
	txt := buf.String()
	wantLine := rep.File + `:2:3: httpserver.enabled: unknown key "enabled"`
	if !strings.Contains(txt, wantLine) {
		t.Errorf("report lacks %q:\n%s", wantLine, txt)
	}
	if !strings.Contains(txt, "\n    fix: ") {
		t.Errorf("report lacks an indented fix line:\n%s", txt)
	}
	if !strings.Contains(txt, "warning: ") {
		t.Errorf("the missing modeline warning must be marked as such:\n%s", txt)
	}
	if !strings.HasSuffix(strings.TrimSpace(txt), rep.File+": 1 error, 1 warning") {
		t.Errorf("summary line missing or wrong:\n%s", txt)
	}
}

func TestRunCheckFailsOnErrorsOnly(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte("agent:\n  max_turns: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := RunCheck(&buf, CLIPaths{Home: home, Config: path}); err != nil {
		t.Fatalf("a warning-only report must not fail: %v\n%s", err, buf.String())
	}
	if err := os.WriteFile(path, []byte("agent:\n  max_turns: many\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := RunCheck(&buf, CLIPaths{Home: home, Config: path}); err == nil {
		t.Fatalf("an error must fail the run:\n%s", buf.String())
	}
}

func TestLoadReadOnlyWritesNothing(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, []byte("agent:\n  max_turns: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, raw, err := LoadReadOnly(CLIPaths{Home: home, Config: path})
	if err != nil {
		t.Fatalf("LoadReadOnly: %v", err)
	}
	if cfg.Agent.MaxTurns != 3 || !strings.Contains(string(raw), "max_turns: 3") {
		t.Fatalf("cfg %+v raw %q", cfg.Agent, raw)
	}
	if _, err := os.Stat(BackupPath(path)); !os.IsNotExist(err) {
		t.Fatalf("a read-only load must not write config.yaml.bak (stat err %v)", err)
	}

	broken := "agent:\n  max_turns: \"many\"\n"
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(BackupPath(path), []byte("agent:\n  max_turns: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadReadOnly(CLIPaths{Home: home, Config: path}); err == nil {
		t.Fatal("a broken file must fail instead of being recovered from the backup")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != broken {
		t.Fatalf("config.yaml was rewritten:\n%s", got)
	}
}

func TestLocatorResolvesPathsAndSelectors(t *testing.T) {
	doc := `providers:
  - name: a
    type: openai
  - name: b
    type: openai
    api_base: http://x
httpserver:
  port: 8080
skills:
  dirs: ["/one", "/two"]
`
	loc := NewLocator([]byte(doc))
	cases := map[string][2]int{
		"providers[b]":          {4, 5},
		"providers[1].api_base": {6, 15},
		"providers[0]":          {2, 5},
		"httpserver.port":       {8, 9},
		"skills.dirs[1]":        {10, 18},
		"httpserver":            {7, 1},
	}
	for path, want := range cases {
		line, col, ok := loc.Locate(path)
		if !ok || line != want[0] || col != want[1] {
			t.Errorf("%s: got %d:%d ok=%v, want %d:%d", path, line, col, ok, want[0], want[1])
		}
	}
	if _, _, ok := loc.Locate("providers[zzz]"); ok {
		t.Error("an entry that is not there must not resolve")
	}
	if _, _, ok := loc.Locate("logger.level"); ok {
		t.Error("a key that is not there must not resolve")
	}
	var nilLoc *Locator
	if _, _, ok := nilLoc.Locate("httpserver"); ok {
		t.Error("a nil locator resolves nothing")
	}
}
