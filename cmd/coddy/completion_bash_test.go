package main

import (
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// completeBash runs the packaged bash completion over a command line split
// the way bash splits it, the last word being the one under the cursor, and
// returns what it offers. No coddy is on PATH, so nothing the completion asks
// the binary for is offered.
func completeBash(t *testing.T, words ...string) []string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("needs bash")
	}
	script := `source "$1"; shift; COMP_WORDS=("$@"); COMP_CWORD=$(( $# - 1 )); _coddy; printf '%s\n' "${COMPREPLY[@]}"`
	args := append([]string{"-c", script, "bash", "../../packaging/completions/coddy.bash"}, words...)
	cmd := exec.Command(bash, args...)
	cmd.Env = append(os.Environ(), "PATH="+t.TempDir())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("completing %q: %v", words, err)
	}
	return strings.Fields(string(out))
}

// `coddy docs --lang=ru`, which bash splits into --lang, = and ru, names the
// language and not the verb, wherever it stands; after `--lang=` the
// languages are offered.
func TestBashCompletionOfDocsReadsLangWithAnEqualsSign(t *testing.T) {
	for _, tc := range []struct {
		words []string
		want  []string
	}{
		{[]string{"coddy", "docs", "--lang", "=", "ru", ""}, []string{"list", "search", "show", "--lang"}},
		{[]string{"coddy", "docs", "--lang", "=", "ru", "search", ""}, []string{"--limit", "--lang"}},
		{[]string{"coddy", "docs", "search", "--lang", "=", "ru", ""}, []string{"--limit", "--lang"}},
		{[]string{"coddy", "docs", "--lang", "ru", "search", ""}, []string{"--limit", "--lang"}},
		{[]string{"coddy", "docs", "--lang", "=", ""}, []string{"en", "ru"}},
		{[]string{"coddy", "docs", "--lang", "=", "r"}, []string{"ru"}},
		{[]string{"coddy", "docs", "--lang", ""}, []string{"en", "ru"}},
		// A bash whose COMP_WORDBREAKS has no "=" keeps the word whole.
		{[]string{"coddy", "docs", "--lang=r"}, []string{"--lang=ru"}},
		{[]string{"coddy", "docs", "--lang=ru", "search", ""}, []string{"--limit", "--lang"}},
	} {
		if got := completeBash(t, tc.words...); !slices.Equal(got, tc.want) {
			t.Errorf("completing %q: %q, want %q", tc.words, got, tc.want)
		}
	}
}
