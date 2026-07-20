package fs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// --- patch.go: unified diff / v4a patch ------------------------------------

func TestApplyUnifiedDiff_hunkZeroOriginDoesNotPanic(t *testing.T) {
	t.Parallel()
	original := ""
	diff := strings.Join([]string{
		"--- a/file.txt",
		"+++ b/file.txt",
		"@@ -0,0 +1,2 @@",
		"+line one",
		"+line two",
	}, "\n")

	got, err := applyUnifiedDiff(original, diff)
	if err != nil {
		t.Fatalf("applyUnifiedDiff: %v", err)
	}
	want := "line one\nline two"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplyUnifiedDiff_replaceMiddleLine(t *testing.T) {
	t.Parallel()
	original := "line1\nline2\nline3\n"
	diff := "@@ -2,1 +2,1 @@\n-line2\n+newline2\n"

	got, err := applyUnifiedDiff(original, diff)
	if err != nil {
		t.Fatalf("applyUnifiedDiff: %v", err)
	}
	want := "line1\nnewline2\nline3"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplyV4APatch_codexEnvelope(t *testing.T) {
	t.Parallel()
	original := strings.Join([]string{
		"import splunklib.client as client",
		"import splunklib.results as results",
		"",
		"def splunk_search():",
		"    pass",
	}, "\n")
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: project/src/api/auto_uw_api.py",
		"@@",
		"-import splunklib.client as client",
		"-import splunklib.results as results",
		"+try:",
		"+    import splunklib.client as client  # type: ignore",
		"+    import splunklib.results as results  # type: ignore",
		"+except Exception:",
		"+    client = None",
		"+    results = None",
		"*** End Patch",
	}, "\n")

	got, err := applyPatch(original, patch)
	if err != nil {
		t.Fatalf("applyPatch: %v", err)
	}
	if !strings.Contains(got, "try:") || !strings.Contains(got, "splunklib.client as client  # type: ignore") {
		t.Fatalf("patch not applied: %q", got)
	}
	if strings.Contains(got, "\nimport splunklib.client as client\n") {
		t.Fatalf("old imports should be removed: %q", got)
	}
}

