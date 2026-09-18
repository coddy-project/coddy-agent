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
		}
		// An unreadable answer keeps the last rows: unreachable is not "no tasks".
	}
	a.armTasksPoll()
}

// tasksPollInterval is how soon the tasks are worth reading again.
func (a *App) tasksPollInterval() time.Duration {
	if a.turnActive || a.remoteTurnActive || a.runningTasks > 0 {
		return tasksPollActive
	}
	return tasksPollIdle
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
