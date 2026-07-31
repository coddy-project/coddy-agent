//go:build windows

package platform

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// DetachProcessGroup puts the command in its own console process group so that
// TerminateProcessGroup can reach the whole tree a shell spawns, not just the
// shell itself.
func DetachProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// TerminateProcessGroup kills the process tree started by cmd. Windows has no
// graceful group signal comparable to SIGTERM for a non-console child, so the
// grace period only bounds how long taskkill is given before the process handle
// is closed directly. A process that already exited is not an error.
func TerminateProcessGroup(cmd *exec.Cmd, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := strconv.Itoa(cmd.Process.Pid)

	kill := exec.Command("taskkill", "/T", "/F", "/PID", pid)
	if err := kill.Start(); err == nil {
		done := make(chan struct{})
		go func() {
			_ = kill.Wait()
			close(done)
		}()
		if grace <= 0 {
			grace = 5 * time.Second
		}
		select {
		case <-done:
			return nil
		case <-time.After(grace):
			_ = kill.Process.Kill()
		}
	}

	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// ProcessGroupAlive reports whether the process still exists. Windows has no
// process group to probe the way a unix signal 0 does, so this asks the OS to
// open the process: FindProcess fails once the pid is gone. Its children are
// handled by taskkill /T when terminating.
//
// Deliberately no Wait() here - on Windows that blocks until a live process
// exits, which would hang a liveness probe.
func ProcessGroupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}

// TerminateProcessGroupByPID kills a tree this process did not start, which is
// what reaping survivors of a previous run needs.
func TerminateProcessGroupByPID(pid int, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	if err := kill.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		_ = kill.Wait()
		close(done)
	}()
	if grace <= 0 {
		grace = 5 * time.Second
	}
	select {
	case <-done:
		return nil
	case <-time.After(grace):
		_ = kill.Process.Kill()
		return nil
	}
}
