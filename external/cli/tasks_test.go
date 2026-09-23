//go:build cli

package cli

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
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
		// A preview server is named by the address the operator opens.
		{bgtask.Snapshot{ID: "bg_6", Kind: bgtask.KindServer, Label: "preview demo", URL: "http://127.0.0.1:4321/", Status: bgtask.StatusRunning}, "server", "http://127.0.0.1:4321/"},
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
	// A row of the list leaves how the task ended to its mark (✓, ✗, ■) and the exit
	// code to the open task, the way a folded card of the web UI does.
	failed := bgtask.Snapshot{Kind: bgtask.KindCommand, Status: bgtask.StatusFailed, StartedAt: ended.Add(-90 * time.Second), FinishedAt: &ended, ExitCode: &code}
	if got := taskMetaLine(failed, now); got != "1m 30s" {
		t.Errorf("failed meta = %q", got)
	}
	// An agent run names the model it runs on and the tokens its calls spent.
	zero := 0
	agent := bgtask.Snapshot{Kind: bgtask.KindAgent, Status: bgtask.StatusSucceeded, StartedAt: ended.Add(-200 * time.Second), FinishedAt: &ended, ExitCode: &zero,
		Agent: &bgtask.AgentInfo{Name: "general", Model: "neuraldeep/qwen3.8-27b", InputTokens: 198_000, OutputTokens: 14_345}}
	if got := taskMetaLine(agent, now); got != "3m 20s · qwen3.8-27b · 212.3k tokens" {
		t.Errorf("agent meta = %q", got)
	}
	live := bgtask.Snapshot{Kind: bgtask.KindAgent, Status: bgtask.StatusRunning, StartedAt: now.Add(-44 * time.Second),
		Agent: &bgtask.AgentInfo{Name: "explore", Model: "rpa/qwen3.6-35b-a3b"}}
	if got := taskMetaLine(live, now); got != "44s · qwen3.6-35b-a3b" {
		t.Errorf("an agent that has not reported yet: meta = %q", got)
	}
	// A running task that will start a turn when it ends says so, and a task
	// whose end started one says that. A task that was to wake the agent and
	// has not - the turn has not begun yet - says nothing of it once it ended.
	waking := bgtask.Snapshot{Kind: bgtask.KindCommand, Status: bgtask.StatusRunning, StartedAt: now.Add(-65 * time.Second), NotifyOnFinish: true}
	if got := taskMetaLine(waking, now); got != "1m 05s · wakes the agent" {
		t.Errorf("waking meta = %q", got)
	}
	unwoken := failed
	unwoken.NotifyOnFinish = true
	if got := taskMetaLine(unwoken, now); got != "1m 30s" {
		t.Errorf("a finished task that has not woken the agent yet: meta = %q", got)
	}
	woke := unwoken
	woke.WokeAgent = true
	if got := taskMetaLine(woke, now); got != "1m 30s · woke the agent" {
		t.Errorf("a finished task that woke the agent: meta = %q", got)
	}
}

