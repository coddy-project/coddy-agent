//go:build scheduler

package daemon

import (
	"sync"
	"time"
)

// In-process guard so poll ticks do not launch the same cron fire slot twice when .state
// lags behind (read races, transient IO) while the exclusive lock is not held yet.
var (
	spawnDedupeMu sync.Mutex
	spawnDedupe   = map[string]time.Time{} // abs job .md path -> last dueSlot already dispatched
)

func shouldSkipDuplicateCronSpawn(absJobPath string, dueSlot time.Time, lastFromDisk time.Time) bool {
	spawnDedupeMu.Lock()
	defer spawnDedupeMu.Unlock()
	if !lastFromDisk.IsZero() && !lastFromDisk.Before(dueSlot) {
		delete(spawnDedupe, absJobPath)
		// Checkpoint on disk already covers this fire (or newer): do not launch again.
		return true
	}
	prev, ok := spawnDedupe[absJobPath]
	if ok && prev.Equal(dueSlot) {
		return true
	}
	return false
}

func noteSpawnDispatched(absJobPath string, dueSlot time.Time) {
	spawnDedupeMu.Lock()
	spawnDedupe[absJobPath] = dueSlot
	spawnDedupeMu.Unlock()
}

// clearSpawnDedupeEntry removes in-memory dedupe state for a job path (for example after the
// schedule string in the *.md file changes so stale slot keys do not affect the next tick).
func clearSpawnDedupeEntry(absJobPath string) {
	spawnDedupeMu.Lock()
	delete(spawnDedupe, absJobPath)
	spawnDedupeMu.Unlock()
}
