package scheduler

import (
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Options are what one scheduler daemon is built from.
type Options struct {
	// Cfg is the configuration the daemon reads its jobs and limits from.
	Cfg *config.Config
	// Log is the process logger.
	Log *slog.Logger
	// ProcessCWD is the workspace a job gets when its definition names none.
	ProcessCWD string
}
