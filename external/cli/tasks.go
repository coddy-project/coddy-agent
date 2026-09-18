//go:build cli

package cli

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// Background tasks of the session on screen.
//
// The console keeps one read of the session's tasks: the status line counts the
// running ones and the /tasks overlay lists them. The read goes through the backend,
// so it is the pool of this process locally and the server's REST routes under
// --remote, and it happens on a worker - a remote read is a network round trip and
// must not stall the UI loop. It is repeated on the cadence of the web UI's Tasks
// panel: every 2.5 s while something runs, a turn is in flight or the overlay is
// open, every 15 s otherwise.

const (
	tasksPollActive = 2500 * time.Millisecond
	tasksPollIdle   = 15 * time.Second
	tasksReadBudget = 10 * time.Second
)

// tasksLoaded carries one read of a session's tasks back to the UI loop.
type tasksLoaded struct {
	sessionID string
	rows      []bgtask.Snapshot
	err       error
}

// tasksPollDue is the poll timer's note to the UI loop.
type tasksPollDue struct{}

// taskOutputLoaded carries the output of the task open in the overlay.
type taskOutputLoaded struct {
	sessionID string
	taskID    string
	output    string
	snap      bgtask.Snapshot
	err       error
}

// taskStopped carries the answer to a stop the operator asked for.
type taskStopped struct {
	sessionID string
	taskID    string
	err       error
}

// countRunningTasks is how many tasks run right now, as the status line counts them.
// A system task - the memory run the runtime starts for every turn - is left out, like
// Pool.RunningCount leaves it out: it is not work the model or the operator started.
func countRunningTasks(rows []bgtask.Snapshot) int {
	running := 0
	for _, row := range rows {
		if !row.Status.Finished() && !row.SystemTask() {
			running++
		}
	}
	return running
}

// sortTasksNewestFirst orders tasks by start time, newest first, the one order the
// web UI's panel uses.
func sortTasksNewestFirst(rows []bgtask.Snapshot) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].StartedAt.After(rows[j].StartedAt) })
}

// refreshTasks reads the session's tasks on a worker. One read is in flight at a
// time: a slow server must not collect a queue of identical requests behind it.
func (a *App) refreshTasks() {
	sessionID := strings.TrimSpace(a.sessionID)
	if sessionID == "" || a.mgr == nil || a.workCtx == nil || a.tasksReading {
		return
	}
	a.tasksReading = true
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, tasksReadBudget)
		defer cancel()
		rows, err := a.mgr.BackgroundTasks(ctx, sessionID)
		select {
		case a.updatesCh <- updateMsg{update: tasksLoaded{sessionID: sessionID, rows: rows, err: err}}:
		case <-a.closed:
		}
	}()
}

// applyTasksLoaded adopts a read and arms the next one.
func (a *App) applyTasksLoaded(u tasksLoaded) {
	a.tasksReading = false
	if u.sessionID == strings.TrimSpace(a.sessionID) {
		if u.err == nil {
			sortTasksNewestFirst(u.rows)
			a.tasks = u.rows
			a.runningTasks = countRunningTasks(u.rows)
			a.foot.SetRunningTasks(a.runningTasks)
			if m := a.tasksOverlay(); m != nil {
				m.SetRows(u.rows)
				// The task on screen is still writing: read what it printed since.
				if id := m.OpenTaskID(); id != "" {
					for _, row := range u.rows {
						if row.ID == id && !row.Status.Finished() {
							a.readTaskOutput(id)
						}
					}
				}
			}
		}
		// An unreadable answer keeps the last rows: unreachable is not "no tasks".
	}
	a.armTasksPoll()
}

// tasksPollInterval is how soon the tasks are worth reading again.
func (a *App) tasksPollInterval() time.Duration {
	if a.turnActive || a.remoteTurnActive || a.runningTasks > 0 || a.tasksOverlay() != nil {
		return tasksPollActive
	}
	return tasksPollIdle
}

// tasksOverlay is the /tasks overlay when it is what the editor's place shows.
func (a *App) tasksOverlay() *tasksModal {
	m, _ := a.modal.(*tasksModal)
	return m
}

// openTasksOverlay is /tasks: the session's background tasks in the place of the
// editor. It opens on what the last read knew and asks for a fresh one.
func (a *App) openTasksOverlay() {
	if strings.TrimSpace(a.sessionID) == "" {
		a.appendStatus(roleWarning, "No session yet")
		return
	}
	m := newTasksModal(a.theme, a.screen.RequestRender)
	if a.tasks != nil {
		m.SetRows(a.tasks)
	}
	m.OnOpen = a.readTaskOutput
	m.OnStop = a.stopTask
	m.OnRefresh = func() {
		a.refreshTasks()
		if id := m.OpenTaskID(); id != "" {
			a.readTaskOutput(id)
		}
	}
	m.OnClose = a.closeModal
	a.openModal(m)
	a.refreshTasks()
	a.armTasksPoll()
}

// readTaskOutput reads the output of one task on a worker.
func (a *App) readTaskOutput(taskID string) {
	sessionID := strings.TrimSpace(a.sessionID)
	if sessionID == "" || a.mgr == nil || a.workCtx == nil {
		return
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, tasksReadBudget)
		defer cancel()
		output, snap, err := a.mgr.BackgroundTaskOutput(ctx, sessionID, taskID, tasksOutputTail)
		select {
		case a.updatesCh <- updateMsg{update: taskOutputLoaded{sessionID: sessionID, taskID: taskID, output: output, snap: snap, err: err}}:
		case <-a.closed:
		}
	}()
}

