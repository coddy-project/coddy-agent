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

func TestRGToolNativeFallbackSupportsPOSIXExpressions(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, filepath.Join(root, "words.txt"), "farm\nfirm\nform\nfoam\n")
	writeSearchFixture(t, filepath.Join(root, "skip.md"), "farm\n")

	args, _ := json.Marshal(map[string]any{
		"pattern":     `^f(a|i|o)rm$`,
		"path":        root,
		"glob":        "**/*.txt",
		"max_results": 10,
	})
	out, err := executeRGToolWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, nativeOnlyRGRunner())
	if err != nil {
		t.Fatalf("rg_tool: %v", err)
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

func TestPOSIXMatcherSyntaxExamples(t *testing.T) {
	tests := []struct {
		pattern string
		match   string
		reject  string
	}{
		{`f.rm`, "farm", "frm"},
		{`to*`, "too", "ago"},
		{`to+`, "too", "t"},
		{`too?`, "to", "t"},
		{`f(a|i|o)t`, "fit", "feet"},
		{`f[io]rm`, "firm", "farm"},
		{`f[^io]rm`, "farm", "firm"},
		{`^f[aio]rm$`, "form", "former"},
		{`Comin\?`, "Comin?", "Coming"},
	}
	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			matcher, err := compilePOSIXMatcher(tc.pattern, true)
			if err != nil {
				t.Fatalf("compilePOSIXMatcher: %v", err)
			}
			if !matcher.MatchString(tc.match) {
				t.Fatalf("%q should match %q", tc.pattern, tc.match)
			}
			if matcher.MatchString(tc.reject) {
				t.Fatalf("%q should not match %q", tc.pattern, tc.reject)
			}
		})
	}
}

func TestRGToolNativeFallbackCaseInsensitiveAndLimited(t *testing.T) {
	root := t.TempDir()
	writeSearchFixture(t, filepath.Join(root, "a.txt"), "Alpha\nALPHA\nalpha\n")
	args, _ := json.Marshal(map[string]any{
		"pattern":     `^alpha$`,
		"path":        root,
		"max_results": 2,
	})

	out, err := executeRGToolWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, nativeOnlyRGRunner())
	if err != nil {
		t.Fatalf("rg_tool: %v", err)
	}
	if got := len(strings.Split(strings.TrimSpace(out), "\n")); got != 2 {
		t.Fatalf("result count = %d, want 2: %q", got, out)
	}
}

func TestRGToolRejectsNonPOSIXExpressionBeforeBackendSelection(t *testing.T) {
	root := t.TempDir()
	args, _ := json.Marshal(map[string]any{"pattern": `foo(?=bar)`, "path": root})
	called := false
	runner := rgRunner{
		lookPath: func(string) (string, error) { called = true; return "rg", nil },
		run: func(context.Context, string, []string) (string, int, error) {
			called = true
			return "", 0, nil
		},
	}

	_, err := executeRGToolWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, runner)
	if err == nil || !strings.Contains(err.Error(), "POSIX") {
		t.Fatalf("error = %v, want POSIX validation error", err)
	}
	if called {
		t.Fatal("backend must not run for an invalid POSIX expression")
	}
}

func TestRGToolUsesSystemRipgrepWhenAvailable(t *testing.T) {
	root := t.TempDir()
	args, _ := json.Marshal(map[string]any{"pattern": "farm", "path": root})
	var executable string
	runner := rgRunner{
		lookPath: func(name string) (string, error) {
			if name != "rg" {
				t.Fatalf("lookPath(%q), want rg", name)
			}
			return "/tools/rg", nil
		},
		run: func(_ context.Context, exe string, _ []string) (string, int, error) {
			executable = exe
			return filepath.Join(root, "words.txt") + ":1:farm\n", 0, nil
		},
	}

	out, err := executeRGToolWithRunner(context.Background(), string(args), &tooling.Env{CWD: root}, runner)
	if err != nil {
		t.Fatalf("rg_tool: %v", err)
	}
	if executable != "/tools/rg" || !strings.Contains(out, "farm") {
		t.Fatalf("system backend not used: executable=%q output=%q", executable, out)
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

func nativeOnlyRGRunner() rgRunner {
	return rgRunner{
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
