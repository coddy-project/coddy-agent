package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
)

// topLevelCommands is what `coddy <command>` dispatches. The packaging rule ties
// three files to this set - the usage text, the man page and the shell
// completions - and nothing else keeps them together, so the list is asserted
// against all of them here, in both directions: a command the usage text has
// and a completion lacks, and a name a completion still offers after the
// binary stopped answering it (`http` and `gateway` after `serve` took over,
// issue #188), both fail.
//
// Adding a command means adding it below, which then fails until the usage
// text, the man page and the completions carry it too.
var topLevelCommands = []string{
	"acp", "cli", "serve", "sessions", "skills", "plugin",
	"mcp", "providers", "rules", "agents", "hooks", "update",
}

// serveVerbs control a daemon that is already running. They are subcommands of
// `serve` rather than commands of their own, so the top-level assertions above
// say nothing about them - and the same three files still have to carry them.
var serveVerbs = []string{"status", "stop", "restart"}

func TestUsageListsEveryCommand(t *testing.T) {
	assertSameSet(t, "the usage text", usageCommands(t), "topLevelCommands", topLevelCommands)
}

func TestPackagingFilesTrackTheCommandSet(t *testing.T) {
	completions := readRepoFile(t, "../../packaging/completions/coddy.bash")
	zsh := readRepoFile(t, "../../packaging/completions/coddy.zsh")
	man := readRepoFile(t, "../../packaging/man/coddy.1")
	usage := usageCommands(t)

	// The bash completion enumerates the commands on one line, and the zsh one
	// in its commands=( ... ) array, so those are the things to assert against
	// - the word appearing anywhere else in the file (a per-command flag case,
	// a comment) proves nothing. Both are compared as sets with the usage
	// text, so a name lingering after its command went is caught as well.
	assertSameSet(t, "packaging/completions/coddy.bash", commandWords(t, completions), "the usage text", usage)
	assertSameSet(t, "packaging/completions/coddy.zsh", zshCommandWords(t, zsh), "the usage text", usage)
	for _, cmd := range usage {
		if !strings.Contains(man, "\n.B "+cmd+"\n") && !strings.Contains(man, "\n.B \""+cmd+" ") && !strings.Contains(man, "\n.BI \""+cmd+" ") {
			t.Errorf("packaging/man/coddy.1 does not document %q", cmd)
		}
	}
}

// usageCommands derives the command set from the usage text itself: the word
// after the program name on every usage line, minus the flag forms and the
// bare invocation. The test binary is the program name here.
func usageCommands(t *testing.T) []string {
	t.Helper()
	var buf bytes.Buffer
	printUsage(&buf)
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), os.Args[0]+" ")
		if !ok {
			continue
		}
		word := strings.Fields(rest)[0]
		if strings.HasPrefix(word, "-") || strings.HasPrefix(word, "(") || slices.Contains(out, word) {
			continue
		}
		out = append(out, word)
	}
	if len(out) == 0 {
		t.Fatal("no command lines found in the usage text")
	}
	return out
}

// assertSameSet reports every name one side has and the other lacks.
func assertSameSet(t *testing.T, gotName string, got []string, wantName string, want []string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s does not offer %q, which %s lists", gotName, w, wantName)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			t.Errorf("%s offers %q, which %s does not list", gotName, g, wantName)
		}
	}
}

func TestPackagingFilesTrackTheServeVerbs(t *testing.T) {
	usage := func() string {
		var buf bytes.Buffer
		printUsage(&buf)
		return buf.String()
	}()
	completions := readRepoFile(t, "../../packaging/completions/coddy.bash")
	zsh := readRepoFile(t, "../../packaging/completions/coddy.zsh")
	man := readRepoFile(t, "../../packaging/man/coddy.1")

	for _, verb := range serveVerbs {
		if !strings.Contains(usage, verb) {
			t.Errorf("usage does not mention `serve %s`", verb)
		}
		if !strings.Contains(completions, verb) {
			t.Errorf("packaging/completions/coddy.bash does not offer `serve %s`", verb)
		}
		if !strings.Contains(zsh, verb) {
			t.Errorf("packaging/completions/coddy.zsh does not offer `serve %s`", verb)
		}
		if !strings.Contains(man, "serve "+verb) {
			t.Errorf("packaging/man/coddy.1 does not document `serve %s`", verb)
		}
	}
	// The background form is what the verbs are about, so it is asserted with
	// them rather than left to a reader to notice it went missing.
	for name, body := range map[string]string{
		"the usage text":                   usage,
		"packaging/completions/coddy.bash": completions,
		"packaging/completions/coddy.zsh":  zsh,
		"packaging/man/coddy.1":            man,
	} {
		if !strings.Contains(body, "daemon") {
			t.Errorf("%s does not mention `serve --daemon`", name)
		}
	}
}

