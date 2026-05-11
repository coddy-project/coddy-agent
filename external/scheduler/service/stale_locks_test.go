//go:build scheduler

package schedservice

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestStaleLockGraceFromConfig(t *testing.T) {
	c := &config.Config{Scheduler: config.SchedulerConfig{Timeout: "5m"}}
	g := StaleLockGraceFromConfig(c)
	want := 5*time.Minute + 20*time.Second
	if g < want {
		t.Fatalf("grace %v want at least %v", g, want)
	}
}

func TestCancelJobRunRemovesOrphanLock(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	schedDir := filepath.Join(root, "sched")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(schedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: root},
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: schedDir},
	}
	p := cfg.Paths
	cfg.Scheduler.Normalize(p)
	cfg.Scheduler.ApplyDefaults(p)
	if err := cfg.Scheduler.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	jobPath := filepath.Join(schedDir, "demo.md")
	body := "---\ndescription: x\nschedule: \"0 * * * *\"\n---\nbody\n"
	if err := os.WriteFile(jobPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(schedDir, "demo.lock")
	if err := os.WriteFile(lockPath, []byte("2020-01-01T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	svc := NewService(cfg, nil, root)
	ok, err := svc.CancelJobRun("demo")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("want cancelled or lock cleared")
	}
	if _, err := os.Stat(lockPath); err == nil {
		t.Fatal("lock should be removed")
	}
}

func TestListJobsClearsStaleLockAndRunningFalse(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	schedDir := filepath.Join(root, "sched")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(schedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: root},
		Scheduler: config.SchedulerConfig{Enabled: true, Dir: schedDir},
	}
	p := cfg.Paths
	cfg.Scheduler.Normalize(p)
	cfg.Scheduler.ApplyDefaults(p)
	if err := cfg.Scheduler.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	jobPath := filepath.Join(schedDir, "zjob.md")
	body := "---\ndescription: x\nschedule: \"0 * * * *\"\n---\nbody\n"
	if err := os.WriteFile(jobPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(schedDir, "zjob.lock")
	if err := os.WriteFile(lockPath, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	svc := NewService(cfg, nil, root)
	list, err := svc.ListJobs(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err == nil {
		t.Fatal("stale lock should be removed during list")
	}
	var z *SchedulerJob
	for i := range list.Jobs {
		if list.Jobs[i].JobID == "zjob" {
			z = &list.Jobs[i]
			break
		}
	}
	if z == nil {
		t.Fatal("missing zjob")
	}
	if z.Running {
		t.Fatal("running should be false without tracked run")
	}
}
