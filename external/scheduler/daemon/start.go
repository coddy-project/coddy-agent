//go:build scheduler

package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Start launches the background scheduler daemon when scheduler is effectively enabled.
func Start(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD string) {
	if cfg == nil || !cfg.SchedulerEffectiveEnabled() {
		return
	}
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
	log.Info("scheduler daemon enabled", "poll", cfg.Scheduler.PollInterval, "dir", cfg.Scheduler.Dir, "component", "scheduler")
	go runDaemon(ctx, cfg, log, pcwd)
}
