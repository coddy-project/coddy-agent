package shell

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func waitOperatorCommand(t *testing.T, cmd *OperatorCommand) {
	t.Helper()
	select {
	case <-cmd.Done():
	case <-time.After(20 * time.Second):
		cmd.Kill(0)
		t.Fatalf("the command did not end; output so far: %q", cmd.Output())
	}
}

// TestOperatorToolCommandInterrupt: Stop interrupts the command the way
// Ctrl+C does, so a program that cleans up on SIGINT gets to.
func TestOperatorToolCommandInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("there is no console to send Ctrl+C to on Windows")
	}
	cmd, err := StartOperatorToolCommand(`trap 'echo trapped; exit 7' INT; echo ready; while :; do sleep 0.1; done`, t.TempDir(), platform.CurrentShell())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(cmd.Output(), "ready") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cmd.Interrupt()
	waitOperatorCommand(t, cmd)
	if !strings.Contains(cmd.Output(), "trapped") {
		t.Fatalf("the trap did not run: %q", cmd.Output())
	}
	if code := cmd.ExitCode(); code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

// TestOperatorToolCommandKillAfterAnIgnoredInterrupt: a command that ignores
// SIGINT still ends when Stop escalates.
func TestOperatorToolCommandKillAfterAnIgnoredInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signals are a Unix notion")
	}
	cmd, err := StartOperatorToolCommand(`trap '' INT; echo ready; while :; do sleep 0.1; done`, t.TempDir(), platform.CurrentShell())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(cmd.Output(), "ready") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cmd.Interrupt()
	select {
	case <-cmd.Done():
		t.Fatal("a command that ignores SIGINT ended on the interrupt")
	case <-time.After(300 * time.Millisecond):
	}
	cmd.Kill(time.Second)
	waitOperatorCommand(t, cmd)
}

// TestOperatorToolCommandRunsTheCommandAsWritten: quotes, dollars and a
// newline reach the operator shell untouched.
func TestOperatorToolCommandRunsTheCommandAsWritten(t *testing.T) {
	command := "echo \"a  b\" 'c$d'\necho second"
	want := "a  b c$d\nsecond"
	if k := platform.CurrentShell().Kind; k == platform.ShellPwsh || k == platform.ShellPowerShell {
		command = "Write-Output \"a  b\" 'c$d'\nWrite-Output second"
		want = "a  b\nc$d\nsecond"
	} else if k == platform.ShellCmd {
		t.Skip("cmd has no single-quote strings")
	}
	cmd, err := StartOperatorToolCommand(command, t.TempDir(), platform.CurrentShell())
	if err != nil {
		t.Fatal(err)
	}
	waitOperatorCommand(t, cmd)
	got := strings.ReplaceAll(strings.TrimSpace(cmd.Output()), "\r\n", "\n")
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
