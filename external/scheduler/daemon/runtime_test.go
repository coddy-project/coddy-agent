//go:build scheduler

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// A cancel that lands while the job is reserved but its task is not registered
// yet is kept on the reservation rather than answered "not running": there is
// nothing to stop yet, and StartRun stops the task the moment it has one.
func TestCancelRunIsKeptOnAReservationWithoutATask(t *testing.T) {
	r := &Runtime{running: map[string]*runningEntry{}}
	roots := storage.Roots{Home: "/tmp/coddy-bdd-home", User: "/tmp/coddy-bdd-home/scheduler"}
	job := roots.UserRef("nightly")
	r.running[job.Key()] = &runningEntry{ref: schedservice.RunRef{JobID: "nightly", Trigger: schedservice.TriggerManual}}

	if !r.CancelRun(job) {
		t.Fatal("a reserved job must accept a cancel")
	}
	if !r.running[job.Key()].cancelRequested {
		t.Fatal("the cancel must be kept on the reservation")
	}
	if _, running := r.RunningRun(job); !running {
		t.Fatal("the reservation stays until the run settles")
	}
	if r.CancelRun(roots.ProjectRef("/tmp/ws", "nightly")) {
		t.Fatal("a project job of the same id is another job")
	}
	if r.CancelRun(roots.UserRef("other")) {
		t.Fatal("a job that is not reserved is not cancelled")
	}
}

// StartRun decides trust again on the snapshot it is handed: a receipt
// withdrawn after the caller looked stops the run before anything is
// reserved.
func TestStartRunRechecksTrustOnTheSnapshot(t *testing.T) {
	root := t.TempDir()
	home, ws := filepath.Join(root, "home"), filepath.Join(root, "ws")
	if err := os.MkdirAll(filepath.Join(ws, ".coddy", "scheduler"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: ws}, Scheduler: config.SchedulerConfig{Enabled: true}}
	ws = schedservice.CanonicalWorkspace(ws)
	ref := schedservice.RootsOf(cfg).ProjectRef(ws, "lint")
	body := []byte("---\nschedule: \"* * * * *\"\n---\nlint\n")
	if err := os.WriteFile(ref.Path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	store := storage.NewTrustStore(home)
	if err := store.Approve(ws, "lint", storage.Digest(body)); err != nil {
		t.Fatal(err)
	}
	snap, err := schedservice.RunnableSnapshot(cfg, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Revoke(ws, "lint"); err != nil {
		t.Fatal(err)
	}
	r := &Runtime{cfg: func() *config.Config { return cfg }, running: map[string]*runningEntry{}, slots: make(chan struct{}, 1)}
	if _, err := r.StartRun(context.Background(), schedservice.RunRequest{Snapshot: snap, Trigger: schedservice.TriggerManual}); !errors.Is(err, schedservice.ErrJobUntrusted) {
		t.Fatalf("start of a withdrawn job = %v, want ErrJobUntrusted", err)
	}
	if len(r.running) != 0 || len(r.slots) != 0 {
		t.Fatal("a refused run reserved something")
	}
}