func TestApplyV4APatch_bareHunkHeader(t *testing.T) {
	t.Parallel()
	original := "alpha\nbeta\ngamma\n"
	patch := "@@\n-beta\n+BETA\n"

	got, err := applyPatch(original, patch)
	if err != nil {
		t.Fatalf("applyPatch: %v", err)
	}
	want := "alpha\nBETA\ngamma"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplyUnifiedDiff_deleteOutOfRangeReturnsError(t *testing.T) {
	t.Parallel()
	original := "only\n"
	diff := "@@ -5,1 +5,0 @@\n-gone\n"

	_, err := applyUnifiedDiff(original, diff)
	if err == nil {
		t.Fatal("expected error for delete out of range")
	}
}

// --- paths.go / grep.go / glob.go: session-store hiding --------------------

// buildStoreTree lays out a workspace that also contains Coddy's own session store,
// mirroring the real ~/.coddy layout where config.yaml sits next to sessions/<id>/.
// It returns the root and the active session dir.
func buildStoreTree(t *testing.T) (root, sessionDir string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("provider: moonshot\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionDir = filepath.Join(root, "sessions", "sess_other")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Another session's transcript that mentions both the search term and an unrelated task.
	leak := "moonshot reference and find lyrics for another session\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "messages.json"), []byte(leak), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, sessionDir
}

func TestSessionStoreRootAndIsWithinDir(t *testing.T) {
	if got := sessionStoreRoot(""); got != "" {
		t.Fatalf("empty SessionDir should disable filtering, got %q", got)
	}
	root := sessionStoreRoot(filepath.FromSlash("/home/u/.coddy/sessions/sess_abc"))
	if root != filepath.FromSlash("/home/u/.coddy/sessions") {
		t.Fatalf("store root = %q", root)
	}
	if !isWithinDir(filepath.FromSlash("/home/u/.coddy/sessions/sess_x/messages.json"), root) {
		t.Fatal("store file should be within store root")
	}
	if isWithinDir(filepath.FromSlash("/home/u/.coddy/config.yaml"), root) {
		t.Fatal("sibling config must not be treated as within the store")
	}
	if isWithinDir(filepath.FromSlash("/home/u/.coddy/sessions-archive/x"), root) {
		t.Fatal("prefix-similar sibling dir must not match")
	}
}

func TestDropStoreLinesKeepsNonPathLines(t *testing.T) {
	// No SessionDir → unchanged.
	if got := dropStoreLines("a\nb", ""); got != "a\nb" {
		t.Fatalf("expected passthrough, got %q", got)
	}
	in := "/work/sessions/sess_x/messages.json:1:leak\n/work/main.go:2:keep\nno matches found"
	out := dropStoreLines(in, "/work/sessions")
	if strings.Contains(out, "messages.json") {
		t.Fatalf("store line not dropped: %q", out)
	}
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "no matches found") {
		t.Fatalf("non-store lines must be kept: %q", out)
	}
}

func TestGrepLineFilePathWindowsDrive(t *testing.T) {
	line := `C:\work\src\main.go:42:func main() {}`
	if got := grepLineFilePath(line); got != `C:\work\src\main.go` {
		t.Fatalf("grepLineFilePath() = %q", got)
	}
}

func TestGrepHidesSessionStore(t *testing.T) {
	root, sessionDir := buildStoreTree(t)
	env := &tooling.Env{CWD: root, SessionDir: sessionDir}

	args, _ := json.Marshal(map[string]any{"pattern": "moonshot", "path": root})
	out, err := executeGrep(context.Background(), string(args), env)
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(out, "config.yaml") {
		t.Fatalf("expected a real config.yaml match, got: %q", out)
	}
	if strings.Contains(out, "messages.json") || strings.Contains(out, "find lyrics") {
		t.Fatalf("session store leaked into grep results: %q", out)
	}
}

func TestGlobHidesSessionStore(t *testing.T) {
	root, sessionDir := buildStoreTree(t)
	env := &tooling.Env{CWD: root, SessionDir: sessionDir}

	args, _ := json.Marshal(map[string]any{"pattern": "**/*.json", "path": root})
	out, err := executeGlob(context.Background(), string(args), env)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if !strings.Contains(out, "data.json") {
		t.Fatalf("expected the real data.json file, got: %q", out)
	}
	if strings.Contains(out, "messages.json") {
		t.Fatalf("session store leaked into glob results: %q", out)
	}
}

// --- grep.go / search.go: portable content search ---------------------------

func TestGrepNativeFallbackSearch(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, filepath.Join(root, "words.txt"), "farm\nfirm\nform\nfoam\n")
	writeSearchFixture(t, filepath.Join(root, "skip.md"), "farm\n")

	args, _ := json.Marshal(map[string]any{
		"pattern":     `^f(a|i|o)rm$`,
		"path":        root,
		"glob":        "**/*.txt",
		"max_results": 10,
	})
	out, err := executeGrepWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, nativeOnlyGrepRunner())
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	for _, want := range []string{"words.txt:1:farm", "words.txt:2:firm", "words.txt:3:form"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output does not contain %q: %q", want, out)
		}
	}
	if strings.Contains(out, "foam") || strings.Contains(out, "skip.md") {
		t.Fatalf("unexpected match: %q", out)
	}
}

func TestGrepNativeFallbackSupportsCommonEscapes(t *testing.T) {
	// \d, \s, \w and friends are what models actually emit; the built-in
	// engine must accept them just like ripgrep does.
	root := t.TempDir()
	writeSearchFixture(t, filepath.Join(root, "main.go"), "func main() {\n\tport := 8080\n}\n")

	args, _ := json.Marshal(map[string]any{"pattern": `\w+ := \d+`, "path": root})
	out, err := executeGrepWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, nativeOnlyGrepRunner())
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(out, "main.go:2:") {
		t.Fatalf("expected a \\d/\\w match, got: %q", out)
	}
}

