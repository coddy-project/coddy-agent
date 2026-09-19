package session

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// The manager reads the process-wide pool, so these tests start a real, short shell
// command under a session id of their own and clean up after themselves.
func TestManagerReadsAndStopsTheBackgroundTasksOfASession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command below is a POSIX shell line")
	}
	m, _ := newTestManager(t)
	sessionID := "sess_bgtasks_" + strings.ReplaceAll(t.Name(), "/", "_")
	pool := bgtask.Default()
	t.Cleanup(func() {
		pool.StopSession(sessionID)
		pool.ReleaseSession(sessionID)
	})

	snap, err := pool.Start(bgtask.Spec{SessionID: sessionID, Command: "echo started; sleep 30", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}

	ctx := context.Background()
	rows, err := m.BackgroundTasks(ctx, sessionID)
	if err != nil || len(rows) != 1 || rows[0].ID != snap.ID || rows[0].Status.Finished() {
		t.Fatalf("BackgroundTasks() = %+v, %v; want the one running task", rows, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var output string
	for time.Now().Before(deadline) {
		output, _, err = m.BackgroundTaskOutput(ctx, sessionID, snap.ID, 0)
		if err != nil {
			t.Fatalf("BackgroundTaskOutput(): %v", err)
		}
		if strings.Contains(output, "started") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(output, "started") {
		t.Fatalf("output = %q, want what the task printed", output)
	}

	stopped, err := m.StopBackgroundTask(ctx, sessionID, snap.ID)
	if err != nil || stopped.Status != bgtask.StatusStopped {
		t.Fatalf("StopBackgroundTask() = %+v, %v; want a stopped task", stopped, err)
	}
	if _, err := m.StopBackgroundTask(ctx, sessionID, "bg_missing"); !errors.Is(err, bgtask.ErrNotFound) {
		t.Fatalf("stopping an unknown task = %v, want ErrNotFound", err)
	}
	if _, _, err := m.BackgroundTaskOutput(ctx, sessionID, "bg_missing", 0); !errors.Is(err, bgtask.ErrNotFound) {
		t.Fatalf("reading an unknown task = %v, want ErrNotFound", err)
	}
}
