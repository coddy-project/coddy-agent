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
		echoes   bool
	}{
		{ShellBash, nil, "set -o history\n", false},
		{ShellSh, nil, "", false},
		// PowerShell prints its own prompt and the line it read, even reading a
		// pipe, so a caller that echoes too shows every command twice.
		{ShellPwsh, []string{"-NoLogo"}, powerShellPrologue, true},
		{ShellPowerShell, []string{"-NoLogo"}, powerShellPrologue, true},
		{ShellCmd, []string{"/q"}, "chcp 65001 >nul 2>&1\r\n", false},
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
		if got := shell.EchoesInput(); got != tc.echoes {
			t.Fatalf("%s EchoesInput = %v, want %v", tc.kind, got, tc.echoes)
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