// stopTask terminates one task on a worker: a stop waits for the process group to
// settle, and under --remote it is a request to the server.
func (a *App) stopTask(taskID string) {
	sessionID := strings.TrimSpace(a.sessionID)
	if sessionID == "" || a.mgr == nil || a.workCtx == nil {
		return
	}
	if m := a.tasksOverlay(); m != nil {
		m.SetNote("Stopping " + taskID + "…")
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		ctx, cancel := context.WithTimeout(a.workCtx, 30*time.Second)
		defer cancel()
		_, err := a.mgr.StopBackgroundTask(ctx, sessionID, taskID)
		select {
		case a.updatesCh <- updateMsg{update: taskStopped{sessionID: sessionID, taskID: taskID, err: err}}:
		case <-a.closed:
		}
	}()
}

func (a *App) applyTaskOutputLoaded(u taskOutputLoaded) {
	m := a.tasksOverlay()
	if m == nil || u.sessionID != strings.TrimSpace(a.sessionID) {
		return
	}
	if u.err != nil {
		m.SetNote("Could not read the output of " + u.taskID + ": " + u.err.Error())
		return
	}
	m.SetOutput(u.taskID, u.output, u.snap.OutputTruncated)
}

func (a *App) applyTaskStopped(u taskStopped) {
	if u.sessionID != strings.TrimSpace(a.sessionID) {
		return
	}
	if m := a.tasksOverlay(); m != nil {
		if u.err != nil {
			m.SetNote("Could not stop " + u.taskID + ": " + u.err.Error())
		} else {
			m.SetNote("Stopped " + u.taskID)
			if m.OpenTaskID() == u.taskID {
				a.readTaskOutput(u.taskID)
			}
		}
	} else if u.err != nil {
		a.appendStatus(roleWarning, "Could not stop "+u.taskID+": "+u.err.Error())
	}
	a.refreshTasks()
}

// armTasksPoll schedules the next read; a poll already armed is replaced.
func (a *App) armTasksPoll() {
	a.stopTasksPoll()
	if a.workCtx == nil || a.workCtx.Err() != nil {
		return
	}
	a.tasksTimer = a.usageAfter(a.tasksPollInterval(), func() {
		select {
		case a.updatesCh <- updateMsg{update: tasksPollDue{}}:
		case <-a.closed:
		}
	})
}

func (a *App) stopTasksPoll() {
	if a.tasksTimer != nil {
		a.tasksTimer()
		a.tasksTimer = nil
	}
}

// resetTasks drops what is known about another session's tasks and reads this one's.
func (a *App) resetTasks() {
	a.tasks = nil
	a.runningTasks = 0
	a.foot.SetRunningTasks(0)
	a.refreshTasks()
}

// toolTouchesTasks reports whether a finished call of this tool can have changed the
// session's tasks, so the count follows the call instead of the next poll.
func toolTouchesTasks(toolName string) bool {
	n := strings.ToLower(strings.TrimSpace(toolName))
	return n == "run_command" || n == "spawn_agent" || strings.HasPrefix(n, "background_")
}

// The three things a task row says, the same three the web UI's task card says
// (external/ui/src/ui/tasks/taskStatus.ts): what stands behind the task, the work
// itself, and how it is going.

// taskTag is what stands behind the task: a subagent's name, "memory" for the memory
// run of a turn, "shell" for a command.
func taskTag(row bgtask.Snapshot) string {
	if row.SystemTask() {
		return "memory"
	}
	if row.Kind == bgtask.KindAgent {
		if row.Agent != nil && strings.TrimSpace(row.Agent.Name) != "" {
			return strings.TrimSpace(row.Agent.Name)
		}
		return "agent"
	}
	return "shell"
}

// taskTitle is the work itself. The pool labels an agent run "agent <name>:
// <description>" and a memory run "memory: <first line>"; the tag says the first half
// already, so the title keeps the second. A run nobody described gets a plain name.
func taskTitle(row bgtask.Snapshot) string {
	label := strings.TrimSpace(row.Label)
	if row.Kind != bgtask.KindAgent {
		if label != "" {
			return label
		}
		return strings.TrimSpace(row.Command)
	}
	name := ""
	if row.Agent != nil {
		name = strings.ToLower(strings.TrimSpace(row.Agent.Name))
	}
	if head, rest, found := strings.Cut(label, ":"); found {
		head = strings.ToLower(strings.TrimSpace(head))
		if head == "memory" || head == "agent "+name {
			if rest = strings.TrimSpace(rest); rest != "" {
				return rest
			}
			return "Subagent run"
		}
	}
	if label == "" || strings.ToLower(label) == "agent "+name {
		return "Subagent run"
	}
	return label
}

// taskMetaLine is how the task is going: elapsed against the estimate while it runs,
// the outcome, the duration and the exit code afterwards. An agent run has no process
// behind it, so the pool's synthetic exit code for it is left out.
func taskMetaLine(row bgtask.Snapshot, now time.Time) string {
	elapsed := formatElapsed(row.Elapsed(now))
	if !row.Status.Finished() {
		parts := []string{elapsed}
		if row.ExpectedSeconds > 0 {
			parts = append(parts, "est. "+formatElapsed(time.Duration(row.ExpectedSeconds)*time.Second))
		}
		if row.Overdue(now) {
			parts = append(parts, "overdue")
		}
		return strings.Join(parts, " · ")
	}
	parts := []string{string(row.Status), elapsed}
	if row.Kind != bgtask.KindAgent && row.ExitCode != nil {
		parts = append(parts, "exit "+itoa(*row.ExitCode))
	}
	return strings.Join(parts, " · ")
}
