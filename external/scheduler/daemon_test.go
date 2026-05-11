//go:build scheduler

package scheduler

import (
	"testing"

	sched "github.com/EvilFreelancer/coddy-agent/external/scheduler/lib"
)

func TestJobRunnableForTickPaused(t *testing.T) {
	if jobRunnableForTick(&sched.JobFrontmatter{Paused: true}) {
		t.Fatal("paused job must not be runnable")
	}
	if !jobRunnableForTick(&sched.JobFrontmatter{Paused: false}) {
		t.Fatal("unpaused job must be runnable")
	}
	if jobRunnableForTick(nil) {
		t.Fatal("nil fm must not be runnable")
	}
}
