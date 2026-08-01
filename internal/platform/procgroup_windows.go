//go:build windows

package platform

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
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

// processIdentitySlack is how far a process's creation time may sit from the
// start time the task record carries and still be accepted as that task. A
// task's process is created milliseconds after the pool stamps StartedAt, so the
// window only has to absorb a slow shell launch; a recycled pid, by contrast,
// belongs to something started minutes to days apart.
const processIdentitySlack = 2 * time.Minute

// ProcessGroupAlive reports whether the process the record describes is still
// running. startedAt is the task's recorded start time and is what tells the
// process apart from a stranger that inherited its pid.
//
// Windows has no process group to probe the way a unix signal 0 does, and the
// obvious substitute - opening the process by pid - answers a different
// question in two damaging ways. A process object outlives its own process for
// as long as anybody holds a handle to it, so a corpse still opens; and opening
// by number matches any process at all, where the unix probe only ever matches a
// group leader and so filters out pid reuse for free. Both mistakes end at
// background_reap running taskkill /T /F on the wrong tree.
//
// So this asks two questions instead. WaitForSingleObject with a zero timeout
// reports whether the process object is signalled, which is the difference
// between a running process and a retained corpse - it is not os.Process.Wait,
// which would block until a live process exits and hang the probe.
// GetProcessTimes then confirms the process was created when the record says the
// task started. Children are handled by taskkill /T when terminating.
func ProcessGroupAlive(pid int, startedAt time.Time) bool {
	if pid <= 0 {
		return false
	}

	// PROCESS_QUERY_LIMITED_INFORMATION is granted across integrity levels, so
	// an elevated child does not read as gone the way it would with the wider
	// rights os.FindProcess asks for. Failing to open means the pid cannot be
	// shown to be this task, and an unproven pid is not one to offer for killing.
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	// WAIT_OBJECT_0 means the process is signalled, which for a process object
	// means it has exited. WAIT_TIMEOUT means it is still running.
	event, err := windows.WaitForSingleObject(handle, 0)
	if err != nil || event != uint32(windows.WAIT_TIMEOUT) {
		return false
	}

	return processStartedAround(handle, startedAt)
}

// processStartedAround reports whether the open process was created close enough
// to startedAt to be the task the record describes. A record with no usable
// start time falls back to liveness alone, so the probe never becomes stricter
// than the one it replaces by accident.
func processStartedAround(handle windows.Handle, startedAt time.Time) bool {
	if startedAt.IsZero() {
		return true
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return true
	}
	drift := time.Unix(0, creation.Nanoseconds()).Sub(startedAt)
	if drift < 0 {
		drift = -drift
	}
	return drift <= processIdentitySlack
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