// commandWords reads the words of the bash completion's commands= line.
func commandWords(t *testing.T, completions string) []string {
	t.Helper()
	for _, line := range strings.Split(completions, "\n") {
		_, rest, ok := strings.Cut(line, `commands="`)
		if !ok {
			continue
		}
		list, _, ok := strings.Cut(rest, `"`)
		if !ok {
			t.Fatalf("unterminated commands= line: %s", line)
		}
		return strings.Fields(list)
	}
	t.Fatal("packaging/completions/coddy.bash has no commands= line")
	return nil
}

// zshCommandWords reads the 'name:description' entries of the zsh completion's
// commands=( ... ) array.
func zshCommandWords(t *testing.T, zsh string) []string {
	t.Helper()
	_, body, ok := strings.Cut(zsh, "commands=(")
	if !ok {
		t.Fatal("packaging/completions/coddy.zsh has no commands=( array")
	}
	body, _, ok = strings.Cut(body, ")")
	if !ok {
		t.Fatal("packaging/completions/coddy.zsh: unterminated commands=( array")
	}
	var out []string
	for _, line := range strings.Split(body, "\n") {
		entry := strings.Trim(strings.TrimSpace(line), "'")
		name, _, ok := strings.Cut(entry, ":")
		if !ok || name == "" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestPackagingFilesCarryTheConfigTestFlag ties -t / --test-config to the same
// three files: the flag is offered on the console, on acp and on serve, so the
// completions must list it for each of them and the man page must explain it.
func TestPackagingFilesCarryTheConfigTestFlag(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf)
	if !strings.Contains(buf.String(), "--test-config") {
		t.Error("usage does not mention --test-config")
	}
	completions := readRepoFile(t, "../../packaging/completions/coddy.bash")
	if strings.Count(completions, "--test-config") < 2 {
		t.Error("packaging/completions/coddy.bash must offer --test-config for cli|acp and for serve")
	}
	zsh := readRepoFile(t, "../../packaging/completions/coddy.zsh")
	if strings.Count(zsh, "--test-config") < 2 {
		t.Error("packaging/completions/coddy.zsh must offer --test-config for cli|acp and for serve")
	}
	man := readRepoFile(t, "../../packaging/man/coddy.1")
	if !strings.Contains(man, `\-\-test\-config`) {
		t.Error("packaging/man/coddy.1 does not document --test-config")
	}
}

// TestPackagingFilesCarryTheDryRunFlag does for --dry-run what the test above
// does for --test-config.
func TestPackagingFilesCarryTheDryRunFlag(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf)
	if !strings.Contains(buf.String(), "--dry-run") {
		t.Error("usage does not mention --dry-run")
	}
	if strings.Count(readRepoFile(t, "../../packaging/completions/coddy.bash"), "--dry-run") < 2 {
		t.Error("packaging/completions/coddy.bash must offer --dry-run for cli|acp and for serve")
	}
	if strings.Count(readRepoFile(t, "../../packaging/completions/coddy.zsh"), "--dry-run") < 2 {
		t.Error("packaging/completions/coddy.zsh must offer --dry-run for cli|acp and for serve")
	}
	if !strings.Contains(readRepoFile(t, "../../packaging/man/coddy.1"), `\-\-dry\-run`) {
		t.Error("packaging/man/coddy.1 does not document --dry-run")
	}
}
