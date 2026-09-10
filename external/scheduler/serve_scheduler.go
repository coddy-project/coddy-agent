//go:build scheduler

package scheduler

import (
	"context"
	"errors"
)

// Available reports whether this binary can run the scheduler.
const Available = true

// Serve runs the cron daemon until ctx is cancelled.
//
// daemon.Start returns as soon as its goroutines are up, so the block happens
// here: the supervisor above treats every subsystem the same way, and one that
// returned immediately would read as a surface that stopped on its own.
func Serve(ctx context.Context, opts Options) error {
	if opts.Cfg == nil || opts.Log == nil {
		return errors.New("scheduler: Cfg and Log are required")
	}
	Start(ctx, opts.Cfg, opts.Log, opts.ProcessCWD)
	<-ctx.Done()
	return nil
}
