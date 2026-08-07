package bgtask

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Runner starts the work described by a Spec and hands back a Handle the pool
// can wait on and terminate. Keeping this an interface is what lets a later
// change add a nested-agent runner without touching scheduling, persistence, or
// the HTTP surface.
type Runner interface {
	Start(spec Spec, out io.Writer) (Handle, error)
}

// Handle controls one started task.
type Handle interface {
	// Wait blocks until the work finishes and reports its exit code.
	Wait() (exitCode int, err error)
	// Stop terminates the work, allowing grace for a clean exit first.
	Stop(grace time.Duration) error
	// PID leads the process group the work runs in, or 0 when the work is not
	// an OS process. It is persisted so a later coddy can reach a survivor.
	PID() int
	// ProcessStartedAt is the exact OS process creation time used to prove that
	// a persisted pid still identifies this task. It is zero when the platform
	// does not need or cannot provide a process identity.
	ProcessStartedAt() time.Time
}

// CommandRunner runs shell commands through the detected host interpreter.
type CommandRunner struct {
	Shell platform.Shell
}

// NewCommandRunner returns a runner bound to the host shell.
func NewCommandRunner() *CommandRunner {
	return &CommandRunner{Shell: platform.CurrentShell()}
}

// Start launches the command in its own process group so that stopping the task
// reaches everything the shell spawned, not just the shell.
func (r *CommandRunner) Start(spec Spec, out io.Writer) (Handle, error) {
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return nil, fmt.Errorf("background command is empty")
	}

	sh := r.Shell
	if sh.Path == "" {
		sh = platform.CurrentShell()
	}

	executable, args := sh.Command(command)
	cmd := exec.Command(executable, args...) // #nosec G204 -- the command is operator-approved shell input, same as run_command
	cmd.Dir = spec.CWD
	cmd.Stdout = out
	cmd.Stderr = out
	platform.DetachProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &commandHandle{
		cmd:              cmd,
		processStartedAt: platform.ProcessStartedAt(cmd.Process.Pid),
	}, nil
}

type commandHandle struct {
	cmd              *exec.Cmd
	processStartedAt time.Time
}

func (h *commandHandle) Wait() (int, error) {
	err := h.cmd.Wait()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

func (h *commandHandle) Stop(grace time.Duration) error {
	return platform.TerminateProcessGroup(h.cmd, grace)
}

func (h *commandHandle) PID() int {
	if h.cmd == nil || h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *commandHandle) ProcessStartedAt() time.Time {
	return h.processStartedAt
}
