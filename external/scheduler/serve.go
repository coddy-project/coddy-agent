package scheduler

import (
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Options are what one scheduler daemon is built from.
type Options struct {
	// Cfg reads the live configuration the daemon reads its jobs and limits
	// from.
	Cfg func() *config.Config
	// Log is the process logger.
	Log *slog.Logger
	// ProcessCWD is the workspace a job gets when its definition names none.
	ProcessCWD string
	// Mgr owns the sessions a run is made of: the job session every run is a
	// child of, and the run session that holds the transcript.
	Mgr *session.Manager
	// Pool is the background task pool the runs are tasks of; nil means the
	// process-wide default.
	Pool *bgtask.Pool
}
