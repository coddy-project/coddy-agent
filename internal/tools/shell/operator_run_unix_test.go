//go:build !windows

package shell

import (
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// TestOperatorToolCommandHasItsOwnSession: a command the operator runs for
// the agent leads a session of its own, so it has no controlling terminal and
// a program that wants one fails at once instead of drawing over the console
// or stopping on SIGTTIN.
func TestOperatorToolCommandHasItsOwnSession(t *testing.T) {
	cmd, err := StartOperatorToolCommand("sleep 5", t.TempDir(), platform.CurrentShell())
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.Kill(0)
	pid := cmd.sc.cmd.Process.Pid
	sid, err := unix.Getsid(pid)
	if err != nil {
		t.Fatal(err)
	}
	if sid != pid {
		t.Fatalf("the shell (pid %d) is not the leader of its own session (sid %d)", pid, sid)
	}
}

// TestOperatorKillLeavesNoProcessGroup: a Kill ends what the command started
// in its group, not only the shell.
func TestOperatorKillLeavesNoProcessGroup(t *testing.T) {
	cmd, err := StartOperatorToolCommand("sleep 30 & sleep 30", t.TempDir(), platform.CurrentShell())
	if err != nil {
		t.Fatal(err)
	}
	pgid := cmd.sc.cmd.Process.Pid
	time.Sleep(200 * time.Millisecond)
	cmd.Kill(time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for platform.ProcessGroupAlive(pgid, time.Time{}) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if platform.ProcessGroupAlive(pgid, time.Time{}) {
		t.Fatal("the process group outlived the kill")
	}
}
