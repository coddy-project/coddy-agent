//go:build cli

package cli

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func testTasksConfig(t *testing.T) *config.Config {
	t.Helper()
	home := t.TempDir()
	return &config.Config{
		Paths:  config.Paths{Home: home, CWD: home},
		Models: []config.ModelEntry{{Model: "stub/model", MaxTokens: 1000, MaxContextTokens: 100000}},
		Agent:  config.Agent{Model: "stub/model"},
	}
}

// tasksBackend is a backend whose only working part is the three task methods; the
// rest of the interface comes from the embedded manager the unit tests never call.
type tasksBackend struct {
	backend
	mu      sync.Mutex
	rows    []bgtask.Snapshot
	outputs map[string]string
	listErr error
	lists   int
	stopped []string
}

func (b *tasksBackend) BackgroundTasks(context.Context, string) ([]bgtask.Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lists++
	if b.listErr != nil {
		return nil, b.listErr
	}
	return append([]bgtask.Snapshot(nil), b.rows...), nil
}

func (b *tasksBackend) BackgroundTaskOutput(_ context.Context, _ string, taskID string, _ int) (string, bgtask.Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, row := range b.rows {
		if row.ID == taskID {
			return b.outputs[taskID], row, nil
		}
	}
	return "", bgtask.Snapshot{}, bgtask.ErrNotFound
}

func (b *tasksBackend) StopBackgroundTask(_ context.Context, _ string, taskID string) (bgtask.Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, row := range b.rows {
		if row.ID == taskID {
			now := time.Now()
			b.rows[i].Status, b.rows[i].FinishedAt = bgtask.StatusStopped, &now
			b.stopped = append(b.stopped, taskID)
			return b.rows[i], nil
		}
	}
	return bgtask.Snapshot{}, bgtask.ErrNotFound
}

func taskRow(id string, status bgtask.Status, startedAgo time.Duration) bgtask.Snapshot {
	return bgtask.Snapshot{ID: id, SessionID: "sess_tasks", Kind: bgtask.KindCommand, Label: "make " + id,
		Command: "make " + id, Status: status, StartedAt: time.Now().Add(-startedAgo)}
}

// pumpUntil applies queued loop messages until cond holds.
func pumpUntil(t *testing.T, a *App, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for !cond() {
		select {
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func newTasksApp(t *testing.T, b *tasksBackend) *App {
	t.Helper()
	a := newApp(testTasksConfig(t), b, discardLogger(), &bddTerminal{cols: 100, rows: 30}, "dark", true)
	a.sessionID = "sess_tasks"
	// The poll timer is driven by hand: a test must not wait 2.5 s for a read.
	a.usageAfterFn = func(time.Duration, func()) func() bool { return func() bool { return true } }
	t.Cleanup(a.Close)
	return a
}

func TestRunningTasksAreCountedWithoutTheMemoryRun(t *testing.T) {
	memory := taskRow("bg_3", bgtask.StatusRunning, time.Second)
	memory.Kind, memory.Agent = bgtask.KindAgent, &bgtask.AgentInfo{Name: "memory", System: true}
	b := &tasksBackend{rows: []bgtask.Snapshot{
		taskRow("bg_1", bgtask.StatusSucceeded, time.Hour),
		taskRow("bg_2", bgtask.StatusRunning, time.Minute),
		memory,
	}}
	a := newTasksApp(t, b)

	a.refreshTasks()
	pumpUntil(t, a, "the tasks to load", func() bool { return len(a.tasks) == 3 })
	if a.runningTasks != 1 {
		t.Fatalf("runningTasks = %d, want the one task the model started", a.runningTasks)
	}
	// Newest first, the order the web UI's panel uses.
	if a.tasks[0].ID != "bg_3" || a.tasks[2].ID != "bg_1" {
		t.Fatalf("order = %s, %s, %s", a.tasks[0].ID, a.tasks[1].ID, a.tasks[2].ID)
	}
}

func TestAnUnreadableTaskListKeepsTheLastCount(t *testing.T) {
	b := &tasksBackend{rows: []bgtask.Snapshot{taskRow("bg_1", bgtask.StatusRunning, time.Minute)}}
	a := newTasksApp(t, b)
	a.refreshTasks()
	pumpUntil(t, a, "the first read", func() bool { return a.runningTasks == 1 })

	b.mu.Lock()
	b.listErr = context.DeadlineExceeded
	b.mu.Unlock()
	a.refreshTasks()
	pumpUntil(t, a, "the failed read to settle", func() bool { return !a.tasksReading })
	if a.runningTasks != 1 || len(a.tasks) != 1 {
		t.Fatalf("an unreachable server read as no tasks: %d running, %d rows", a.runningTasks, len(a.tasks))
	}
}

func TestOneTaskReadIsInFlightAtATime(t *testing.T) {
	b := &tasksBackend{}
	a := newTasksApp(t, b)
	a.refreshTasks()
	a.refreshTasks()
	a.refreshTasks()
	pumpUntil(t, a, "the read to settle", func() bool { return !a.tasksReading })
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lists != 1 {
		t.Fatalf("backend was asked %d times for one refresh", b.lists)
	}
}

func TestThePollRunsFastWhileSomethingIsGoingOn(t *testing.T) {
	a := &App{}
	if got := a.tasksPollInterval(); got != tasksPollIdle {
		t.Fatalf("idle interval = %v", got)
	}
	a.runningTasks = 1
	if got := a.tasksPollInterval(); got != tasksPollActive {
		t.Fatalf("interval with a running task = %v", got)
	}
	a.runningTasks, a.turnActive = 0, true
	if got := a.tasksPollInterval(); got != tasksPollActive {
		t.Fatalf("interval during a turn = %v", got)
	}
}

func TestOnlyToolsThatStartOrEndTasksTriggerARead(t *testing.T) {
	for _, name := range []string{"run_command", "spawn_agent", "background_stop", "background_wait"} {
		if !toolTouchesTasks(name) {
			t.Errorf("%s can change the session's tasks", name)
		}
	}
	for _, name := range []string{"read", "grep", "edit", ""} {
		if toolTouchesTasks(name) {
			t.Errorf("%s cannot change the session's tasks", name)
		}
	}
}
