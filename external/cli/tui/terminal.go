//go:build cli

package tui

import (
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// ProcessTerminal drives the real tty: raw mode, bracketed paste,
// modifyOtherKeys, resize notification, cursor visibility.
type ProcessTerminal struct {
	mu sync.Mutex

	in       *os.File
	out      io.Writer
	oldState *term.State

	cols, rows int
	onInput    func([]byte)
	onResize   func()
	stopCh     chan struct{}
	stopped    bool
	plain      bool
}

// NewProcessTerminal creates a terminal over stdin/stdout.
func NewProcessTerminal() *ProcessTerminal {
	return &ProcessTerminal{in: os.Stdin, out: os.Stdout}
}

// SetPlain disables control-sequence negotiation (used by --plain e2e mode:
// no modifyOtherKeys push, no queries; raw mode and resize still work).
func (t *ProcessTerminal) SetPlain(v bool) { t.plain = v }

// IsTerminal reports whether both stdin and stdout are ttys.
func IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// Start switches to raw mode and begins the input and resize loops.
func (t *ProcessTerminal) Start(onInput func([]byte), onResize func()) error {
	st, err := term.MakeRaw(int(t.in.Fd()))
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.oldState = st
	t.onInput = onInput
	t.onResize = onResize
	t.stopCh = make(chan struct{})
	t.stopped = false
	t.mu.Unlock()

	if err := enableVT(); err != nil {
		// Non-fatal: POSIX terminals do not need it.
		_ = err
	}
	t.refreshSize()
	// Bracketed paste stays on even in plain mode (deterministic and needed
	// for paste-collapse behavior); only protocol negotiation is skipped.
	t.Write("\x1b[?2004h")
	if !t.plain {
		t.Write("\x1b[>4;2m") // modifyOtherKeys mode 2 (pi fallback path)
	}
	go t.readLoop()
	watchResize(t)
	return nil
}

// Stop restores the terminal state.
func (t *ProcessTerminal) Stop() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	close(t.stopCh)
	old := t.oldState
	t.mu.Unlock()
	if !t.plain {
		t.Write("\x1b[>4;0m")
		t.Write("\x1b]0;\x07") // reset the window title set by SetTitle
	}
	t.Write("\x1b[?2004l")
	t.ShowCursor()
	if old != nil {
		_ = term.Restore(int(t.in.Fd()), old)
	}
}

func (t *ProcessTerminal) readLoop() {
	buf := make([]byte, 65536)
	for {
		n, err := t.in.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			t.mu.Lock()
			cb := t.onInput
			stopped := t.stopped
			t.mu.Unlock()
			if stopped {
				return
			}
			if cb != nil {
				cb(data)
			}
		}
		if err != nil {
			return
		}
		select {
		case <-t.stopCh:
			return
		default:
		}
	}
}

func (t *ProcessTerminal) refreshSize() {
	cols, rows, err := term.GetSize(int(t.out.(*os.File).Fd()))
	if err != nil || cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	t.mu.Lock()
	changed := cols != t.cols || rows != t.rows
	t.cols, t.rows = cols, rows
	cb := t.onResize
	t.mu.Unlock()
	if changed && cb != nil {
		cb()
	}
}

// Write sends raw bytes to the terminal.
func (t *ProcessTerminal) Write(s string) { _, _ = io.WriteString(t.out, s) }

// Columns returns the terminal width.
func (t *ProcessTerminal) Columns() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cols <= 0 {
		return 80
	}
	return t.cols
}

// Rows returns the terminal height.
func (t *ProcessTerminal) Rows() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rows <= 0 {
		return 24
	}
	return t.rows
}

// HideCursor hides the hardware cursor.
func (t *ProcessTerminal) HideCursor() { t.Write("\x1b[?25l") }

// ShowCursor shows the hardware cursor.
func (t *ProcessTerminal) ShowCursor() { t.Write("\x1b[?25h") }

// SetTitle sets the terminal window title.
func (t *ProcessTerminal) SetTitle(title string) {
	if !t.plain {
		t.Write("\x1b]0;" + title + "\x07")
	}
}

// EscTimeout returns the lone-ESC resolution timeout (pi: 100 ms over SSH,
// 10 ms locally).
func EscTimeout() time.Duration {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return 100 * time.Millisecond
	}
	return 10 * time.Millisecond
}
