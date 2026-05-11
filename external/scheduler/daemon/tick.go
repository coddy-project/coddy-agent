//go:build scheduler

package daemon

import (
	"context"
	"log/slog"
	"time"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func jobRunnableForTick(fm *storage.JobFrontmatter) bool {
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
	paths, err := storage.ListFlatJobMarkdownFiles(cfg.SchedulerScanRoots())
	if err != nil {
		log.Warn("scheduler scan", "error", err)
		return
	}
	now := time.Now().UTC()
	grace := schedservice.StaleLockGraceFromConfig(cfg)
	for _, path := range paths {
		_ = schedservice.CleanupStaleSchedulerLock(path, grace)
		fm, body, err := storage.ParseJobFile(path)
		if err != nil {
			log.Debug("scheduler skip file", "path", path, "error", err)
			continue
		}
		if !jobRunnableForTick(fm) {
			continue
		}
		sch, err := storage.ParseCronUTC(fm.Schedule)
		if err != nil {
			log.Warn("scheduler bad cron", "path", path, "error", err)
			continue
		}
		last, err := storage.ReadJobState(storage.StatePath(path))
		if err != nil {
			log.Warn("scheduler state read", "path", path, "error", err)
			continue
		}
		slot := storage.DueFireSlotUTC(sch, last, now)
		if slot.After(now) {
			continue
		}
		select {
		case sem <- struct{}{}:
			go func(p string, fm *storage.JobFrontmatter, instruction string, fire time.Time) {
				defer func() { <-sem }()
				_ = RunJobFile(ctx, cfg, log, processCWD, p, fire, true, fm, instruction)
			}(path, fm, body, slot)
		default:
			log.Warn("scheduler max_queue saturated, skipping job until a run finishes (raise scheduler.max_queue if needed)",
				"job", path, "max_queue", maxQueue, "component", "scheduler")
		}
	}
}
