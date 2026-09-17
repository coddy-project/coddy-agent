//go:build scheduler

package daemon

import (
	"testing"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
)

// A cancel that lands while the job is reserved but its task is not registered
// yet is kept on the reservation rather than answered "not running": there is
// nothing to stop yet, and StartRun stops the task the moment it has one.
func TestCancelRunIsKeptOnAReservationWithoutATask(t *testing.T) {
	r := &Runtime{running: map[string]*runningEntry{}}
	abs := canonicalJobPath("/tmp/coddy-bdd-jobs/nightly.md")
	r.running[abs] = &runningEntry{ref: schedservice.RunRef{JobID: "nightly", Trigger: schedservice.TriggerManual}}

	if !r.CancelRun(abs) {
		t.Fatal("a reserved job must accept a cancel")
	}
	if !r.running[abs].cancelRequested {
		t.Fatal("the cancel must be kept on the reservation")
	}
	if _, running := r.RunningRun(abs); !running {
		t.Fatal("the reservation stays until the run settles")
	}
	if r.CancelRun("/tmp/coddy-bdd-jobs/other.md") {
		t.Fatal("a job that is not reserved is not cancelled")
	}
}
