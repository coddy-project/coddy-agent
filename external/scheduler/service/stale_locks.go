//go:build scheduler

package schedservice

import (
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// StaleLockGraceFromConfig returns how old a basename.lock may get with no tracked run
// before ListJobs, the daemon tick, or explicit cancel may remove it.
func StaleLockGraceFromConfig(c *config.Config) time.Duration {
	const floor = 2 * time.Minute
	if c == nil {
		return floor
	}
	d := floor
	if td, err := time.ParseDuration(strings.TrimSpace(c.Scheduler.Timeout)); err == nil && td > 0 {
		x := td + 20*time.Second
		if x > d {
			d = x
		}
	}
	return d
}

// CleanupStaleSchedulerLock removes basename.lock when it is older than grace and no
// in-process run is registered for abs. Returns true if the lock file was removed.
func CleanupStaleSchedulerLock(abs string, grace time.Duration) bool {
	if IsTrackedJob(abs) {
		return false
	}
	lp := storage.LockPath(abs)
	fi, err := os.Stat(lp)
	if err != nil {
		return false
	}
	if time.Since(fi.ModTime()) < grace {
		return false
	}
	return os.Remove(lp) == nil
}
