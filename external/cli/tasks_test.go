//go:build cli

package cli

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
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
	// listedFor names the session of every list read, in order; outputReads counts the
	// output reads. A gate, when set, holds the matching read until it is closed.
	listedFor   []string
	outputReads int
	listGate    chan struct{}
	outputGate  chan struct{}
}

func (b *tasksBackend) BackgroundTasks(_ context.Context, sessionID string) ([]bgtask.Snapshot, error) {
	b.mu.Lock()
	gate := b.listGate
	b.lists++
	b.listedFor = append(b.listedFor, sessionID)
	b.mu.Unlock()
	if gate != nil {
		<-gate
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.listErr != nil {
		return nil, b.listErr
	}
	return append([]bgtask.Snapshot(nil), b.rows...), nil
}

func (b *tasksBackend) BackgroundTaskOutput(_ context.Context, _ string, taskID string, _ int) (string, bgtask.Snapshot, error) {
	b.mu.Lock()
	gate := b.outputGate
	b.outputReads++
	b.mu.Unlock()
	if gate != nil {
		<-gate
	}
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

// A read that was in flight when the operator switched sessions answers for the
// session they left. The new session's tasks are read as soon as that answer frees the
// slot, not on the idle poll fifteen seconds later.
func TestASessionSwitchDuringAReadReadsTheNewSessionAtOnce(t *testing.T) {
	gate := make(chan struct{})
	b := &tasksBackend{listGate: gate, rows: []bgtask.Snapshot{taskRow("bg_1", bgtask.StatusRunning, time.Minute)}}
	a := newTasksApp(t, b)
	a.refreshTasks()

	a.sessionID = "sess_next"
	a.resetTasks()
	close(gate)
	b.mu.Lock()
	b.listGate = nil
	b.mu.Unlock()

	pumpUntil(t, a, "the new session's tasks", func() bool { return a.runningTasks == 1 })
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.listedFor) != 2 || b.listedFor[1] != "sess_next" {
		t.Fatalf("list reads went to %v, want the second one for the new session", b.listedFor)
	}
}

// openTaskInOverlay opens /tasks and the first task in it, and waits for its output.
func openTaskInOverlay(t *testing.T, a *App, want string) *tasksModal {
	t.Helper()
	if !a.dispatchSlash("/tasks") {
		t.Fatal("/tasks was not handled by the console")
	}
	modal := a.modal.(*tasksModal)
	pumpUntil(t, a, "the overlay to list the task", func() bool { return len(modal.rows) > 0 })
	modal.HandleInput([]byte("\r"))
	pumpUntil(t, a, "the output to arrive", func() bool {
		return strings.Contains(plainLines(modal.Render(100)), want)
	})
	return modal
}

// The task on screen ends between two polls. What it printed after the last read is
// read once more; a finished task is not polled after that.
func TestTheOpenTaskReadsWhatItPrintedLastWhenItEnds(t *testing.T) {
	b := &tasksBackend{
		rows:    []bgtask.Snapshot{taskRow("bg_1", bgtask.StatusRunning, time.Minute)},
		outputs: map[string]string{"bg_1": "step 1"},
	}
	a := newTasksApp(t, b)
	modal := openTaskInOverlay(t, a, "step 1")

	b.mu.Lock()
	now := time.Now()
	b.rows[0].Status, b.rows[0].FinishedAt = bgtask.StatusSucceeded, &now
	b.outputs["bg_1"] = "step 1\nall done"
	b.mu.Unlock()
	a.refreshTasks()
	pumpUntil(t, a, "the last lines of the finished task", func() bool {
		return strings.Contains(plainLines(modal.Render(100)), "all done")
	})

	b.mu.Lock()
	settled := b.outputReads
	b.mu.Unlock()
	a.refreshTasks()
	pumpUntil(t, a, "the next poll to settle", func() bool { return !a.tasksReading })
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.outputReads != settled {
		t.Fatalf("a finished task was read again: %d reads, then %d", settled, b.outputReads)
	}
}

// A slow server must not collect a queue of output reads behind the poll, the same as
// for the list.
func TestThePollDoesNotStackOutputReads(t *testing.T) {
	b := &tasksBackend{
		rows:    []bgtask.Snapshot{taskRow("bg_1", bgtask.StatusRunning, time.Minute)},
		outputs: map[string]string{"bg_1": "step 1"},
	}
	a := newTasksApp(t, b)
	openTaskInOverlay(t, a, "step 1")

	gate := make(chan struct{})
	b.mu.Lock()
	b.outputGate = gate
	before := b.outputReads
	b.mu.Unlock()
	for i := 0; i < 3; i++ {
		a.refreshTasks()
		pumpUntil(t, a, "the poll to settle", func() bool { return !a.tasksReading })
	}
	// Every read the polls started reaches the backend once the gate opens.
	close(gate)
	pumpUntil(t, a, "the output reads to be answered", func() bool { return a.taskOutputInflight == 0 })
	b.mu.Lock()
	asked := b.outputReads - before
	b.mu.Unlock()
	if asked != 1 {
		t.Fatalf("three polls behind a slow server asked for the output %d times, want 1", asked)
	}
}

// Two reads of one task overlap and the network answers them out of order: the older
// answer must not replace the newer one.
func TestALateOutputAnswerDoesNotReplaceANewerOne(t *testing.T) {
	b := &tasksBackend{
		rows:    []bgtask.Snapshot{taskRow("bg_1", bgtask.StatusRunning, time.Minute)},
		outputs: map[string]string{"bg_1": "step 1"},
	}
	a := newTasksApp(t, b)
	modal := openTaskInOverlay(t, a, "step 1")

	row := taskRow("bg_1", bgtask.StatusSucceeded, time.Minute)
	a.applyTaskOutputLoaded(taskOutputLoaded{sessionID: "sess_tasks", taskID: "bg_1", seq: a.taskOutputSeq + 2, output: "step 1\nall done", snap: row})
	a.applyTaskOutputLoaded(taskOutputLoaded{sessionID: "sess_tasks", taskID: "bg_1", seq: a.taskOutputSeq + 1, output: "step 1", snap: taskRow("bg_1", bgtask.StatusRunning, time.Minute)})
	if got := plainLines(modal.Render(100)); !strings.Contains(got, "all done") {
		t.Fatalf("the older answer replaced the newer one:\n%s", got)
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

// The overlay reads a task the way the web UI's card does: a tag that says what stands
// behind it, the work as the title, a meta line.

func agentRow(id, name, label string, system bool, status bgtask.Status) bgtask.Snapshot {
	row := taskRow(id, status, time.Minute)
	row.Kind, row.Command, row.Label = bgtask.KindAgent, "", label
	row.Agent = &bgtask.AgentInfo{Name: name, SessionID: "sess_child_" + id, System: system}
	return row
}

func TestTaskTagAndTitleMatchTheWebCards(t *testing.T) {
	cases := []struct {
		row   bgtask.Snapshot
		tag   string
		title string
	}{
		{taskRow("bg_1", bgtask.StatusRunning, time.Minute), "shell", "make bg_1"},
		{agentRow("bg_2", "general", "agent general: review the diff: handlers first", false, bgtask.StatusRunning), "general", "review the diff: handlers first"},
		{agentRow("bg_3", "memory", "memory: what did we decide", true, bgtask.StatusRunning), "memory", "what did we decide"},
		{agentRow("bg_4", "general", "agent general", false, bgtask.StatusRunning), "general", "Subagent run"},
		// A scheduled run is labelled by the scheduler, colon and all.
		{agentRow("bg_5", "general", "nightly: refresh the changelog", false, bgtask.StatusRunning), "general", "nightly: refresh the changelog"},
	}
	for _, c := range cases {
		if got := taskTag(c.row); got != c.tag {
			t.Errorf("taskTag(%s) = %q, want %q", c.row.ID, got, c.tag)
		}
		if got := taskTitle(c.row); got != c.title {
			t.Errorf("taskTitle(%s) = %q, want %q", c.row.ID, got, c.title)
		}
	}
}

func TestTaskMetaLine(t *testing.T) {
	now := time.Date(2026, 9, 18, 10, 1, 5, 0, time.UTC)
	running := bgtask.Snapshot{Status: bgtask.StatusRunning, StartedAt: now.Add(-65 * time.Second), ExpectedSeconds: 300}
	if got := taskMetaLine(running, now); got != "1m 05s · est. 5m 00s" {
		t.Errorf("running meta = %q", got)
	}
	overdue := bgtask.Snapshot{Status: bgtask.StatusRunning, StartedAt: now.Add(-400 * time.Second), ExpectedSeconds: 300}
	if got := taskMetaLine(overdue, now); got != "6m 40s · est. 5m 00s · overdue" {
		t.Errorf("overdue meta = %q", got)
	}
	ended := now.Add(-5 * time.Second)
	code := 2
	failed := bgtask.Snapshot{Kind: bgtask.KindCommand, Status: bgtask.StatusFailed, StartedAt: ended.Add(-90 * time.Second), FinishedAt: &ended, ExitCode: &code}
	if got := taskMetaLine(failed, now); got != "failed · 1m 30s · exit 2" {
		t.Errorf("failed meta = %q", got)
	}
	// No shell stands behind an agent run, so its synthetic exit code is not shown.
	zero := 0
	agent := bgtask.Snapshot{Kind: bgtask.KindAgent, Status: bgtask.StatusSucceeded, StartedAt: ended.Add(-200 * time.Second), FinishedAt: &ended, ExitCode: &zero}
	if got := taskMetaLine(agent, now); got != "succeeded · 3m 20s" {
		t.Errorf("agent meta = %q", got)
	}
}

func plainLines(lines []string) string {
	return tui.StripTerminalSequences(strings.Join(lines, "\n"))
}

func TestTasksModalListsTasksAndOpensOne(t *testing.T) {
	var opened, stopped []string
	closed := false
	m := newTasksModal(newTheme("dark"), func() {})
	m.OnOpen = func(id string) { opened = append(opened, id) }
	m.OnStop = func(id string) { stopped = append(stopped, id) }
	m.OnClose = func() { closed = true }
	m.SetRows([]bgtask.Snapshot{
		taskRow("bg_2", bgtask.StatusRunning, time.Minute),
		agentRow("bg_3", "explore", "agent explore: map the session package", false, bgtask.StatusRunning),
		taskRow("bg_1", bgtask.StatusSucceeded, time.Hour),
	})

	text := plainLines(m.Render(100))
	for _, want := range []string{"Background tasks", "2 running", "3 in total", "shell", "make bg_2", "explore", "map the session package", "succeeded"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the list is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "agent explore:") {
		t.Fatalf("the title repeats what the tag says:\n%s", text)
	}

	// s stops the selected running task; on a finished one it does nothing.
	m.HandleInput([]byte("s"))
	m.HandleInput([]byte("\x1b[B")) // down
	m.HandleInput([]byte("\x1b[B")) // down: the finished task
	m.HandleInput([]byte("s"))
	if len(stopped) != 1 || stopped[0] != "bg_2" {
		t.Fatalf("stopped = %v, want only the running task under the cursor", stopped)
	}

	// enter opens the task under the cursor in place; its output arrives later.
	m.HandleInput([]byte("\r"))
	if len(opened) != 1 || opened[0] != "bg_1" {
		t.Fatalf("opened = %v", opened)
	}
	m.SetOutput("bg_1", "built ok\nbuild/coddy 46 MB", false, false)
	detail := plainLines(m.Render(100))
	for _, want := range []string{"make bg_1", "$ make bg_1", "built ok", "build/coddy 46 MB", "esc back"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("the open task is missing %q:\n%s", want, detail)
		}
	}
	// Output for another task is not this view's.
	m.SetOutput("bg_2", "not mine", false, false)
	if strings.Contains(plainLines(m.Render(100)), "not mine") {
		t.Fatal("the open task shows another task's output")
	}

	// esc goes back to the list first, then closes the overlay.
	m.HandleInput([]byte("\x1b"))
	if closed || !strings.Contains(plainLines(m.Render(100)), "3 in total") {
		t.Fatal("esc on an open task did not return to the list")
	}
	m.HandleInput([]byte("\x1b"))
	if !closed {
		t.Fatal("esc on the list did not close the overlay")
	}
}

func TestTasksModalSaysSoWhenThereIsNothing(t *testing.T) {
	m := newTasksModal(newTheme("dark"), func() {})
	m.SetRows(nil)
	if text := plainLines(m.Render(80)); !strings.Contains(text, "No background tasks in this session yet") {
		t.Fatalf("empty overlay:\n%s", text)
	}
}

func TestSlashTasksOpensTheOverlayReadsOutputAndStops(t *testing.T) {
	b := &tasksBackend{
		rows:    []bgtask.Snapshot{taskRow("bg_1", bgtask.StatusRunning, time.Minute)},
		outputs: map[string]string{"bg_1": "=== RUN TestSuite\nok pkg/a"},
	}
	a := newTasksApp(t, b)

	if !a.dispatchSlash("/tasks") {
		t.Fatal("/tasks was not handled by the console")
	}
	modal, ok := a.modal.(*tasksModal)
	if !ok {
		t.Fatalf("modal = %T, want the tasks overlay", a.modal)
	}
	pumpUntil(t, a, "the overlay to list the task", func() bool {
		return strings.Contains(plainLines(modal.Render(100)), "make bg_1")
	})

	modal.HandleInput([]byte("\r"))
	pumpUntil(t, a, "the output to arrive", func() bool {
		return strings.Contains(plainLines(modal.Render(100)), "ok pkg/a")
	})

	modal.HandleInput([]byte("s"))
	pumpUntil(t, a, "the stop to reach the backend", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.stopped) == 1
	})
	pumpUntil(t, a, "the overlay to show the task stopped", func() bool {
		return strings.Contains(plainLines(modal.Render(100)), "stopped")
	})

	modal.HandleInput([]byte("\x1b"))
	modal.HandleInput([]byte("\x1b"))
	if a.modal != nil {
		t.Fatal("the overlay stayed open after esc")
	}
}