// A wake that lands while the console is busy, or for a session it is not
// showing, is handed back to the waker as busy - it asks again - and a wake held
// for another session is announced once, not on every retry.
func TestAWakeWaitsForTheConsoleAndForItsSession(t *testing.T) {
	a := newTestApp(t)
	a.sessionID = "sess_here"
	wake := agent.Wake{SessionID: "sess_here", Tasks: []bgtask.Snapshot{{ID: "bg_3", Status: bgtask.StatusFailed}}}

	a.turnActive = true
	done := make(chan error, 1)
	a.startWakeTurn(wakeTurn{wake: wake, done: done})
	if err := <-done; !errors.Is(err, session.ErrSessionTurnBusy) {
		t.Fatalf("a wake during a turn = %v, want ErrSessionTurnBusy", err)
	}
	a.turnActive = false

	a.shellActive = true
	a.startWakeTurn(wakeTurn{wake: wake, done: done})
	if err := <-done; !errors.Is(err, session.ErrSessionTurnBusy) {
		t.Fatalf("a wake during a local command = %v, want ErrSessionTurnBusy", err)
	}
	a.shellActive = false

	elsewhere := agent.Wake{SessionID: "sess_left", Tasks: []bgtask.Snapshot{{ID: "bg_7", Status: bgtask.StatusSucceeded}}}
	for range 3 {
		a.startWakeTurn(wakeTurn{wake: elsewhere, done: done})
		if err := <-done; !errors.Is(err, session.ErrSessionTurnBusy) {
			t.Fatalf("a wake of another session = %v, want ErrSessionTurnBusy", err)
		}
	}
	got := transcriptText(a)
	if n := strings.Count(got, "bg_7 of session sess_left finished"); n != 1 {
		t.Fatalf("the held wake was announced %d times:\n%s", n, got)
	}
	if a.turnActive {
		t.Fatal("a held wake started a turn")
	}

	// Back in that session, the held wake runs, and the console forgets it:
	// what it remembers is only the wakes still waiting somewhere else.
	ran := make(chan *session.PromptRunOpts, 1)
	a.mgr = wakeBackend{ran: ran}
	a.sessionID = "sess_left"
	a.startWakeTurn(wakeTurn{wake: elsewhere, done: done})
	if opts := <-ran; opts == nil || opts.BackgroundWake == nil {
		t.Fatalf("the held wake ran without its marker: %+v", opts)
	}
	if err := <-done; err != nil {
		t.Fatalf("the held wake's turn = %v", err)
	}
	if len(a.wakesHeld) != 0 {
		t.Fatalf("the console still holds %v after the wake ran", a.wakesHeld)
	}
}

// wakeBackend is a console backend that runs a turn by returning at once.
type wakeBackend struct {
	backend
	ran chan *session.PromptRunOpts
}

func (b wakeBackend) HandleSessionPromptWithSender(_ context.Context, _ acp.SessionPromptParams, _ acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	b.ran <- opts
	return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn}, nil
}

// A woken turn refused as busy is the waker's to retry: the transcript does not
// call it a failed turn. Any other failure of a woken turn is reported.
func TestABusyWokenTurnIsNotReportedAsFailed(t *testing.T) {
	a := newTestApp(t)
	a.sessionID = "sess_here"
	a.turnActive, a.turnSessionID = true, "sess_here"
	a.applyLoopMessage(updateMsg{sessionID: "sess_here", update: turnDone{sessionID: "sess_here", err: session.ErrSessionTurnBusy, woken: true}})
	if got := transcriptText(a); strings.Contains(got, "Turn failed") {
		t.Fatalf("a busy wake was reported as a failed turn:\n%s", got)
	}
	a.turnActive, a.turnSessionID = true, "sess_here"
	a.applyLoopMessage(updateMsg{sessionID: "sess_here", update: turnDone{sessionID: "sess_here", err: errors.New("model unreachable"), woken: true}})
	if got := transcriptText(a); !strings.Contains(got, "Turn failed: model unreachable") {
		t.Fatalf("a woken turn's own failure went unreported:\n%s", got)
	}
}

// A woken turn - live, or replayed by /resume - shows nothing of its own: no
// note and no message the operator did not type, only the agent's answer, in a
// block of its own rather than at the tail of the answer before it.
func TestAWakeAddsNothingToTheTranscript(t *testing.T) {
	a := newTestApp(t)
	a.sessionID = "sess_here"
	chunk := func(text string) updateMsg {
		return updateMsg{sessionID: "sess_here", update: acp.MessageChunkUpdate{
			SessionUpdate: "agent_message_chunk",
			Content:       acp.ContentBlock{Type: "text", Text: text},
		}}
	}
	a.applyLoopMessage(chunk("The build runs in the background."))
	before := transcriptText(a)
	two := 2
	a.applyLoopMessage(updateMsg{sessionID: "sess_here", update: acp.BackgroundWakeUpdate{
		SessionUpdate: acp.UpdateTypeBackgroundWake,
		Tasks: []acp.BackgroundWakeTask{
			{ID: "bg_3", Kind: "command", Label: "make test", Status: "failed", ExitCode: &two, DurationMs: 90_000},
		},
	}})
	if got := transcriptText(a); got != before {
		t.Fatalf("the wake added to the transcript:\n%s\nwas:\n%s", got, before)
	}
	a.applyLoopMessage(chunk("The tests failed."))
	got := transcriptText(a)
	if !strings.Contains(got, "The tests failed.") || strings.Contains(got, "background.The tests") {
		t.Fatalf("the woken turn's answer is not a block of its own:\n%s", got)
	}
}

