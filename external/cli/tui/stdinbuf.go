//go:build cli

package tui

import (
	"strings"
	"sync"
	"time"
)

// Bracketed paste delimiters.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// StdinBuffer reassembles chunked terminal input into complete sequences
// (port of pi-tui StdinBuffer). Emit receives one complete key sequence per
// call; EmitPaste receives the body of a bracketed paste.
type StdinBuffer struct {
	mu     sync.Mutex
	buf    []byte
	paste  []byte
	inPast bool

	escTimeout time.Duration
	timer      *time.Timer

	Emit      func(seq []byte)
	EmitPaste func(body []byte)
}

// NewStdinBuffer creates a buffer with the given lone-ESC timeout.
func NewStdinBuffer(escTimeout time.Duration) *StdinBuffer {
	return &StdinBuffer{escTimeout: escTimeout}
}

// Feed consumes a raw chunk from the tty.
func (b *StdinBuffer) Feed(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.buf = append(b.buf, data...)
	b.drain()
}

func (b *StdinBuffer) drain() {
	for len(b.buf) > 0 {
		if b.inPast {
			idx := strings.Index(string(b.buf), pasteEnd)
			if idx < 0 {
				return // wait for the paste terminator
			}
			b.paste = append(b.paste, b.buf[:idx]...)
			b.buf = b.buf[idx+len(pasteEnd):]
			body := b.paste
			b.paste = nil
			b.inPast = false
			if b.EmitPaste != nil {
				b.mu.Unlock()
				b.EmitPaste(body)
				b.mu.Lock()
			}
			continue
		}
		n, complete := nextSequenceLen(b.buf)
		if n == 0 {
			return
		}
		if !complete {
			// Incomplete escape sequence: wait for more input, or resolve a
			// lone ESC after the timeout.
			if len(b.buf) >= 1 && b.buf[0] == 0x1b {
				b.armEscTimer()
			}
			return
		}
		seq := make([]byte, n)
		copy(seq, b.buf[:n])
		b.buf = b.buf[n:]
		if string(seq) == pasteStart {
			b.inPast = true
			continue
		}
		if b.Emit != nil {
			b.mu.Unlock()
			b.Emit(seq)
			b.mu.Lock()
		}
	}
}

func (b *StdinBuffer) armEscTimer() {
	if b.timer != nil {
		return
	}
	b.timer = time.AfterFunc(b.escTimeout, func() {
		b.mu.Lock()
		b.timer = nil
		if len(b.buf) > 0 && b.buf[0] == 0x1b {
			// Treat the pending ESC (or ESC-prefixed partial) as-is.
			seq := make([]byte, len(b.buf))
			copy(seq, b.buf)
			b.buf = nil
			b.mu.Unlock()
			if b.Emit != nil {
				b.Emit(seq)
			}
			return
		}
		b.mu.Unlock()
	})
}

// nextSequenceLen returns the length of the first complete sequence in buf and
// whether it is complete. Length 0 means empty buffer.
func nextSequenceLen(buf []byte) (int, bool) {
	if len(buf) == 0 {
		return 0, false
	}
	if buf[0] != 0x1b {
		// UTF-8 rune or plain byte: emit the full contiguous non-ESC run.
		i := 0
		for i < len(buf) && buf[i] != 0x1b {
			i++
		}
		return i, true
	}
	if len(buf) == 1 {
		return 1, false // lone ESC so far
	}
	switch buf[1] {
	case '[':
		// CSI: parameters 0x30-0x3F, intermediates 0x20-0x2F, final 0x40-0x7E.
		i := 2
		for i < len(buf) {
			c := buf[i]
			if c >= 0x40 && c <= 0x7e {
				// Legacy mouse: ESC [ M followed by 3 bytes.
				if c == 'M' && i == 2 {
					if len(buf) >= 6 {
						return 6, true
					}
					return len(buf), false
				}
				return i + 1, true
			}
			i++
		}
		return len(buf), false
	case ']', '_', 'P', '^':
		// OSC/APC/DCS/PM: terminated by BEL or ST.
		i := 2
		for i < len(buf) {
			if buf[i] == 0x07 {
				return i + 1, true
			}
			if buf[i] == 0x1b && i+1 < len(buf) && buf[i+1] == '\\' {
				return i + 2, true
			}
			i++
		}
		return len(buf), false
	case 'O':
		if len(buf) >= 3 {
			return 3, true
		}
		return len(buf), false
	case 0x1b:
		// ESC ESC: emit the first ESC alone.
		return 1, true
	default:
		// Meta: ESC + one UTF-8 rune.
		n := utf8SeqLen(buf[1])
		if len(buf) >= 1+n {
			return 1 + n, true
		}
		return len(buf), false
	}
}

func utf8SeqLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b&0xe0 == 0xc0:
		return 2
	case b&0xf0 == 0xe0:
		return 3
	case b&0xf8 == 0xf0:
		return 4
	}
	return 1
}
