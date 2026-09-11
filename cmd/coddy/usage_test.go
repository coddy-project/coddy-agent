package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// topLevelCommands is what `coddy <command>` dispatches. The packaging rule ties
// three files to this set - the usage text, the man page and the shell
// completions - and nothing else keeps them together, so the list is asserted
// against all of them here.
//
// Adding a command means adding it below, which then fails until the man page
// and the completions carry it too.
var topLevelCommands = []string{
	"acp", "cli", "serve", "sessions", "skills", "plugin",
	"mcp", "providers", "rules", "agents", "hooks", "update",
}

// removedCommands are names that must not come back by accident. `serve` runs
// every long-running surface now; a stray reference to one of these in the
// documentation is a promise the binary cannot keep.
var removedCommands = []string{"http", "gateway", "swarm"}

// serveVerbs control a daemon that is already running. They are subcommands of
// `serve` rather than commands of their own, so the top-level assertions above
// say nothing about them - and the same three files still have to carry them.
var serveVerbs = []string{"status", "stop", "restart"}

func TestUsageListsEveryCommand(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf)
	usage := buf.String()
	for _, cmd := range topLevelCommands {
		if !strings.Contains(usage, " "+cmd+" ") && !strings.Contains(usage, " "+cmd+"\n") {
			t.Errorf("usage does not mention the %q command", cmd)
		}
	}
}

func TestPackagingFilesTrackTheCommandSet(t *testing.T) {
	completions := readRepoFile(t, "../../packaging/completions/coddy.bash")
	zsh := readRepoFile(t, "../../packaging/completions/coddy.zsh")
	man := readRepoFile(t, "../../packaging/man/coddy.1")

	// The bash completion enumerates the commands on one line, so that line is
	// the thing to assert against - the word appearing anywhere else in the
	// file (a per-command flag case, a comment) proves nothing.
	offered := commandWords(t, completions)
	for _, cmd := range topLevelCommands {
		if !offered[cmd] {
			t.Errorf("packaging/completions/coddy.bash does not offer %q", cmd)
		}
		if !strings.Contains(zsh, "'"+cmd+":") && !strings.Contains(zsh, cmd+")") {
			t.Errorf("packaging/completions/coddy.zsh does not offer %q", cmd)
		}
		if !strings.Contains(man, "\n.B "+cmd+"\n") && !strings.Contains(man, "\n.B \""+cmd+" ") && !strings.Contains(man, "\n.BI \""+cmd+" ") {
			t.Errorf("packaging/man/coddy.1 does not document %q", cmd)
		}
	}

	// A removed name left in the completion list completes to something that no
	// longer exists.
	for _, cmd := range removedCommands {
		if offered[cmd] {
			t.Errorf("packaging/completions/coddy.bash still offers the removed %q command", cmd)
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
func commandWords(t *testing.T, completions string) map[string]bool {
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
		out := map[string]bool{}
		for _, w := range strings.Fields(list) {
			out[w] = true
		}
		return out
	}
	t.Fatal("packaging/completions/coddy.bash has no commands= line")
	return nil
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
