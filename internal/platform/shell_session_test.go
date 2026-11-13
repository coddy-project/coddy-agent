package platform

import "testing"

// A shell that reads a pipe needs two things of its own on Windows: arguments
// that keep its banner and its echo out of the output, and a prologue that
// settles the encoding. Nothing here runs a shell, so it holds on every host.
func TestInteractiveShellArgumentsAndPrologue(t *testing.T) {
	for _, tc := range []struct {
		kind     ShellKind
		args     []string
		prologue string
	}{
		{ShellBash, nil, "set -o history\n"},
		{ShellSh, nil, ""},
		{ShellPwsh, []string{"-NoLogo"}, powerShellPrologue},
		{ShellPowerShell, []string{"-NoLogo"}, powerShellPrologue},
		{ShellCmd, []string{"/q"}, "chcp 65001 >nul 2>&1\r\n"},
	} {
		shell := Shell{Kind: tc.kind, Path: string(tc.kind)}
		args := shell.InteractiveArgs()
		if len(args) != len(tc.args) {
			t.Fatalf("%s args = %q, want %q", tc.kind, args, tc.args)
		}
		for i := range args {
			if args[i] != tc.args[i] {
				t.Fatalf("%s args = %q, want %q", tc.kind, args, tc.args)
			}
		}
		if got := shell.SessionPrologue(); got != tc.prologue {
			t.Fatalf("%s prologue = %q, want %q", tc.kind, got, tc.prologue)
		}
	}
}

// The prologue of an interactive PowerShell is the one a single command gets:
// a second copy of those lines would drift from it.
func TestPowerShellSessionPrologueAsksForUTF8(t *testing.T) {
	got := Shell{Kind: ShellPwsh}.SessionPrologue()
	if !contains(got, "UTF8Encoding") || !contains(got, "$OutputEncoding") {
		t.Fatalf("prologue does not set the output encoding: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
