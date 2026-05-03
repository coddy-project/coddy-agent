package logger

import (
	"io"
	"log/slog"
)

// newHandler builds the slog.Handler matching cfg.Format and cfg.Level.
func newHandler(w io.Writer, cfg Config) slog.Handler {
	opts := &slog.HandlerOptions{Level: levelOf(cfg.Level)}
	if cfg.Format == FormatJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

func levelOf(name string) slog.Level {
	switch name {
	case LevelDebug:
		return slog.LevelDebug
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
