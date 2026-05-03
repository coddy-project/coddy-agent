package logger

import (
	"io"
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// newHandler builds the slog.Handler matching cfg.Format and cfg.Level.
func newHandler(w io.Writer, cfg config.Logger) slog.Handler {
	opts := &slog.HandlerOptions{Level: levelOf(cfg.Level)}
	if cfg.Format == config.LogFormatJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

func levelOf(name string) slog.Level {
	switch name {
	case config.LogLevelDebug:
		return slog.LevelDebug
	case config.LogLevelWarn:
		return slog.LevelWarn
	case config.LogLevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
