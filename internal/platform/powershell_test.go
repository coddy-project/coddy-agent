package platform

import (
	"strings"
	"testing"
)

// sessionBody reproduces the kind of argument that broke run_command in
// practice: Markdown prose carrying backticks, asterisks, parentheses, an em
// dash and an emoji.
const sessionBody = "Expand `agent.qwen.md` to **7 blocks** (21 items) — done ✅"

func TestWrapPowerShellCommandPreservesCommandVerbatim(t *testing.T) {
	commands := []string{
		sessionBody,
		`Write-Output "he said ""hi"""`,
		"git commit -m 'first line'\n\n'second line'",
		"@'\nliteral `tick` block\n'@",
		"",
	}
	for _, command := range commands {
		script := wrapPowerShellCommand(command)
		if command != "" && !strings.Contains(script, command) {
			t.Fatalf("wrapped script does not contain the command verbatim\ncommand: %q\nscript:  %q", command, script)
		}
		if !strings.HasPrefix(script, powerShellPrologue) {
			t.Fatalf("wrapped script does not start with the prologue: %q", script)
		}
		if !strings.HasSuffix(script, powerShellEpilogue) {
			t.Fatalf("wrapped script does not end with the epilogue: %q", script)
		}
	}
}

func TestPowerShellPrologueSetsUTF8Output(t *testing.T) {
	for _, want := range []string{
		"[Console]::OutputEncoding",
		"UTF8Encoding",
		"$OutputEncoding",
	} {
		if !strings.Contains(powerShellPrologue, want) {
			t.Fatalf("prologue %q does not contain %q", powerShellPrologue, want)
		}
	}
	// Assigning [Console]::OutputEncoding throws when no console is attached, so
	// the assignment must be guarded or a headless run would emit an error.
	if !strings.Contains(powerShellPrologue, "try {") || !strings.Contains(powerShellPrologue, "catch { }") {
		t.Fatalf("prologue does not guard the console assignment: %q", powerShellPrologue)
	}
}

func TestPowerShellEpiloguePropagatesExitCode(t *testing.T) {
	// $? must be captured before the first `if`, which would otherwise overwrite
	// it and mask a failed cmdlet.
	capture := strings.Index(powerShellEpilogue, "$__coddyOK = $?")
	firstIf := strings.Index(powerShellEpilogue, "if ")
	if capture < 0 || firstIf < 0 || capture > firstIf {
		t.Fatalf("epilogue does not capture $? before branching: %q", powerShellEpilogue)
	}
	if !strings.Contains(powerShellEpilogue, "exit $LASTEXITCODE") {
		t.Fatalf("epilogue does not propagate $LASTEXITCODE: %q", powerShellEpilogue)
	}
}

func TestShellCommandPowerShellWrapsScript(t *testing.T) {
	for _, shell := range []Shell{
		{Kind: ShellPwsh, Path: "pwsh"},
		{Kind: ShellPowerShell, Path: "powershell"},
	} {
		executable, args := shell.Command(sessionBody)
		if executable != shell.Path {
			t.Fatalf("executable = %q, want %q", executable, shell.Path)
		}
		if len(args) != 3 || args[0] != "-NoProfile" || args[1] != "-Command" {
			t.Fatalf("args = %#v, want -NoProfile -Command <script>", args)
		}
		if args[2] != wrapPowerShellCommand(sessionBody) {
			t.Fatalf("args[2] = %q, want the wrapped script", args[2])
		}
	}
}

// The POSIX shells must keep receiving the raw command: the wrapper is a Windows
// concern only.
func TestShellCommandPOSIXUnwrapped(t *testing.T) {
	for _, shell := range []Shell{
		{Kind: ShellBash, Path: "/bin/bash"},
		{Kind: ShellSh, Path: "/bin/sh"},
	} {
		_, args := shell.Command(sessionBody)
		if len(args) != 2 || args[0] != "-c" || args[1] != sessionBody {
			t.Fatalf("args = %#v, want -c %q", args, sessionBody)
		}
	}
}
