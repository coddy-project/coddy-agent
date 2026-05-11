//go:build scheduler

package scheduler

import (
	"context"
	"log/slog"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/daemon"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Start launches the background scheduler daemon when scheduler is effectively enabled.
func Start(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD string) {
	daemon.Start(ctx, cfg, log, processCWD)
}
