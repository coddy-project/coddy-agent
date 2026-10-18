//go:build !windows

package shell

import (
	"testing"

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
