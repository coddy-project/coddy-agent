package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// recordingRunner captures the turns a waker starts.
type recordingRunner struct {
	mu           sync.Mutex
	instructions []string
	sessions     []string
	block        chan struct{}
}

func (r *recordingRunner) run(_ context.Context, sessionID, instruction string) error {
	if r.block != nil {
		<-r.block
	}
	r.mu.Lock()
	r.sessions = append(r.sessions, sessionID)
	r.instructions = append(r.instructions, instruction)
	r.mu.Unlock()
	return nil
}

func (r *recordingRunner) calls() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sessions...), append([]string(nil), r.instructions...)
}

func waitForCalls(t *testing.T, r *recordingRunner, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sessions, instructions := r.calls(); len(sessions) >= want {
			return instructions
		}
		time.Sleep(10 * time.Millisecond)
	}
	sessions, _ := r.calls()
	t.Fatalf("waker started %d turns, want %d", len(sessions), want)
	return nil
}

func finished(id, sessionID string, status bgtask.Status, notify bool) bgtask.Snapshot {
	end := time.Now()
	code := 0
	return bgtask.Snapshot{
		ID:             id,
		SessionID:      sessionID,
		Kind:           bgtask.KindCommand,
		Label:          "make test",
		Status:         status,
		StartedAt:      end.Add(-30 * time.Second),
		FinishedAt:     &end,
		ExitCode:       &code,
		NotifyOnFinish: notify,
	}
}

func TestWakerOnlyRunsForTasksThatAskedForIt(t *testing.T) {
	runner := &recordingRunner{}
	w := NewBackgroundWaker(slog.Default(), runner.run)

	// Neither of these should wake anything: one did not opt in, the other is
	// still running.
	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusSucceeded, false))
	running := finished("bg_2", "s1", bgtask.StatusRunning, true)
	running.FinishedAt = nil
	w.OnSnapshot(running)

	time.Sleep(wakeSettleDelay + 300*time.Millisecond)
	if sessions, _ := runner.calls(); len(sessions) != 0 {
		t.Fatalf("waker started %d turns, want none", len(sessions))
	}

	w.OnSnapshot(finished("bg_3", "s1", bgtask.StatusSucceeded, true))
	instructions := waitForCalls(t, runner, 1)
	if !strings.Contains(instructions[0], "bg_3") {
		t.Fatalf("instruction %q does not name the finished task", instructions[0])
	}
}

func TestWakerBatchesABurstIntoOneTurn(t *testing.T) {
	runner := &recordingRunner{}
	w := NewBackgroundWaker(slog.Default(), runner.run)

	// Three tasks landing together must cost one turn, not three.
	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusSucceeded, true))
	w.OnSnapshot(finished("bg_2", "s1", bgtask.StatusFailed, true))
	w.OnSnapshot(finished("bg_3", "s1", bgtask.StatusTimedOut, true))

	instructions := waitForCalls(t, runner, 1)
	time.Sleep(wakeSettleDelay + 300*time.Millisecond)

	sessions, _ := runner.calls()
	if len(sessions) != 1 {
		t.Fatalf("waker started %d turns for one burst, want 1", len(sessions))
	}
	for _, id := range []string{"bg_1", "bg_2", "bg_3"} {
		if !strings.Contains(instructions[0], id) {
			t.Fatalf("instruction %q is missing %s", instructions[0], id)
		}
	}
}

func TestWakerKeepsSessionsApart(t *testing.T) {
	runner := &recordingRunner{}
	w := NewBackgroundWaker(slog.Default(), runner.run)

	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusSucceeded, true))
	w.OnSnapshot(finished("bg_2", "s2", bgtask.StatusSucceeded, true))

	waitForCalls(t, runner, 2)
	sessions, _ := runner.calls()
	seen := map[string]bool{}
	for _, s := range sessions {
		seen[s] = true
	}
	if !seen["s1"] || !seen["s2"] {
		t.Fatalf("sessions woken = %v, want both s1 and s2", sessions)
	}
}

func TestWakerStopsAfterTheCap(t *testing.T) {
	runner := &recordingRunner{}
	w := NewBackgroundWaker(slog.Default(), runner.run)

	w.mu.Lock()
	w.wakes["s1"] = maxWakesPerSession
	w.mu.Unlock()

	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusSucceeded, true))
	time.Sleep(wakeSettleDelay + 300*time.Millisecond)

	if sessions, _ := runner.calls(); len(sessions) != 0 {
		t.Fatalf("waker started %d turns past the cap, want none", len(sessions))
	}
}

func TestWakerDoesNotWakeWhileTheProcessIsShuttingDown(t *testing.T) {
	runner := &recordingRunner{}
	w := NewBackgroundWaker(slog.Default(), runner.run)

	pool := bgtask.NewWithRunner(bgtask.Config{}, nil)
	w.Attach(pool)
	pool.SetDraining(true)

	// Drain stops running tasks, and a stop is terminal: without the guard every
	// task killed by shutdown would start a turn nobody will read.
	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusStopped, true))
	time.Sleep(wakeSettleDelay + 300*time.Millisecond)

	if sessions, _ := runner.calls(); len(sessions) != 0 {
		t.Fatalf("waker started %d turns during drain, want none", len(sessions))
	}
}

func TestAttachReplacesAPreviousWakerInsteadOfStacking(t *testing.T) {
	pool := bgtask.NewWithRunner(bgtask.Config{}, nil)

	first := &recordingRunner{}
	NewBackgroundWaker(slog.Default(), first.run).Attach(pool)

	second := &recordingRunner{}
	NewBackgroundWaker(slog.Default(), second.run).Attach(pool)

	// A process that rebuilds its server (every test scenario does) must not end
	// up waking the model once per stacked subscription.
	pool.SubscribeKeyed(BackgroundWakerKey, nil)
	third := &recordingRunner{}
	w := NewBackgroundWaker(slog.Default(), third.run)
	w.Attach(pool)

	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusSucceeded, true))
	waitForCalls(t, third, 1)

	if sessions, _ := first.calls(); len(sessions) != 0 {
		t.Fatalf("the replaced waker still ran %d turns", len(sessions))
	}
	if sessions, _ := second.calls(); len(sessions) != 0 {
		t.Fatalf("the replaced waker still ran %d turns", len(sessions))
	}
}

func TestWakeInstructionReportsFailureHonestly(t *testing.T) {
	code := 2
	end := time.Now()
	batch := []bgtask.Snapshot{{
		ID:         "bg_9",
		Label:      "make lint",
		Status:     bgtask.StatusFailed,
		StartedAt:  end.Add(-90 * time.Second),
		FinishedAt: &end,
		ExitCode:   &code,
		Error:      "exit status 2",
	}}

	got := WakeInstruction(batch)
	for _, want := range []string{"bg_9", "failed", "make lint", "exit 2", "exit status 2", "background_output"} {
		if !strings.Contains(got, want) {
			t.Fatalf("instruction %q is missing %q", got, want)
		}
	}
	if !strings.Contains(got, "did not succeed") {
		t.Fatalf("instruction %q does not tell the model to report failure honestly", got)
	}
}

func TestWakeInstructionCountsABatch(t *testing.T) {
	batch := []bgtask.Snapshot{
		finished("bg_1", "s1", bgtask.StatusSucceeded, true),
		finished("bg_2", "s1", bgtask.StatusSucceeded, true),
	}
	if got := WakeInstruction(batch); !strings.HasPrefix(got, "2 background tasks") {
		t.Fatalf("instruction %q does not open with the batch count", got)
	}
	if got := WakeInstruction(batch[:1]); !strings.HasPrefix(got, "A background task") {
		t.Fatalf("instruction %q does not open in the singular", got)
	}
}
