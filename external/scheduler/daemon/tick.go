//go:build scheduler

package daemon

import (
	"context"
	"log/slog"
	"strings"
	"time"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// lastSeenScheduleByPath tracks the prior schedule string per job file so we can drop stale
// spawn-dedupe entries when the operator edits the cron expression without restarting coddy.
var lastSeenScheduleByPath = map[string]string{}

func jobRunnableForTick(fm *storage.JobFrontmatter) bool {
	return fm != nil && !fm.Paused
}

func runDaemon(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD string) {
	mq := cfg.Scheduler.MaxQueue
	if mq <= 0 {
		mq = 10
	}
	sem := make(chan struct{}, mq)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		evalMinute := storage.TruncateUTCToMinute(time.Now().UTC())
		deadline := evalMinute.Add(time.Minute)
		doTickAtMinute(ctx, cfg, log, processCWD, sem, mq, evalMinute)
		d := time.Until(deadline)
		if d < 0 {
			d = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
}

func doTickAtMinute(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD string, sem chan struct{}, maxQueue int, evalMinute time.Time) {
	paths, err := storage.ListFlatJobMarkdownFiles(cfg.SchedulerScanRoots())
	if err != nil {
		log.Warn("scheduler scan", "error", err)
		return
	}
	evalMinute = storage.TruncateUTCToMinute(evalMinute)
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
		schedNorm := strings.TrimSpace(fm.Schedule)
		if prev, ok := lastSeenScheduleByPath[path]; ok && prev != schedNorm {
			clearSpawnDedupeEntry(path)
		}
		lastSeenScheduleByPath[path] = schedNorm

		lastSched, err := storage.ReadJobState(storage.StatePath(path))
		if err != nil {
			log.Warn("scheduler state read", "path", path, "error", err)
			continue
		}
		if !storage.CronJobEligibleForMinute(sch, lastSched, evalMinute) {
			continue
		}
		slot := evalMinute
		lockPath := storage.LockPath(path)
		if lt, ok := storage.ReadSchedulerLockFireSlotUTC(lockPath); ok && lt.Equal(slot) {
			continue
		}
		if shouldSkipDuplicateCronSpawn(path, slot, lastSched) {
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