func TestGrepNativeFallbackCaseInsensitiveAndLimited(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, filepath.Join(root, "a.txt"), "Alpha\nALPHA\nalpha\n")
	args, _ := json.Marshal(map[string]any{
		"pattern":     `^alpha$`,
		"path":        root,
		"max_results": 2,
	})

	out, err := executeGrepWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, nativeOnlyGrepRunner())
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if got := len(strings.Split(strings.TrimSpace(out), "\n")); got != 2 {
		t.Fatalf("result count = %d, want 2: %q", got, out)
	}
}

func TestGrepNativeFallbackRejectsInvalidPattern(t *testing.T) {
	root := t.TempDir()
	args, _ := json.Marshal(map[string]any{"pattern": `foo(`, "path": root})
	_, err := executeGrepWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, nativeOnlyGrepRunner())
	if err == nil || !strings.Contains(err.Error(), "invalid regular expression") {
		t.Fatalf("error = %v, want invalid regular expression", err)
	}
}

func TestGrepPassesPatternToSystemRipgrepUntouched(t *testing.T) {
	root := t.TempDir()
	args, _ := json.Marshal(map[string]any{"pattern": `\d+`, "path": root})
	var executable string
	var gotArgs []string
	runner := grepRunner{
		lookPath: func(name string) (string, error) {
			if name != "rg" {
				t.Fatalf("lookPath(%q), want rg", name)
			}
			return "/tools/rg", nil
		},
		run: func(_ context.Context, exe string, cmdArgs []string) (string, int, error) {
			executable = exe
			gotArgs = cmdArgs
			return filepath.Join(root, "words.txt") + ":1:42\n", 0, nil
		},
	}

	out, err := executeGrepWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, runner)
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if executable != "/tools/rg" || !strings.Contains(out, "42") {
		t.Fatalf("system backend not used: executable=%q output=%q", executable, out)
	}
	// The pattern must reach ripgrep as-is, after a "--" separator so leading
	// dashes cannot be parsed as flags.
	sep := -1
	for i, a := range gotArgs {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+1 >= len(gotArgs) || gotArgs[sep+1] != `\d+` {
		t.Fatalf("pattern not passed through after --: %v", gotArgs)
	}
}

func TestGrepFallsBackWhenRipgrepDisappears(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, filepath.Join(root, "a.txt"), "needle\n")
	args, _ := json.Marshal(map[string]any{"pattern": "needle", "path": root})
	runner := grepRunner{
		lookPath: func(string) (string, error) { return "/tools/rg", nil },
		run: func(context.Context, string, []string) (string, int, error) {
			return "", -1, errors.New("binary vanished")
		},
	}

	out, err := executeGrepWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, runner)
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(out, "a.txt:1:needle") {
		t.Fatalf("native fallback not used: %q", out)
	}
}

func TestNativeGlobSupportsDoubleStarWithoutRipgrep(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "nested", "file.go")
	writeSearchFixture(t, want, "package nested\n")
	writeSearchFixture(t, filepath.Join(root, "file.txt"), "not go\n")

	paths, err := nativeGlob(context.Background(), root, "**/*.go", "")
	if err != nil {
		t.Fatalf("nativeGlob: %v", err)
	}
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("paths = %#v, want %#v", paths, []string{want})
	}
}

func TestNativeGlobSupportsDoublestarAlternatives(t *testing.T) {
	root := t.TempDir()
	goFile := filepath.Join(root, "nested", "file.go")
	markdownFile := filepath.Join(root, "README.md")
	writeSearchFixture(t, goFile, "package nested\n")
	writeSearchFixture(t, markdownFile, "# Readme\n")
	writeSearchFixture(t, filepath.Join(root, "notes.txt"), "notes\n")

	paths, err := nativeGlob(context.Background(), root, "**/*.{go,md}", "")
	if err != nil {
		t.Fatalf("nativeGlob: %v", err)
	}
	if len(paths) != 2 || paths[0] != markdownFile || paths[1] != goFile {
		t.Fatalf("paths = %#v, want %#v", paths, []string{markdownFile, goFile})
	}
}

func nativeOnlyGrepRunner() grepRunner {
	return grepRunner{
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		run: func(context.Context, string, []string) (string, int, error) {
			return "", 0, errors.New("must not run")
		},
	}
}

func writeSearchFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
