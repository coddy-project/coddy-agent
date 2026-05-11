//go:build scheduler

package schedulerops

import (
	"context"
	"strings"
	"sync"
)

var (
	trackMu sync.Mutex
	tracked = map[string]context.CancelFunc{}
)

// RegisterTrackedRun records a cancel function for an active job markdown path (absolute, clean).
func RegisterTrackedRun(absJobMD string, cancel context.CancelFunc) {
	if cancel == nil {
		return
	}
	ap := strings.TrimSpace(absJobMD)
	if ap == "" {
		return
	}
	trackMu.Lock()
	tracked[ap] = cancel
	trackMu.Unlock()
}

// UnregisterTrackedRun removes a job path from the active run map.
func UnregisterTrackedRun(absJobMD string) {
	ap := strings.TrimSpace(absJobMD)
	if ap == "" {
		return
	}
	trackMu.Lock()
	delete(tracked, ap)
	trackMu.Unlock()
}

// CancelTrackedRun invokes cancel for given job path when a run is active. Returns whether a run was cancelled.
func CancelTrackedRun(absJobMD string) bool {
	ap := strings.TrimSpace(absJobMD)
	if ap == "" {
		return false
	}
	trackMu.Lock()
	fn := tracked[ap]
	trackMu.Unlock()
	if fn == nil {
		return false
	}
	fn()
	return true
}

// IsTrackedJob reports whether absJobMD has an active registered cancel (scheduler run in flight).
func IsTrackedJob(absJobMD string) bool {
	ap := strings.TrimSpace(absJobMD)
	if ap == "" {
		return false
	}
	trackMu.Lock()
	_, ok := tracked[ap]
	trackMu.Unlock()
	return ok
}

// TrackedJobRunCount returns how many runs are currently tracked.
func TrackedJobRunCount() int {
	trackMu.Lock()
	defer trackMu.Unlock()
	return len(tracked)
}
