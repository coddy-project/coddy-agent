//go:build scheduler

package schedservice

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestPatchJobRenameJobID(t *testing.T) {
	root := t.TempDir()
	schedDir := filepath.Join(root, "scheduler")
	if err := os.MkdirAll(schedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: schedDir},
	}
	cfg.Scheduler.Normalize(config.Paths{CWD: root})
	cfg.Scheduler.ApplyDefaults(config.Paths{CWD: root})

	svc := NewService(cfg, nil, root)
	if err := svc.CreateJob(SchedulerJobCreate{
		JobID:       "old-name",
		Description: "Before rename",
		Schedule:    "0 * * * *",
		Body:        "tick",
	}); err != nil {
		t.Fatal(err)
	}
	state := []byte(`{"last_scheduled_utc":"2026-05-01T12:00:00Z"}`)
	oldAbs, _ := svc.jobAbsPath("old-name")
	if err := os.WriteFile(storage.StatePath(oldAbs), state, 0o644); err != nil {
		t.Fatal(err)
	}

	newID := "new-name"
	if err := svc.PatchJob("old-name", SchedulerJobPatch{JobID: &newID}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldAbs); !os.IsNotExist(err) {
		t.Fatalf("old .md should be gone: %v", err)
	}
	newAbs, err := svc.jobAbsPath("new-name")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newAbs); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(storage.StatePath(newAbs)); err != nil {
		t.Fatalf("state sidecar should move with rename: %v", err)
	}
	got, err := svc.GetJob("new-name")
	if err != nil {
		t.Fatal(err)
	}
	if got.JobID != "new-name" || got.Description != "Before rename" {
		t.Fatalf("got %+v", got)
	}
}

func TestPatchJobRenameJobIDConflict(t *testing.T) {
	root := t.TempDir()
	schedDir := filepath.Join(root, "scheduler")
	if err := os.MkdirAll(schedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: schedDir},
	}
	cfg.Scheduler.Normalize(config.Paths{CWD: root})
	cfg.Scheduler.ApplyDefaults(config.Paths{CWD: root})
	svc := NewService(cfg, nil, root)
	for _, id := range []string{"taken", "mover"} {
		if err := svc.CreateJob(SchedulerJobCreate{
			JobID: id, Description: "x", Schedule: "0 * * * *", Body: "y",
		}); err != nil {
			t.Fatal(err)
		}
	}
	target := "taken"
	if err := svc.PatchJob("mover", SchedulerJobPatch{JobID: &target}); err != ErrJobExists {
		t.Fatalf("want ErrJobExists, got %v", err)
	}
}

// runningStubRuntime is a Runtime whose only fact is that one job is running.
type runningStubRuntime struct {
	Runtime
	running string
}

func (r *runningStubRuntime) RunningRun(jobPath string) (RunRef, bool) {
	if jobPath == r.running {
		return RunRef{JobID: "running"}, true
	}
	return RunRef{}, false
}

func (r *runningStubRuntime) Pool() *bgtask.Pool { return bgtask.Default() }

func TestDeleteAndRenameAreBlockedWhileTheJobRuns(t *testing.T) {
	root := t.TempDir()
	schedDir := filepath.Join(root, "scheduler")
	if err := os.MkdirAll(schedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: schedDir},
	}
	cfg.Scheduler.Normalize(config.Paths{CWD: root})
	cfg.Scheduler.ApplyDefaults(config.Paths{CWD: root})
	svc := NewService(cfg, nil, root)
	if err := svc.CreateJob(SchedulerJobCreate{
		JobID: "busy", Description: "x", Schedule: "0 * * * *", Body: "y",
	}); err != nil {
		t.Fatal(err)
	}
	abs, err := svc.jobAbsPath("busy")
	if err != nil {
		t.Fatal(err)
	}
	SetRuntime(&runningStubRuntime{running: abs})
	defer SetRuntime(nil)
	if err := svc.DeleteJob("busy"); err != ErrJobBusy {
		t.Fatalf("delete: want ErrJobBusy, got %v", err)
	}
	newID := "renamed"
	if err := svc.PatchJob("busy", SchedulerJobPatch{JobID: &newID}); err != ErrJobBusy {
		t.Fatalf("rename: want ErrJobBusy, got %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("the running job's file must still be there: %v", err)
	}
}

// The frontmatter fields a run is made from are validated on the way in, so a
// job file never carries a permission mode or an agent name the daemon would
// have to refuse at fire time.
func TestCreateJobValidatesAgentAndPermissionMode(t *testing.T) {
	root := t.TempDir()
	schedDir := filepath.Join(root, "scheduler")
	if err := os.MkdirAll(schedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: schedDir},
	}
	cfg.Scheduler.Normalize(config.Paths{CWD: root})
	cfg.Scheduler.ApplyDefaults(config.Paths{CWD: root})
	svc := NewService(cfg, nil, root)
	if err := svc.CreateJob(SchedulerJobCreate{
		JobID: "a", Description: "x", Schedule: "0 * * * *", Body: "y", PermissionMode: "sometimes",
	}); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("permission_mode: want ErrInvalidJob, got %v", err)
	}
	if err := svc.CreateJob(SchedulerJobCreate{
		JobID: "a", Description: "x", Schedule: "0 * * * *", Body: "y", Agent: "Not A Name",
	}); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("agent: want ErrInvalidJob, got %v", err)
	}
	if err := svc.CreateJob(SchedulerJobCreate{
		JobID: "a", Description: "x", Schedule: "0 * * * *", Body: "y", Agent: "reviewer", PermissionMode: "accept_edits",
	}); err != nil {
		t.Fatalf("valid fields refused: %v", err)
	}
	job, err := svc.GetJob("a")
	if err != nil {
		t.Fatal(err)
	}
	if job.Agent != "reviewer" || job.PermissionMode != "accept_edits" {
		t.Fatalf("fields lost on the way through the file: %+v", job)
	}
	abs, _ := svc.jobAbsPath("a")
	fm, _, err := storage.ParseJobFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Agent != "reviewer" || fm.PermissionMode != "accept_edits" {
		t.Fatalf("frontmatter = %+v", fm)
	}
}

func TestPatchJobRenameRejectsInvalidID(t *testing.T) {
	root := t.TempDir()
	schedDir := filepath.Join(root, "scheduler")
	if err := os.MkdirAll(schedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: schedDir},
	}
	cfg.Scheduler.Normalize(config.Paths{CWD: root})
	cfg.Scheduler.ApplyDefaults(config.Paths{CWD: root})
	svc := NewService(cfg, nil, root)
	if err := svc.CreateJob(SchedulerJobCreate{
		JobID: "ok", Description: "x", Schedule: "0 * * * *", Body: "y",
	}); err != nil {
		t.Fatal(err)
	}
	bad := "bad/id"
	renameErr := svc.PatchJob("ok", SchedulerJobPatch{JobID: &bad})
	if renameErr == nil {
		t.Fatal("want invalid job_id error")
	}
	if renameErr != ErrInvalidJobID && !strings.Contains(renameErr.Error(), "invalid") {
		t.Fatalf("got %v", renameErr)
	}
}
