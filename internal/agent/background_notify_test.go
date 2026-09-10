package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
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

// busyRunner refuses turns with the session manager's own busy error until it
// is released, then records what it is finally handed.
type busyRunner struct {
	mu       sync.Mutex
	busy     bool
	attempts int
	turns    []string
	fail     error
}

func (r *busyRunner) run(_ context.Context, _, instruction string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts++
	if r.fail != nil {
		return r.fail
	}
	if r.busy {
		return fmt.Errorf("background wake: %w", session.ErrSessionTurnBusy)
	}
	r.turns = append(r.turns, instruction)
	return nil
}

func (r *busyRunner) release() {
	r.mu.Lock()
	r.busy = false
	r.mu.Unlock()
}

func (r *busyRunner) state() (int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempts, append([]string(nil), r.turns...)
}

func fastRetryWaker(run RunTurnFunc) *BackgroundWaker {
	w := NewBackgroundWaker(slog.Default(), run)
	w.busyRetryFirst = 10 * time.Millisecond
	w.busyRetryMax = 20 * time.Millisecond
	return w
}

// A task that dies while the turn that started it is still running is the
// common case, not an exotic one: the wake has to survive a busy session
// instead of being dropped on the floor.
func TestWakeSurvivesABusySessionAndArrivesWhenTheTurnEnds(t *testing.T) {
	runner := &busyRunner{busy: true}
	w := fastRetryWaker(runner.run)

	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusFailed, true))

	// Wait until the busy session has actually refused at least once.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if attempts, _ := runner.state(); attempts > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if attempts, turns := runner.state(); attempts == 0 || len(turns) != 0 {
		t.Fatalf("attempts=%d turns=%d, want a refused attempt and no turn yet", attempts, len(turns))
	}

	runner.release()

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, turns := runner.state(); len(turns) == 1 {
			if !strings.Contains(turns[0], "bg_1") || !strings.Contains(turns[0], "failed") {
				t.Fatalf("woken turn %q lost the outcome across the retry", turns[0])
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, turns := runner.state()
	t.Fatalf("the wake never arrived after the turn ended, %d turns recorded", len(turns))
}

// Retrying a busy session must not spend the per-session wake budget, or a
// single long turn would exhaust it before the first turn ever runs.
func TestBusyRetriesDoNotSpendTheWakeBudget(t *testing.T) {
	runner := &busyRunner{busy: true}
	w := fastRetryWaker(runner.run)

	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusFailed, true))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if attempts, _ := runner.state(); attempts >= 5 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	attempts, _ := runner.state()
	if attempts < 5 {
		t.Fatalf("the waker retried %d times, want it to keep trying while the session is busy", attempts)
	}

	w.mu.Lock()
	spent := w.wakes["s1"]
	w.mu.Unlock()
	if spent > 1 {
		t.Fatalf("%d wakes charged for one pending batch, want at most 1", spent)
	}

	runner.release()
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, turns := runner.state(); len(turns) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the wake never arrived after the retries")
}

// A turn that fails for its own reasons is not a lock contention: retrying it
// forever would spin, so the batch is dropped exactly as before.
func TestWakeDropsABatchOnANonBusyFailure(t *testing.T) {
	runner := &busyRunner{fail: errors.New("session not found: s1")}
	w := fastRetryWaker(runner.run)

	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusFailed, true))
	time.Sleep(wakeSettleDelay + 500*time.Millisecond)

	attempts, turns := runner.state()
	if attempts != 1 {
		t.Fatalf("the waker made %d attempts on a permanent failure, want 1", attempts)
	}
	if len(turns) != 0 {
		t.Fatalf("recorded %d turns for a failing runner", len(turns))
	}
}

// A session whose turn never ends must not keep a retry goroutine alive for the
// life of the process.
func TestWakeGivesUpOnASessionThatStaysBusy(t *testing.T) {
	runner := &busyRunner{busy: true}
	w := fastRetryWaker(runner.run)
	w.busyGiveUpAfter = 150 * time.Millisecond

	w.OnSnapshot(finished("bg_1", "s1", bgtask.StatusFailed, true))
	time.Sleep(wakeSettleDelay + 1500*time.Millisecond)

	before, _ := runner.state()
	time.Sleep(500 * time.Millisecond)
	after, _ := runner.state()
	if after != before {
		t.Fatalf("the waker was still retrying past its give-up window (%d -> %d attempts)", before, after)
	}
	if before == 0 {
		t.Fatal("the waker never attempted a turn at all")
	}
}
