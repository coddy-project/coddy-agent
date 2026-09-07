package shell

import (
	"bytes"
	"io"
	"sync"
)

// switchWriter buffers a command's output until somebody takes it over.
//
// exec.Cmd fixes Stdout at Start, so a foreground run cannot simply hand its
// pipe to the background pool when the command turns out to be long-lived. It
// always writes here instead; adoption then points this writer at the task's
// output sink and flushes everything captured so far, so the background log
// reads as one stream from the very first byte.
//
// It must stay an io.Writer and never become an *os.File: exec passes a real
// file straight to the child and there is nothing left to redirect.
type switchWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	target io.Writer
}

// Write implements io.Writer for the running command. It never reports an
// error, so a failing target cannot make the child see a broken pipe.
func (w *switchWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.target != nil {
		_, _ = w.target.Write(p)
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

// Redirect sends everything from here on to target, flushes what was buffered
// into it, and returns that prefix so the caller can show it. Redirecting twice
// is a no-op that returns nil: the first caller owns the stream.
func (w *switchWriter) Redirect(target io.Writer) []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.target != nil || target == nil {
		return nil
	}
	w.target = target
	prefix := append([]byte(nil), w.buf.Bytes()...)
	if len(prefix) > 0 {
		_, _ = target.Write(prefix)
	}
	return prefix
}

// Bytes returns a copy of what has been captured while no target was attached.
// It is a copy because the command may still be writing: handing out the live
// buffer would race with the next append.
func (w *switchWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}
