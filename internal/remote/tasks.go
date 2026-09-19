package remote

import (
	"context"
	"net/url"
	"strconv"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// Background tasks of a session on the server the console is attached to.
//
// The console lists the running processes of a session, reads their output and stops
// them the same way in both modes: in-process through session.Manager, over --remote
// through these three methods, which wrap the REST routes the web UI's Tasks panel uses
// (GET /coddy/sessions/{id}/background-tasks[/{task}], POST .../{task}/stop). The rows
// decode straight into bgtask.Snapshot: the server renders that type, plus computed
// fields the console works out for itself from the status and the start time.

type backgroundTaskListResponse struct {
	Data []bgtask.Snapshot `json:"data"`
}

type backgroundTaskResponse struct {
	Task   bgtask.Snapshot `json:"task"`
	Output string          `json:"output"`
}

func backgroundTasksPath(sessionID string) string {
	return "/coddy/sessions/" + url.PathEscape(sessionID) + "/background-tasks"
}

// taskError reads a remote 404 as the pool's own "no such task", so a caller tells it
// from an unreachable server the same way in both modes.
func taskError(err error) error {
	if isNotFound(err) {
		return bgtask.ErrNotFound
	}
	return err
}

// BackgroundTasks lists every background task of the session, the ones an earlier
// server process recorded included.
func (h *Handler) BackgroundTasks(ctx context.Context, sessionID string) ([]bgtask.Snapshot, error) {
	var res backgroundTaskListResponse
	if err := h.getJSON(ctx, backgroundTasksPath(sessionID), &res); err != nil {
		if isNotFound(err) {
			// A session minted here and not yet persisted there has no tasks.
			return nil, nil
		}
		return nil, err
	}
	return res.Data, nil
}

// BackgroundTaskOutput reads the captured output of one task, trimmed by the server
// to its last tailLines lines when that is above zero.
func (h *Handler) BackgroundTaskOutput(ctx context.Context, sessionID, taskID string, tailLines int) (string, bgtask.Snapshot, error) {
	path := backgroundTasksPath(sessionID) + "/" + url.PathEscape(taskID)
	if tailLines > 0 {
		path += "?tail=" + strconv.Itoa(tailLines)
	}
	var res backgroundTaskResponse
	if err := h.getJSON(ctx, path, &res); err != nil {
		return "", bgtask.Snapshot{}, taskError(err)
	}
	return res.Output, res.Task, nil
}

// StopBackgroundTask terminates a task on the server and returns the row it settled on.
func (h *Handler) StopBackgroundTask(ctx context.Context, sessionID, taskID string) (bgtask.Snapshot, error) {
	var res backgroundTaskResponse
	path := backgroundTasksPath(sessionID) + "/" + url.PathEscape(taskID) + "/stop"
	if err := h.postJSON(ctx, path, nil, &res); err != nil {
		return bgtask.Snapshot{}, taskError(err)
	}
	return res.Task, nil
}
