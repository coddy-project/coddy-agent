//go:build scheduler

package scheduler

import (
	"context"
	"log/slog"
	"time"

	sched "github.com/EvilFreelancer/coddy-agent/external/scheduler/lib"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/schedulerops"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func init() {
	schedulerops.LaunchManualJob = launchManualScheduledJob
}

func launchManualScheduledJob(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD, absJob string, fm *sched.JobFrontmatter, instruction string) error {
	if fm == nil {
		return nil
	}
	return runJobFile(ctx, cfg, log, processCWD, absJob, time.Time{}, false, fm, instruction)
}
