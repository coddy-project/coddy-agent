package shell

import (
	"errors"
	"os/exec"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// operatorOutputCap is how much of a command's output is kept for display.
// An operator command has no timeout and nobody reads its output but a human,
// so the capture is a tail rather than the whole stream: `tail -f` must not
// grow the console's memory, and every poll must cost the same no matter how
// long the command has been running.
const operatorOutputCap = 256 << 10

// OperatorCommand is a command the human operator started directly - the
// console's `!!` prefix - rather than one the model asked for. It reuses the
// process handling of run_command (same shell selection, detached process
// group, decoded and sanitised output) and deliberately nothing else: no
// permission gate, because the operator is the principal here; no session, no
// background pool, no persisted tool call, so the run stays invisible to the
// agent.
type OperatorCommand struct {
	sc   *startedCommand
	tail *tailBuffer
}

// StartOperatorCommand launches command in cwd with the shell of this host.
func StartOperatorCommand(command, cwd string) (*OperatorCommand, error) {
	return StartOperatorCommandForShell(command, cwd, platform.CurrentShell())
}

// StartOperatorCommandForShell is StartOperatorCommand bound to a given shell.
func StartOperatorCommandForShell(command, cwd string, commandShell platform.Shell) (*OperatorCommand, error) {
	tail := &tailBuffer{limit: operatorOutputCap}
	// The sink is attached before the child starts, so not even the first
	// burst of a `yes` can land in an unbounded buffer.
	sc, err := startForegroundInto(command, &tooling.Env{CWD: cwd}, commandShell, tail)
	if err != nil {
		return nil, err
	}
	return &OperatorCommand{sc: sc, tail: tail}, nil
}

// Output returns the retained tail of what the command printed, decoded and
// stripped of terminal control sequences. It is safe to call while the command
// runs, and costs the same on every call.
func (c *OperatorCommand) Output() string {
	raw, truncated := c.tail.Snapshot()
	return platform.SanitizeOutput(platform.DecodeOutput(trimPartialUTF8(raw, truncated, c.running())))
}

// running reports whether more output can still arrive.
func (c *OperatorCommand) running() bool {
	select {
	case <-c.sc.waited:
		return false
	default:
		return true
	}
}

// trimPartialUTF8 drops a character the capture cut in half - at the end, while
// the command is mid-write, and, when the buffer really was truncated at the
// front, the rune the tail buffer beheaded there.
//
// It must run before decoding, not after: DecodeOutput reads a buffer that is
// not valid UTF-8 as the Windows console code page, so a single broken byte at
// either end would turn a whole screen of UTF-8 into mojibake for one poll and
// back again on the next. Three guards keep a genuine code-page capture intact:
// the head is only touched when bytes were actually dropped there, the tail
// only while more bytes can still arrive, and a trim is kept only if it makes
// the whole buffer valid UTF-8.
func trimPartialUTF8(b []byte, truncated, midWrite bool) []byte {
	if utf8.Valid(b) {
		return b
	}
	start := 0
	if truncated {
		for start < len(b) && start < 3 && b[start]&0xC0 == 0x80 {
			start++
		}
	}
	end := len(b)
	for i := 1; midWrite && i <= 4 && end-i >= start; i++ {
		c := b[end-i]
		if c&0xC0 == 0x80 {
			continue // inside a sequence, keep walking back to its lead byte
		}
		if c&0x80 != 0 && utf8RuneLen(c) > i {
			end -= i // a lead byte whose sequence has not arrived yet
		}
		break
	}
	if trimmed := b[start:end]; utf8.Valid(trimmed) {
		return trimmed
	}
	return b
}

// utf8RuneLen is the encoded length a UTF-8 lead byte announces (0 when the
// byte cannot lead a sequence).
func utf8RuneLen(lead byte) int {
	switch {
	case lead&0xE0 == 0xC0:
		return 2
	case lead&0xF0 == 0xE0:
		return 3
	case lead&0xF8 == 0xF0:
		return 4
	default:
		return 0
	}
}

// Dropped reports how many bytes fell off the front of the capture because the
// command printed more than operatorOutputCap.
func (c *OperatorCommand) Dropped() int64 { return c.tail.Dropped() }

// Done is closed once the command has been reaped.
func (c *OperatorCommand) Done() <-chan struct{} { return c.sc.waited }

// ExitCode reports the status of a finished command; call it only after Done
// is closed. A command that never reported a status answers -1.
func (c *OperatorCommand) ExitCode() int {
	err := c.sc.exitErr
	if err == nil {
		return 0
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The shell exited cleanly but something it spawned still held the
		// output pipe, exactly as in the tool path: not a failed command.
		if state := c.sc.cmd.ProcessState; state != nil {
			return state.ExitCode()
		}
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// Terminate kills the command and everything it spawned, then waits briefly
// for the process to be reaped so Output covers what it managed to print.
// Blocking, so callers on a UI goroutine should run it on a worker.
func (c *OperatorCommand) Terminate(grace time.Duration) {
	c.Kill(grace)

	timer := time.NewTimer(drainAfterKill)
	defer timer.Stop()
	select {
	case <-c.sc.waited:
	case <-timer.C:
	}
}

// Kill is Terminate without the drain: it returns as soon as the signals are
// out. The console uses it while quitting, where the remaining output will
// never be shown and the shutdown budget is a few seconds for everything.
func (c *OperatorCommand) Kill(grace time.Duration) {
	_ = platform.TerminateProcessGroup(c.sc.cmd, grace)
}

// tailBuffer keeps the last limit bytes written to it and counts what it threw
// away. Trimming happens on the raw bytes, so the first character of a trimmed
// capture can be a split multi-byte sequence; trimPartialUTF8 cleans that up
// before the bytes are decoded.
type tailBuffer struct {
	mu      sync.Mutex
	buf     []byte
	limit   int
	dropped int64
}

// Write implements io.Writer for the running command and never fails: a
// display sink must not make the child see a broken pipe.
func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p) >= t.limit {
		// A single write larger than the limit never reaches the buffer whole:
		// the bound is on what this type allocates, not only on what it keeps.
		t.dropped += int64(len(t.buf) + len(p) - t.limit)
		t.buf = append(t.buf[:0], p[len(p)-t.limit:]...)
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.limit; over > 0 {
		t.dropped += int64(over)
		// Reuse the backing array: copying forward keeps the buffer from
		// growing without bound while the command keeps printing.
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

// Snapshot copies out the retained tail and reports whether anything was
// dropped in front of it, which is what tells a beheaded rune from a leading
// byte that was always there.
func (t *tailBuffer) Snapshot() ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.buf...), t.dropped > 0
}

// Dropped reports the bytes discarded from the front so far.
func (t *tailBuffer) Dropped() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped
}
