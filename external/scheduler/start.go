//go:build scheduler

package scheduler

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/daemon"
)

// Start launches the background scheduler daemon when scheduler is effectively enabled.
func Start(ctx context.Context, opts Options) {
	daemon.Start(ctx, opts.Cfg, opts.Log, opts.ProcessCWD, opts.Mgr, opts.Pool)
}
