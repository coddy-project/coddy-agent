//go:build scheduler

package daemon

import (
	"testing"
	"time"
)

func TestSpawnDedupeSkipsSecondLaunchSameSlot(t *testing.T) {
	p := "/tmp/demo.md"
	slot, err := time.Parse(time.RFC3339, "2026-05-12T00:02:00Z")
	if err != nil {
		t.Fatal(err)
	}
	var zero time.Time
	if shouldSkipDuplicateCronSpawn(p, slot, zero) {
		t.Fatal("first check should not skip")
	}
	noteSpawnDispatched(p, slot)
	if !shouldSkipDuplicateCronSpawn(p, slot, zero) {
		t.Fatal("second launch same slot should skip while disk last still empty")
	}
}

// RunJobFile registers noteSpawnDispatched before WriteJobState so a failing checkpoint write
// still suppresses repeated launches for the same cron slot on subsequent polls.
func TestSpawnDedupeRegistersBeforeCheckpointWrite(t *testing.T) {
	p := "/tmp/demo-state-write.md"
	slot, err := time.Parse(time.RFC3339, "2026-05-12T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	var zero time.Time
	noteSpawnDispatched(p, slot)
	if !shouldSkipDuplicateCronSpawn(p, slot, zero) {
		t.Fatal("same-slot poll retry must skip even when .state write failed")
	}
}

func TestSpawnDedupeClearsWhenDiskCaughtUp(t *testing.T) {
	p := "/tmp/other.md"
	slot, err := time.Parse(time.RFC3339, "2026-05-12T00:05:00Z")
	if err != nil {
		t.Fatal(err)
	}
	var zero time.Time
	noteSpawnDispatched(p, slot)
	last := slot
	if !shouldSkipDuplicateCronSpawn(p, slot, last) {
		t.Fatal("when last on disk equals due slot, must skip duplicate spawn")
	}
	if shouldSkipDuplicateCronSpawn(p, slot, zero) {
		t.Fatal("after disk catch-up path, mem entry should be gone")
	}
}
