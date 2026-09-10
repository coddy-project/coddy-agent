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
