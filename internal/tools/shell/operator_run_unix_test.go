//go:build !windows

package shell

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"

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

// waitForFile polls for a file the command writes once it is ready.
func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			return strings.TrimSpace(string(b))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

// TestOperatorCancelEndsWhatTheCommandLeftRunning: a cancelled turn stops the
// command the way a Stop does, the whole group included. A non-interactive
// shell starts its background jobs with SIGINT ignored, so the shell can exit
// on the interrupt while its child lives on.
func TestOperatorCancelEndsWhatTheCommandLeftRunning(t *testing.T) {
	client := &operatorClient{available: true, answer: acp.OperatorActionRun}
	env := operatorEnv(t, client)
	pidFile := filepath.Join(env.CWD, "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		waitForFile(t, pidFile)
		cancel()
	}()
	_, err := OperatorCommandTool(env.OperatorCommand).Execute(ctx,
		operatorArgs(`trap 'exit 0' INT; sleep 600 >/dev/null 2>&1 & echo $! > child.pid; wait`), env)
	if err == nil {
		t.Fatal("a cancelled turn returned no error")
	}
	pid, _ := strconv.Atoi(waitForFile(t, pidFile))
	deadline := time.Now().Add(5 * time.Second)
	for unix.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if unix.Kill(pid, 0) == nil {
		_ = unix.Kill(pid, unix.SIGKILL)
		t.Fatal("the command's child outlived the cancelled turn")
	}
}

// TestOperatorRunDuringCancelStartsNothing: a Run that lands while the turn is
// being cancelled does not start the command.
func TestOperatorRunDuringCancelStartsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &operatorClient{available: true, answer: acp.OperatorActionRun, beforeAnswer: cancel}
	env := operatorEnv(t, client)
	_, err := OperatorCommandTool(env.OperatorCommand).Execute(ctx, operatorArgs(`trap '' INT; touch ran`), env)
	if err == nil {
		t.Fatal("a cancelled turn returned no error")
	}
	for _, phase := range client.phases() {
		if phase == OperatorPhaseRunning {
			t.Fatalf("the command started although the turn was cancelled: %v", client.phases())
		}
	}
	time.Sleep(300 * time.Millisecond)
	if _, statErr := os.Stat(filepath.Join(env.CWD, "ran")); statErr == nil {
		t.Fatal("the command ran although the turn was cancelled")
	}
}

// TestOperatorStopDuringTheCancelDrainKillsAtOnce: once the turn is cancelled
// the interrupt has gone out, so the card says stopping at once and the next
// Stop the operator presses is the kill.
func TestOperatorStopDuringTheCancelDrainKillsAtOnce(t *testing.T) {
	ready := make(chan struct{})
	client := &operatorClient{available: true, answer: acp.OperatorActionRun}
	env := operatorEnv(t, client)
	readyFile := filepath.Join(env.CWD, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.afterRun = func(tk *acp.OperatorCommandTicket) {
		waitForFile(t, readyFile)
		cancel()
		close(ready)
		time.Sleep(300 * time.Millisecond)
		_ = tk.Answer(acp.OperatorActionStop, "")
	}
	start := time.Now()
	_, err := OperatorCommandTool(env.OperatorCommand).Execute(ctx, operatorArgs(`trap '' INT; echo up > ready; sleep 30`), env)
	if err == nil {
		t.Fatal("a cancelled turn returned no error")
	}
	<-ready
	if took := time.Since(start); took > operatorInterruptWait() {
		t.Fatalf("the Stop after the cancel did not kill at once: %s", took)
	}
	phases := client.phases()
	if len(phases) < 2 || phases[len(phases)-2] != OperatorPhaseStopping || phases[len(phases)-1] != OperatorPhaseStopped {
		t.Fatalf("phases = %v, want ... stopping, stopped", phases)
	}
}

// TestOperatorRefusesAFolderSwappedWhileTheCardWaited: the folder the card
// showed is the folder the command runs in; one replaced by a link while the
// operator decided is refused.
func TestOperatorRefusesAFolderSwappedWhileTheCardWaited(t *testing.T) {
	client := &operatorClient{available: true, answer: acp.OperatorActionRun}
	env := operatorEnv(t, client)
	build := filepath.Join(env.CWD, "build")
	elsewhere := t.TempDir()
	if err := os.Mkdir(build, 0o755); err != nil {
		t.Fatal(err)
	}
	client.beforeAnswer = func() {
		if err := os.Remove(build); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(elsewhere, build); err != nil {
			t.Error(err)
		}
	}
	args := `{"command":"touch ran","reason":"run_command was refused","cwd":"build"}`
	_, err := OperatorCommandTool(env.OperatorCommand).Execute(context.Background(), args, env)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("err = %v, want the folder refused", err)
	}
	if _, statErr := os.Stat(filepath.Join(elsewhere, "ran")); statErr == nil {
		t.Fatal("the command ran in the folder the link points to")
	}
}
