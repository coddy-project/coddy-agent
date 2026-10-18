package dryrun

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runYAMLBody(t *testing.T, body string) *Report {
	t.Helper()
	prep, _ := prepare(t, body)
	return Run(context.Background(), Request{Cfg: prep.Cfg, Paths: prep.Paths, Locator: prep.Locator})
}

// TestOperatorShellAndMultiplexerProbes: the operator shell and the
// multiplexer are looked up on the host that will run the commands.
func TestOperatorShellAndMultiplexerProbes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup of a shell script is a POSIX fixture")
	}
	bin := t.TempDir()
	for _, name := range []string{"coddy-dry-run-shell", "tmux"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	rep := runYAMLBody(t, "tools:\n  operator_command:\n    shell: coddy-dry-run-shell\n    multiplexer: tmux\n")
	if c := find(t, rep, "tools.operator_command.shell"); c.Status != StatusOK || !strings.Contains(c.Message, "coddy-dry-run-shell") {
		t.Errorf("shell %+v", c)
	}
	if c := find(t, rep, "tools.operator_command.multiplexer"); c.Status != StatusOK || !strings.Contains(c.Message, "tmux") {
		t.Errorf("multiplexer %+v", c)
	}

	rep = runYAMLBody(t, "tools:\n  operator_command:\n    shell: definitely-not-a-shell\n    multiplexer: screen\n")
	if c := find(t, rep, "tools.operator_command.shell"); c.Status != StatusError || !strings.Contains(c.Fix, "tools.operator_command.shell") {
		t.Errorf("missing shell %+v", c)
	}
	if c := find(t, rep, "tools.operator_command.multiplexer"); c.Status != StatusError || !strings.Contains(c.Message, "screen") {
		t.Errorf("missing multiplexer %+v", c)
	}
}
