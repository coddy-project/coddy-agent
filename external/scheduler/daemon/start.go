//go:build scheduler

package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Start launches the scheduler daemon when the configuration enables it: the
// runtime is registered with the service (so the HTTP handlers and the tools
// reach it), the cron loop starts, and both are withdrawn when ctx ends. cfg
// is read live, so a reload the manager applied is what the next tick reads;
// what a reload cannot change in place (the directory, the limits) is the
// supervisor's reason to start a fresh daemon.
func Start(ctx context.Context, cfg func() *config.Config, log *slog.Logger, processCWD string, mgr *session.Manager, pool *bgtask.Pool) {
	c := cfg()
	if c == nil || !c.SchedulerEffectiveEnabled() {
		return
	}
	if mgr == nil {
		if log != nil {
			log.Error("scheduler needs a session manager; not started")
		}
		return
	}
	if log == nil {
		log = slog.Default()
	}
	// Tag once here: an inline "component" attribute reads the same in a log
	// file but cannot scope logger.levels, because slog decides whether to build
	// a record before any attribute of it exists.
	log = logger.Component(log, logger.ComponentScheduler)
	pcwd := strings.TrimSpace(processCWD)
	if pcwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			log.Warn("scheduler could not resolve cwd", "error", err)
			return
		}
		pcwd = wd
	}
	rt := NewRuntime(ctx, cfg, mgr, pool, log, pcwd)
	schedservice.SetRuntime(rt)
	warnTimeoutCap(c, log)
	log.Info("scheduler daemon enabled", "dir", c.Scheduler.Dir)
	go func() {
		runDaemon(ctx, rt, log)
		if schedservice.CurrentRuntime() == rt {
			schedservice.SetRuntime(nil)
		}
	}()
}
