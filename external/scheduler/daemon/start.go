//go:build scheduler

package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
)

// Start launches the background scheduler daemon when scheduler is effectively enabled.
func Start(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD string) {
	if cfg == nil || !cfg.SchedulerEffectiveEnabled() {
		return
	}
	// Tag once here: an inline "component" attribute reads the same in a log
	// file but cannot scope logger.levels, because slog decides whether to build
	// a record before any attribute of it exists.
	log = logger.Component(log, logger.ComponentScheduler)
	pcwd := strings.TrimSpace(processCWD)
	if pcwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			if log != nil {
				log.Warn("scheduler could not resolve cwd", "error", err)
			}
			return
		}
		pcwd = wd
	}
	log.Info("scheduler daemon enabled", "dir", cfg.Scheduler.Dir)
	go runDaemon(ctx, cfg, log, pcwd)
}
