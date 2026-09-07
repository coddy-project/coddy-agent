package bgtask

import (
	"os"
	"strings"
	"sync"
	"time"
)

// OutputSink collects a task's combined stdout and stderr. It keeps a bounded
// tail in memory so a chatty task cannot grow without limit, and mirrors every
// byte to a log file when the session is persisted, so the full text survives
// even once the in-memory window has scrolled past it.
type OutputSink struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	total     int64
	truncated bool
	file      *os.File
	lastWrite time.Time
}

// NewOutputSink returns a sink holding at most limit bytes in memory. A
// non-positive limit falls back to defaultOutputBufferBytes.
func NewOutputSink(limit int) *OutputSink {
	if limit <= 0 {
		limit = defaultOutputBufferBytes
	}
	return &OutputSink{limit: limit}
}

// AttachFile mirrors subsequent writes to path. A failure to open the file is
// returned but never fatal: the in-memory window keeps working without it.
func (s *OutputSink) AttachFile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.file = file
	s.mu.Unlock()
	return nil
}

// Write implements io.Writer for the running command.
func (s *OutputSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.total += int64(len(p))
	s.lastWrite = time.Now()
	if s.file != nil {
		_, _ = s.file.Write(p)
	}

	s.buf = append(s.buf, p...)
	if len(s.buf) > s.limit {
		s.buf = s.buf[len(s.buf)-s.limit:]
		s.truncated = true
	}
	return len(p), nil
}

// Close releases the mirror file. The in-memory window stays readable.
func (s *OutputSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		_ = s.file.Close()
		s.file = nil
	}
}

// Stats reports how many bytes the task produced and whether the in-memory
// window dropped any of them.
func (s *OutputSink) Stats() (total int64, truncated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total, s.truncated
}

// LastWrite is when the task last produced output, or the zero time when it
// never has.
func (s *OutputSink) LastWrite() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastWrite
}

// Text returns the retained window as decoded text.
func (s *OutputSink) Text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return decodeOutput(s.buf)
}

// Tail returns at most the last n lines of the retained window. A non-positive
// n returns the whole window.
func (s *OutputSink) Tail(n int) string {
	text := s.Text()
	if n <= 0 || text == "" {
		return text
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
