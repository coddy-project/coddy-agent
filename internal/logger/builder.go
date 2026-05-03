package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// New builds a *slog.Logger from cfg. If cfg.Outputs lists "file" the file
// writer participates in rotation as configured. The returned closer must be
// invoked at process exit; it closes the file output if any.
//
// Validate is called automatically; the returned error is formatted to be
// shown to the user as-is.
func New(cfg Config) (*slog.Logger, io.Closer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, noopCloser{}, err
	}

	writers := make([]io.Writer, 0, len(cfg.Outputs))
	var fileCloser io.Closer = noopCloser{}

	for _, o := range cfg.Outputs {
		switch o {
		case OutputStdout:
			writers = append(writers, os.Stdout)
		case OutputStderr:
			writers = append(writers, os.Stderr)
		case OutputFile:
			rf, err := newRotatingFile(cfg.File, cfg.Rotation)
			if err != nil {
				return nil, noopCloser{}, fmt.Errorf("logger: open file output: %w", err)
			}
			writers = append(writers, rf)
			fileCloser = rf
		}
	}

	w := io.MultiWriter(writers...)
	handler := newHandler(w, cfg)
	return slog.New(handler), fileCloser, nil
}

// MustNew is like New but panics on error. Useful in tests and at startup
// when a misconfigured logger should fail loud.
func MustNew(cfg Config) (*slog.Logger, io.Closer) {
	l, c, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return l, c
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }
