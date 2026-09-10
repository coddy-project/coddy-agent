package logger

import (
	"io"
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// newHandler builds the slog.Handler matching cfg.Format, cfg.Level and the
// per-component overrides in cfg.Levels.
//
// The format handler is opened at the lowest level any component asks for,
// because slog decides whether to build a record before it can see which
// component is logging; componentHandler then drops what that component did
// not ask for. Without overrides there is nothing to scope and the format
// handler's own threshold is the whole story.
func newHandler(w io.Writer, cfg config.Logger) slog.Handler {
	opts := &slog.HandlerOptions{Level: minLevel(cfg)}
	var h slog.Handler
	if cfg.Format == config.LogFormatJSON {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return newComponentHandler(h, cfg)
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
