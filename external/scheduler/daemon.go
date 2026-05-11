//go:build scheduler

package scheduler

import (
	"context"
	"log/slog"
	"time"

	sched "github.com/EvilFreelancer/coddy-agent/external/scheduler/lib"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func jobRunnableForTick(fm *sched.JobFrontmatter) bool {
	return fm != nil && !fm.Paused
}

func runDaemon(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD string) {
	d, err := time.ParseDuration(cfg.Scheduler.PollInterval)
	if err != nil || d < time.Second {
		d = time.Minute
	}
	mq := cfg.Scheduler.MaxQueue
	if mq <= 0 {
		mq = 10
	}
	sem := make(chan struct{}, mq)
	tickFn := func() {
		doTick(ctx, cfg, log, processCWD, sem, mq)
	}
	tickFn()
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tickFn()
		}
	}
}

func doTick(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD string, sem chan struct{}, maxQueue int) {
	paths, err := sched.ListFlatJobMarkdownFiles(cfg.SchedulerScanRoots())
	if err != nil {
		log.Warn("scheduler scan", "error", err)
		return
	}
	now := time.Now().UTC()
	for _, path := range paths {
		fm, body, err := sched.ParseJobFile(path)
		if err != nil {
			log.Debug("scheduler skip file", "path", path, "error", err)
			continue
		}
		if !jobRunnableForTick(fm) {
			continue
		}
		sch, err := sched.ParseCronUTC(fm.Schedule)
		if err != nil {
			log.Warn("scheduler bad cron", "path", path, "error", err)
			continue
		}
		last, err := sched.ReadJobState(sched.StatePath(path))
		if err != nil {
			log.Warn("scheduler state read", "path", path, "error", err)
			continue
		}
		slot := sched.NextScheduledUTC(sch, last)
		if slot.After(now) {
			continue
		}
		select {
		case sem <- struct{}{}:
			go func(p string, fm *sched.JobFrontmatter, instruction string, fire time.Time) {
				defer func() { <-sem }()
				_ = runJobFile(ctx, cfg, log, processCWD, p, fire, true, fm, instruction)
			}(path, fm, body, slot)
		default:
			log.Warn("scheduler max_queue saturated, skipping job until a run finishes (raise scheduler.max_queue if needed)",
				"job", path, "max_queue", maxQueue, "component", "scheduler")
		}
	}
}
