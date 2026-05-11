//go:build scheduler

package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TestCronCheckpointBeforeSkillsLoadGate ensures .state is updated before skills.LoadAll runs.
// If the checkpoint were written only after LoadAll, this test would deadlock until timeout.
func TestCronCheckpointBeforeSkillsLoadGate(t *testing.T) {
	tmp := t.TempDir()
	sessionsDir := filepath.Join(tmp, "sessions")
	home := filepath.Join(tmp, "home")
	jobPath := filepath.Join(tmp, "demo.md")
	content := "---\nschedule: \"* * * * *\"\n---\nhello\n"
	if err := os.WriteFile(jobPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	jobAbs, err := filepath.Abs(jobPath)
	if err != nil {
		t.Fatal(err)
	}
	stPath := storage.StatePath(storage.CanonicalSchedulerJobPath(jobAbs))

	cfg := &config.Config{
		Sessions: config.Sessions{Dir: sessionsDir},
		Paths:    config.Paths{Home: home},
		Skills:   config.Skills{},
		Scheduler: config.SchedulerConfig{
			Timeout: "30s",
		},
	}

	checkpointSeen := make(chan struct{})
	go func() {
		deadline := time.After(4 * time.Second)
		for {
			select {
			case <-deadline:
				return
			default:
			}
			last, err := storage.ReadJobState(stPath)
			if err == nil && !last.IsZero() {
				close(checkpointSeen)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	schedulerSkillsLoadGateForTest = func() {
		select {
		case <-checkpointSeen:
		case <-time.After(3 * time.Second):
			t.Fatal("checkpoint not visible before skills LoadAll (would deadlock if checkpoint followed LoadAll)")
		}
	}
	defer func() { schedulerSkillsLoadGateForTest = nil }()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fm := &storage.JobFrontmatter{Schedule: "* * * * *"}
	fireSlot := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)

	_ = RunJobFile(ctx, cfg, log, tmp, jobAbs, fireSlot, true, fm, "ping")
}