// The open task says how it ended first, then the exit code of a command, then how
// long it ran: the foot of an open card in the web UI.
func TestTaskOutcomeLine(t *testing.T) {
	now := time.Date(2026, 9, 18, 10, 1, 5, 0, time.UTC)
	ended := now.Add(-5 * time.Second)
	code, zero := 2, 0
	failed := bgtask.Snapshot{Kind: bgtask.KindCommand, Status: bgtask.StatusFailed, StartedAt: ended.Add(-90 * time.Second), FinishedAt: &ended, ExitCode: &code}
	if got := taskOutcomeLine(failed, now); got != "failed · exit 2 · 1m 30s" {
		t.Errorf("failed outcome = %q", got)
	}
	// No shell stands behind an agent run, so its synthetic exit code is not shown.
	agent := bgtask.Snapshot{Kind: bgtask.KindAgent, Status: bgtask.StatusSucceeded, StartedAt: ended.Add(-200 * time.Second), FinishedAt: &ended, ExitCode: &zero,
		Agent: &bgtask.AgentInfo{Name: "general", Model: "rpa/qwen3.6-35b-a3b", InputTokens: 900, OutputTokens: 100}}
	if got := taskOutcomeLine(agent, now); got != "succeeded · 3m 20s · qwen3.6-35b-a3b · 1k tokens" {
		t.Errorf("agent outcome = %q", got)
	}
	running := bgtask.Snapshot{Status: bgtask.StatusRunning, StartedAt: now.Add(-65 * time.Second)}
	if got := taskOutcomeLine(running, now); got != "1m 05s" {
		t.Errorf("running outcome = %q", got)
	}
}

// A command that exits non-zero is recorded with the error "exit status N", which the
// open task already says as "exit N"; any other error is shown.
func TestTaskErrorText(t *testing.T) {
	code := 2
	row := bgtask.Snapshot{Status: bgtask.StatusFailed, ExitCode: &code}
	for _, tc := range []struct{ err, want string }{
		{"exit status 2", ""},
		{" exit status 2 ", ""},
		{"exit status 1", "exit status 1"},
		{"signal: killed", "signal: killed"},
		{"", ""},
	} {
		row.Error = tc.err
		if got := taskErrorText(row); got != tc.want {
			t.Errorf("taskErrorText(%q) = %q, want %q", tc.err, got, tc.want)
		}
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
	for _, want := range []string{"Background tasks", "2 running", "3 in total", "shell", "make bg_2", "explore", "map the session package", "✓"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the list is missing %q:\n%s", want, text)
		}
	}
	// The mark says how a task ended; the row does not say it again in words.
	if strings.Contains(text, "succeeded") {
		t.Fatalf("a row repeats its mark in words:\n%s", text)
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
	for _, want := range []string{"make bg_1", "succeeded", "$ make bg_1", "built ok", "build/coddy 46 MB", "esc back"} {
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

// A turn that stopped at its step limit says so in the transcript (issue
// #255), where it used to end without a word.
func TestAStopNoticeIsShownWhenTheTurnEnds(t *testing.T) {
	a := newTestApp(t)
	a.sessionID = "sess_here"
	a.turnActive, a.turnSessionID = true, "sess_here"
	notice := "Stopped after 3 steps, the step limit set by agent.max_turns."
	a.applyLoopMessage(updateMsg{sessionID: "sess_here", update: turnDone{sessionID: "sess_here", stop: "max_turns", notice: notice}})
	if got := transcriptText(a); !strings.Contains(got, notice) {
		t.Fatalf("the stop notice is not in the transcript:\n%s", got)
	}
}
