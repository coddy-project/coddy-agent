package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// captureOutput runs fn with os.Stdout and os.Stderr replaced by one pipe and
// returns everything it printed on either stream. A help screen lands wherever
// the command it belongs to writes its usage - the flag package prints on
// stderr - so the contract of `coddy help` is what the two say together.
func captureOutput(fn func()) (string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	prevOut, prevErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	os.Stdout, os.Stderr = prevOut, prevErr
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out, nil
}

// withTempHome points CODDY_HOME at an empty directory for the test, so a help
// screen of a command that reads the configuration touches nothing real.
func withTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("CODDY_HOME", t.TempDir())
}

// TestHelpPrintsTheUsageText pins `coddy help` to the screen `-h` prints: the
// command list is one screen whatever names it.
func TestHelpPrintsTheUsageText(t *testing.T) {
	withTempHome(t)
	out, err := captureOutput(func() {
		if err := runHelp(nil); err != nil {
			t.Errorf("coddy help: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	printUsage(&want)
	if out != want.String() {
		t.Errorf("coddy help printed:\n%s\nwant the usage text:\n%s", out, want.String())
	}
}

// TestHelpForwardsToTheCommandsOwnHelp: `coddy help <command> ...` answers
// what `coddy <command> ... --help` answers - the deeper the path, the more
// specific the screen.
func TestHelpForwardsToTheCommandsOwnHelp(t *testing.T) {
	withTempHome(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"serve"}, "daemon"},
		{[]string{"serve", "install"}, "systemd"},
		{[]string{"sessions"}, "sessions export"},
		{[]string{"sessions", "export"}, "sessions export"},
		{[]string{"skills"}, "skills list"},
		{[]string{"docs"}, "docs [list]"},
	} {
		out, err := captureOutput(func() {
			if err := runHelp(tc.args); err != nil {
				t.Errorf("coddy help %s: %v", strings.Join(tc.args, " "), err)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("coddy help %s printed no %q:\n%s", strings.Join(tc.args, " "), tc.want, out)
		}
	}
}

// TestHelpOnAnUnknownCommandNamesIt keeps `coddy help <word>` answering like
// `coddy <word>`: the word, then the usage, rather than silence.
func TestHelpOnAnUnknownCommandNamesIt(t *testing.T) {
	withTempHome(t)
	err := runHelp([]string{"frobnicate"})
	if err == nil {
		t.Fatal("coddy help frobnicate: accepted an unknown command")
	}
	var unknown *unknownCommandError
	if !errors.As(err, &unknown) {
		t.Fatalf("coddy help frobnicate: %v, want an unknownCommandError", err)
	}
	if unknown.name != "frobnicate" {
		t.Errorf("the error names %q, want %q", unknown.name, "frobnicate")
	}
}

// TestHelpFlagForwardsToo: `coddy help serve --help` is the screen the flag
// alone prints, not the command list.
func TestHelpFlagForwardsToo(t *testing.T) {
	withTempHome(t)
	out, err := captureOutput(func() {
		if err := runHelp([]string{"serve", "--help"}); err != nil {
			t.Errorf("coddy help serve --help: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "daemon") {
		t.Errorf("coddy help serve --help printed:\n%s", out)
	}
}
