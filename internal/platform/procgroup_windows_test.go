//go:build windows

package platform

import (
	"runtime"
	"testing"
	"time"
)

// Windows keeps a process object alive for as long as anybody holds a handle to
// it, so a pid stays openable after the process itself is gone. Asking whether
// the pid can be opened therefore answers the wrong question: the survivor logic
// would report a corpse as "still alive from an earlier run" and background_reap
// would claim to have killed it.
//
// The unix probe cannot get this wrong - an empty process group is ESRCH - which
// is why this test has no cross-platform counterpart.
func TestProcessGroupAliveRejectsAKilledProcessWhoseHandleIsRetained(t *testing.T) {
	cmd, pid, started := startProbeHelper(t)

	if err := TerminateProcessGroupByPID(pid, time.Second); err != nil {
		t.Fatalf("TerminateProcessGroupByPID(): %v", err)
	}
	// Deliberately no cmd.Wait(): the handle stays open, which is the ordinary
	// state after a run that was killed rather than drained.

	alive := waitForProbe(pid, started, false)
	runtime.KeepAlive(cmd)
	if !alive {
		t.Fatalf("ProcessGroupAlive(%d) = true for a killed process whose handle is still open", pid)
	}
}

// Windows hands out pids again quickly and, unlike the unix probe, opening one
// by number matches any process rather than only a group leader. A record whose
// pid has been recycled must not be offered for reaping: background_reap runs
// taskkill /T /F on it, which would take down an unrelated process tree.
//
// The recorded start time is what tells the two apart - a task's process is
// created moments after the pool stamps StartedAt, while a recycled pid belongs
// to something started at a completely different time.
func TestProcessGroupAliveRejectsARecycledPID(t *testing.T) {
	_, pid, started := startProbeHelper(t)

	if ProcessGroupAlive(pid, started.Add(-time.Hour)) {
		t.Fatalf("ProcessGroupAlive(%d) = true for a record started an hour before this process", pid)
	}
	if ProcessGroupAlive(pid, started.Add(time.Hour)) {
		t.Fatalf("ProcessGroupAlive(%d) = true for a record started an hour after this process", pid)
	}

	// The genuine record still matches, or the fix would simply have made the
	// probe answer false to everything.
	if !ProcessGroupAlive(pid, started) {
		t.Fatalf("ProcessGroupAlive(%d) = false for the process the record describes", pid)
	}
}

// A record from before pids were persisted carries no usable start time. The
// probe then falls back to liveness alone rather than becoming stricter than the
// implementation it replaces.
func TestProcessGroupAliveFallsBackWithoutAStartTime(t *testing.T) {
	_, pid, _ := startProbeHelper(t)

	if !ProcessGroupAlive(pid, time.Time{}) {
		t.Fatalf("ProcessGroupAlive(%d) = false without a recorded start time", pid)
	}
}
