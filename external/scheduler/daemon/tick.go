//go:build scheduler

package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func jobRunnableForTick(fm *storage.JobFrontmatter) bool {
	return fm != nil && !fm.Paused
}

// runDaemon is the cron loop: once per UTC minute, on the minute, every job
// file is read and the due ones are started.
func runDaemon(ctx context.Context, rt *Runtime, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		evalMinute := storage.TruncateUTCToMinute(time.Now().UTC())
		deadline := evalMinute.Add(time.Minute)
		doTickAtMinute(rt, log, evalMinute)
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

// doTickAtMinute starts every job whose schedule fires at evalMinute and whose
// checkpoint is before it. "Is it running" has one answer, the runtime's; the
// checkpoint is written by StartRun before the run exists, so the next tick,
// a minute away, never sees this slot as due again.
func doTickAtMinute(rt *Runtime, log *slog.Logger, evalMinute time.Time) {
	cfg := rt.cfg()
	if cfg == nil {
		return
	}
	paths, err := storage.ListFlatJobMarkdownFiles(cfg.SchedulerScanRoots())
	if err != nil {
		log.Warn("scheduler scan", "error", err)
		return
	}
	evalMinute = storage.TruncateUTCToMinute(evalMinute)
	for _, path := range paths {
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
		lastSched, err := storage.ReadJobState(storage.StatePath(path))
		if err != nil {
			log.Warn("scheduler state read", "path", path, "error", err)
			continue
		}
		if !storage.CronJobEligibleForMinute(sch, lastSched, evalMinute) {
			continue
		}
		if _, running := rt.RunningRun(path); running {
			log.Debug("scheduler job still running, slot skipped", "job", path, "slot", evalMinute.Format(time.RFC3339))
			continue
		}
		_, err = rt.StartRun(context.Background(), schedservice.RunRequest{
			JobPath:     path,
			Frontmatter: fm,
			Body:        body,
			Trigger:     schedservice.TriggerCron,
			FireSlot:    evalMinute,
			UpdateState: true,
		})
		switch {
		case err == nil:
		case errors.Is(err, schedservice.ErrJobBusy):
			log.Debug("scheduler job still running, slot skipped", "job", path)
		case errors.Is(err, schedservice.ErrQueueSaturated):
			log.Warn("scheduler max_queue saturated, skipping job until a run finishes (raise scheduler.max_queue if needed)",
				"job", path, "max_queue", cfg.Scheduler.MaxQueue)
		default:
			// The checkpoint may already be written for this slot: it is
			// committed before the run is created, so the slot does not fire
			// again on the next tick. The operator sees the reason here.
			log.Warn("scheduler run not started; its cron slot is checkpointed and will not fire again", "job", path, "slot", evalMinute.Format(time.RFC3339), "error", err)
		}
	}
}

// warnTimeoutCap says at start when scheduler.timeout is above what the pool
// will honour: every task is capped by tools.background.max_timeout_seconds.
// It reads the configuration the daemon started with; a reload that moves
// either knob is not re-checked, which is what a one-shot start diagnostic
// is. The supervisor starts a fresh daemon when scheduler.timeout changes.
func warnTimeoutCap(cfg *config.Config, log *slog.Logger) {
	if cfg == nil {
		return
	}
	d, err := time.ParseDuration(cfg.Scheduler.Timeout)
	if err != nil {
		return
	}
	capSeconds := cfg.Tools.Background.Resolved().MaxTimeoutSeconds
	if capSeconds > 0 && int(d/time.Second) > capSeconds {
		log.Warn("scheduler.timeout is above tools.background.max_timeout_seconds; runs are capped by the pool",
			"scheduler.timeout", cfg.Scheduler.Timeout, "max_timeout_seconds", capSeconds)
	}
}
