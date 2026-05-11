//go:build scheduler

package daemon

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
)

func TestJobRunnableForTickPaused(t *testing.T) {
	if jobRunnableForTick(&storage.JobFrontmatter{Paused: true}) {
		t.Fatal("paused job must not be runnable")
	}
	if !jobRunnableForTick(&storage.JobFrontmatter{Paused: false}) {
		t.Fatal("unpaused job must be runnable")
	}
	if jobRunnableForTick(nil) {
		t.Fatal("nil fm must not be runnable")
	}
}
