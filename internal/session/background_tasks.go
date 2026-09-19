package session

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// The background tasks of a session as a surface in this process reads them: the
// console's /tasks overlay and its running-task count. The HTTP rows and these methods
// go through the same two reads of the pool (bgtask.Pool.SessionTasks and
// SessionTaskOutput), so every surface lists the same tasks, the ones an earlier
// process recorded included. A console attached over --remote has the same three
// methods on remote.Handler, backed by the REST routes.

// taskPool is the pool the manager reads: the process-wide one, which is also what the
// shell tools and the HTTP surface use.
func (m *Manager) taskPool() *bgtask.Pool {
	return bgtask.Default()
}

// sessionTaskDir is the bundle directory the session's task records live under; empty
// for a session that was never persisted, whose tasks exist in the pool alone.
func (m *Manager) sessionTaskDir(sessionID string) string {
	st := m.getSession(strings.TrimSpace(sessionID))
	if st == nil {
		return ""
	}
	return strings.TrimSpace(st.GetPersistedSessionDir())
}

// BackgroundTasks lists every background task of the session.
func (m *Manager) BackgroundTasks(_ context.Context, sessionID string) ([]bgtask.Snapshot, error) {
	id := strings.TrimSpace(sessionID)
	return m.taskPool().SessionTasks(id, m.sessionTaskDir(id)), nil
}

// BackgroundTaskOutput reads the captured output of one task, trimmed to its last
// tailLines lines when that is above zero.
func (m *Manager) BackgroundTaskOutput(_ context.Context, sessionID, taskID string, tailLines int) (string, bgtask.Snapshot, error) {
	id := strings.TrimSpace(sessionID)
	return m.taskPool().SessionTaskOutput(id, m.sessionTaskDir(id), strings.TrimSpace(taskID), tailLines)
}

// StopBackgroundTask terminates a task and everything it started, and returns the
// row it settled on.
func (m *Manager) StopBackgroundTask(_ context.Context, sessionID, taskID string) (bgtask.Snapshot, error) {
	return m.taskPool().Stop(strings.TrimSpace(sessionID), strings.TrimSpace(taskID))
}
