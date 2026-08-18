package shell

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const (
	// defaultForegroundTimeoutSeconds is how long run_command waits before it
	// decides the command is long-lived work rather than a slow answer.
	defaultForegroundTimeoutSeconds = 30

	// terminateGrace matches the pool's own stop grace, so a command killed by
	// the tool and one killed by background_stop behave the same.
	terminateGrace = 3 * time.Second

	// drainAfterKill bounds how long the tool waits for a killed command to be
	// reaped before answering. The answer does not depend on the exit code, and
	// waitPipeDrainDelay already guarantees the goroutine unblocks on its own.
	drainAfterKill = 2 * time.Second

	// waitPipeDrainDelay bounds cmd.Wait once the shell itself has exited.
	// exec gives a non-*os.File stdout an OS pipe and copies it on a goroutine,
	// and any grandchild that inherited the write end keeps that copy from ever
	// seeing EOF: killing the shell of `npm run serve` leaves node holding the
	// pipe, and Wait used to block forever. The timer only starts after the
	// direct child exits, so healthy work is never cut short by it.
	waitPipeDrainDelay = 5 * time.Second
)

// startedCommand is one foreground command in flight. Exactly one goroutine
// ever calls cmd.Wait; everyone else - the tool's select, and the pool once the
// command is adopted - observes the same result through waited.
type startedCommand struct {
	cmd       *exec.Cmd
	writer    *switchWriter
	startedAt time.Time

	// processStartedAt is the OS creation time of the process, read once at
	// launch like the background runner does. It is what proves a persisted pid
	// still belongs to this command if the command is later adopted.
	processStartedAt time.Time

	// waited is closed, never sent on, so a tool call that returns without
	// reading it cannot leave the wait goroutine parked forever.
	waited  chan struct{}
	exitErr error // valid only after waited is closed
}

// startForeground launches a command without binding its lifetime to a context.
// A context-bound command would be killed by the deferred cancel of the tool
// call, and a command that outlives its timeout is precisely the one that must
// survive it to be adopted.
func startForeground(command string, env *tooling.Env, commandShell platform.Shell) (*startedCommand, error) {
	return startForegroundInto(command, env, commandShell, nil)
}

// startForegroundInto is startForeground with the output sink attached before
// the child starts. A sink handed over afterwards would leave a window in
// which output still lands in the unbounded buffer, which is the whole thing a
// bounded sink exists to prevent.
func startForegroundInto(command string, env *tooling.Env, commandShell platform.Shell, sink io.Writer) (*startedCommand, error) {
	executable, commandArgs := commandShell.Command(command)
	cmd := exec.Command(executable, commandArgs...) // #nosec G204 -- the command is operator-approved shell input, same as the background runner
	if env != nil {
		cmd.Dir = env.CWD
	}

	writer := &switchWriter{target: sink}
	cmd.Stdout = writer
	cmd.Stderr = writer
	cmd.WaitDelay = waitPipeDrainDelay
	platform.DetachProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	sc := &startedCommand{
		cmd:              cmd,
		writer:           writer,
		startedAt:        time.Now(),
		processStartedAt: platform.ProcessStartedAt(cmd.Process.Pid),
		waited:           make(chan struct{}),
	}
	go func() {
		sc.exitErr = cmd.Wait()
		close(sc.waited)
	}()
	return sc, nil
}

// finished reports whether the command has already been reaped, without
// blocking. A select that fires on the timeout at the same moment the command
// exits must not report living work.
func (sc *startedCommand) finished() bool {
	select {
	case <-sc.waited:
		return true
	default:
		return false
	}
}

// output returns everything captured so far, decoded and stripped of terminal
// control noise.
func (sc *startedCommand) output() string {
	return platform.SanitizeOutput(platform.DecodeOutput(sc.writer.Bytes()))
}

// pid leads the process group, because the group was detached before Start.
func (sc *startedCommand) pid() int {
	if sc.cmd == nil || sc.cmd.Process == nil {
		return 0
	}
	return sc.cmd.Process.Pid
}

// completedResult renders a command that exited on its own, in the shapes the
// shell feature specs already assert.
func (sc *startedCommand) completedResult() string {
	out := sc.output()
	if sc.exitErr != nil && !errors.Is(sc.exitErr, exec.ErrWaitDelay) {
		return fmt.Sprintf("command failed: %v\n%s", sc.exitErr, out)
	}
	if out == "" {
		return "(no output)"
	}
	return out
}

// terminate kills the command and everything it spawned, then answers with the
// notice followed by whatever the command managed to print. The output comes
// second because the tool output ceiling truncates from the end.
func (sc *startedCommand) terminate(notice string) string {
	_ = platform.TerminateProcessGroup(sc.cmd, terminateGrace)

	timer := time.NewTimer(drainAfterKill)
	defer timer.Stop()
	select {
	case <-sc.waited:
	case <-timer.C:
	}

	if out := sc.output(); out != "" {
		return notice + "\n\n" + out
	}
	return notice
}

// adoptedHandle lets the pool own a command the tool started. It is the second
// observer of the single cmd.Wait already in flight, which is why Wait here
// only reads the settled result.
type adoptedHandle struct {
	sc *startedCommand
}

func (h *adoptedHandle) Wait() (int, error) {
	<-h.sc.waited
	err := h.sc.exitErr
	if err == nil {
		return 0, nil
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The shell exited cleanly but something it spawned still held the
		// output pipe. That is a detached child, not a failed command.
		if state := h.sc.cmd.ProcessState; state != nil {
			return state.ExitCode(), nil
		}
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

func (h *adoptedHandle) Stop(grace time.Duration) error {
	return platform.TerminateProcessGroup(h.sc.cmd, grace)
}

func (h *adoptedHandle) PID() int {
	return h.sc.pid()
}

func (h *adoptedHandle) ProcessStartedAt() time.Time {
	return h.sc.processStartedAt
}

// adoption is what the pool gave back for a command it took over.
type adoption struct {
	snap bgtask.Snapshot
	// prefix is everything the command printed before the handover, decoded and
	// sanitised, ready to show to the model.
	prefix string
}

// adoptForeground hands a still-running command to the session task pool. A nil
// adoption means the pool declined and the caller still owns the process, so it
// must terminate it; the error then says why, in words meant for the model.
func adoptForeground(sc *startedCommand, args runCommandArgs, env *tooling.Env) (*adoption, error) {
	pool, err := requirePool(env)
	if err != nil {
		return nil, err
	}

	adopted := false
	var prefix []byte
	snap, err := pool.Adopt(bgtask.Spec{
		SessionID:  env.SessionID,
		Kind:       bgtask.KindCommand,
		Command:    args.Command,
		CWD:        env.CWD,
		ToolCallID: env.ToolCallID,
		StartedAt:  sc.startedAt,
		// TimeoutSeconds is deliberately left unset: the foreground limit that
		// just expired must not become the task's hard limit.
		ExpectedSeconds: args.ExpectedSeconds,
	}, func(out io.Writer) (bgtask.Handle, error) {
		prefix = sc.writer.Redirect(out)
		adopted = true
		return &adoptedHandle{sc: sc}, nil
	})
	if !adopted {
		return nil, err
	}
	return &adoption{snap: snap, prefix: platform.SanitizeOutput(platform.DecodeOutput(prefix))}, nil
}
