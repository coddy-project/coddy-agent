//go:build scheduler

package schedulerops

import (
	"context"
	"log/slog"

	sched "github.com/EvilFreelancer/coddy-agent/external/scheduler/lib"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// LaunchManualJob executes one asynchronous manual scheduler job (wired from package scheduler via init).
var LaunchManualJob func(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD, absJobMD string, fm *sched.JobFrontmatter, instruction string) error
