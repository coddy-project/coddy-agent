//go:build !windows

package platform

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// DetachProcessGroup puts the command in its own process group so that
// TerminateProcessGroup can reach the whole tree a shell spawns, not just the
// shell itself.
func DetachProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// TerminateProcessGroup signals the group started by cmd to exit and escalates
// to a forced kill when grace elapses. A process that already exited is not an
// error.
func TerminateProcessGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pgid := -cmd.Process.Pid

	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		// The group may not exist when Setpgid was skipped; fall back to the
		// process itself before giving up.
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}

	if grace > 0 && waitForProcessGroupExit(pgid, grace) {
		return nil
	}

	if err := syscall.Kill(pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	return nil
}

// ProcessGroupAlive reports whether any process still belongs to the group led
// by pid. It is how a fresh coddy tells a task its predecessor merely recorded
// from one whose processes are still running on this machine.
func ProcessGroupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
}

// TerminateProcessGroupByPID kills a group this process did not start, which is
// what reaping survivors of a previous run needs: after a crash there is no
// exec.Cmd left to ask, only the leader pid the bundle recorded.
func TerminateProcessGroupByPID(pid int, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}
	pgid := -pid

	if err := syscall.Kill(pgid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	if grace > 0 && waitForProcessGroupExit(pgid, grace) {
		return nil
	}
	if err := syscall.Kill(pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// waitForProcessGroupExit polls the group until signal 0 stops reaching it or
// the deadline passes. It reports whether the group is gone.
func waitForProcessGroupExit(pgid int, grace time.Duration) bool {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pgid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.Is(syscall.Kill(pgid, 0), syscall.ESRCH)
}
