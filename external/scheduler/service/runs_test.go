//go:build scheduler

package schedservice

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// The fallback walk re-attaches a job session whose sidecar lost its pointer,
// and never a run bundle the old scheduler wrote: those were top-level sched_
// sessions marked with the job id exactly like a job session is.
func TestJobSessionIDForSkipsLegacyRunBundles(t *testing.T) {
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	schedDir := filepath.Join(root, "scheduler")
	if err := os.MkdirAll(schedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jobPath := filepath.Join(schedDir, "nightly.md")
	if err := os.WriteFile(jobPath, []byte("---\nschedule: \"0 3 * * *\"\n---\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A legacy run: sched_ id, schedulerRun with the job id.
	legacyDir, err := store.EnsureLayout("sched_0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	legacy := &session.State{ID: "sched_0123456789abcdef01234567", CWD: root, Mode: session.ModeAgent, SessionDir: legacyDir}
	legacy.SetSchedulerJobWithoutPersist("nightly")
	if err := store.Save(legacy); err != nil {
		t.Fatal(err)
	}
	if got := JobSessionIDFor(store, jobPath); got != "" {
		t.Fatalf("a legacy run bundle was taken for the job session: %q", got)
	}
	// A real job session is found by the job it names.
	jobSID := session.NewSessionID()
	dir, err := store.EnsureLayout(jobSID)
	if err != nil {
		t.Fatal(err)
	}
	js := &session.State{ID: jobSID, CWD: root, Mode: session.ModeAgent, SessionDir: dir}
	js.SetSchedulerJobWithoutPersist("nightly")
	if err := store.Save(js); err != nil {
		t.Fatal(err)
	}
	if got := JobSessionIDFor(store, jobPath); got != jobSID {
		t.Fatalf("job session = %q, want %q", got, jobSID)
	}
	// The sidecar pointer wins over the walk.
	if err := storage.WriteJobSessionID(storage.StatePath(jobPath), "sess_fedcba9876543210fedcba98"); err != nil {
		t.Fatal(err)
	}
	if got := JobSessionIDFor(store, jobPath); got != "sess_fedcba9876543210fedcba98" {
		t.Fatalf("pointer ignored: %q", got)
	}
}
