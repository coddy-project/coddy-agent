//go:build windows

package platform

import (
	"os"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"
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

	if err := TerminateProcessGroupByPID(pid, started, time.Second); err != nil {
		t.Fatalf("TerminateProcessGroupByPID(): %v", err)
	}
	// Deliberately no cmd.Wait(): the handle stays open. A coddy that is killed
	// loses its own handles with it, so the holder in production is usually the
	// probe itself - os.FindProcess opened one per call and left it to the
	// runtime to close, which is how a corpse stayed resolvable for the rest of
	// the run. This test reproduces that state with a handle it keeps on purpose.

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
// The creation time is what tells the two apart, and this is where that
// comparison is pinned. It does not wait for a real pid to be recycled - nothing
// makes Windows do that on demand - so it drives the same decision the other way
// round: one live process, several creation times the record could claim. A
// recycled pid is the case where the time on file belongs to a process that is
// gone, which is exactly a mismatch.
func TestProcessGroupAliveRejectsACreationTimeTheRecordDoesNotMatch(t *testing.T) {
	_, pid, started := startProbeHelper(t)

	if ProcessGroupAlive(pid, started.Add(-time.Minute)) {
		t.Fatalf("ProcessGroupAlive(%d) = true for a process created a minute after the recorded task", pid)
	}
	if ProcessGroupAlive(pid, started.Add(-time.Hour)) {
		t.Fatalf("ProcessGroupAlive(%d) = true for a record started an hour before this process", pid)
	}
	if ProcessGroupAlive(pid, started.Add(time.Hour)) {
		t.Fatalf("ProcessGroupAlive(%d) = true for a record started an hour after this process", pid)
	}

	// The genuine creation time still matches, or the fix would simply have made
	// the probe answer false to everything.
	if !ProcessGroupAlive(pid, started) {
		t.Fatalf("ProcessGroupAlive(%d) = false for the process the record describes", pid)
	}
}

// A positive pid has never been persisted without StartedAt. A missing identity
// therefore means the record is incomplete, not that any process with this pid
// is safe to kill.
func TestProcessGroupAliveFailsClosedWithoutAStartTime(t *testing.T) {
	_, pid, _ := startProbeHelper(t)

	if ProcessGroupAlive(pid, time.Time{}) {
		t.Fatalf("ProcessGroupAlive(%d) = true without a recorded start time", pid)
	}
}

// Proving the identity in one call and killing by number in the next would leave
// a window for the pid to change hands in between, which is the same mistake in
// a smaller form. The kill therefore re-checks the identity itself, under a
// handle it holds for the whole of taskkill, and refuses a record that does not
// describe the process rather than trusting whoever asked.
func TestTerminateProcessGroupByPIDRefusesAPidTheRecordDoesNotDescribe(t *testing.T) {
	cmd, pid, started := startProbeHelper(t)

	for name, identity := range map[string]time.Time{
		"a creation time from another process": started.Add(-time.Hour),
		"no recorded identity at all":          {},
	} {
		if err := TerminateProcessGroupByPID(pid, identity, time.Second); err == nil {
			t.Fatalf("TerminateProcessGroupByPID(%d) = nil for %s", pid, name)
		}
		if !ProcessGroupAlive(pid, started) {
			t.Fatalf("the process was killed on %s", name)
		}
	}
	runtime.KeepAlive(cmd)
}

// Identity is the safety boundary before background_reap runs taskkill /T /F.
// If the process creation time cannot be read, the pid must remain unproven and
// must not be offered for killing.
func TestProcessStartedAroundFailsClosedWhenCreationTimeCannotBeRead(t *testing.T) {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(os.Getpid()))
	if err != nil {
		t.Skipf("cannot open the current process with SYNCHRONIZE only: %v", err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	if processStartedAround(handle, time.Now()) {
		t.Fatal("processStartedAround() = true when GetProcessTimes cannot read the handle")
	}
}
