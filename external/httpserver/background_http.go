//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// registerBackgroundRoutes wires the background task surface the tasks panel
// polls. The pool is process-wide, so these handlers answer for whatever the
// agent loop started, including tasks whose turn already ended.
func (s *Server) registerBackgroundRoutes() {
	s.mux.HandleFunc("GET /coddy/sessions/{id}/background-tasks", s.coddyBackgroundTasksList)
	s.mux.HandleFunc("DELETE /coddy/sessions/{id}/background-tasks", s.coddyBackgroundTasksClear)
	s.mux.HandleFunc("GET /coddy/sessions/{id}/background-tasks/{task_id}", s.coddyBackgroundTaskGet)
	s.mux.HandleFunc("POST /coddy/sessions/{id}/background-tasks/{task_id}/stop", s.coddyBackgroundTaskStop)
}

// AttachBackgroundWaker subscribes a waker of this server's own, for a process
// in which nothing else owns one - a test that drives the HTTP surface alone.
// `coddy serve` does not call it: its runtime owns the process waker and hands
// a woken turn to this server through RunBackgroundWake.
func (s *Server) AttachBackgroundWaker() {
	agent.NewBackgroundWaker(s.log, func(ctx context.Context, wake agent.Wake) error {
		_, err := s.RunBackgroundWake(ctx, wake)
		return err
	}).Attach(bgtask.Default())
}

// RunBackgroundWake runs the turn finished background tasks started, through
// this server: it can run any session's, so it always handles the wake.
//
// The turn takes the composer turn lock before anything else, like a turn a
// client posted: registering the relay first would evict the relay of a turn
// that is still running, and the watchers of that turn would lose it to a wake
// that is about to be refused as busy and tried again.
//
// Its frames go to the session's composer relay, so a browser or a console
// following the session watches it, and its first message is the wake itself
// (agent.Wake.RunOpts). Nobody started it, but somebody can answer for it: a
// permission prompt goes to the relay and is persisted as the session's pending
// prompt, and whoever shows it first - the web UI, a console following the turn
// - answers it through POST /coddy/sessions/{id}/permission. It waits the way
// the prompt of a browser turn whose tab was closed does. A question is still
// refused: nothing persists one for a client that arrives later.
func (s *Server) RunBackgroundWake(ctx context.Context, wake agent.Wake) (bool, error) {
	if bgtask.Default().Draining() {
		return true, nil
	}
	sessionID := strings.TrimSpace(wake.SessionID)
	if s.mgr.SessionByID(sessionID) == nil {
		if _, err := s.mgr.HandleSessionLoad(ctx, acp.SessionLoadParams{
			SessionID: sessionID,
		}); err != nil {
			return true, err
		}
	}
	st := s.mgr.SessionByID(sessionID)
	if st == nil {
		return true, fmt.Errorf("background wake: session %s is not live", sessionID)
	}
	unlock, err := s.mgr.AcquireComposerTurnLock(sessionID, st)
	if err != nil {
		return true, err
	}
	defer unlock()
	s.bgWG.Add(1)
	defer s.bgWG.Done()
	rel := s.beginComposerRelay(sessionID)
	defer s.endComposerRelay(sessionID, rel)
	bridge := NewWakeRelaySender(s.activeCfg(), rel, st.GetMode())
	bridge.SetSessionDir(strings.TrimSpace(st.GetPersistedSessionDir()))
	defer func() { _ = bridge.FinishStream() }()
	opts := wake.RunOpts()
	opts.SkipTurnLock = true
	_, err = s.mgr.HandleSessionPromptWithSender(ctx, wake.PromptParams(), bridge, opts)
	return true, err
}

var _ agent.WakeSurface = (*Server)(nil)

func (s *Server) coddyBackgroundTasksClear(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	if dir := strings.TrimSpace(st.GetPersistedSessionDir()); dir != "" {
		bgtask.Default().SetSessionDir(id, dir)
	}

	cleared := bgtask.Default().ClearFinished(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.background_tasks_cleared",
		"sessionId": id,
		"cleared":   cleared,
	})
}

// backgroundTaskRow is the JSON shape the SPA renders. Elapsed and overdue are
// computed server-side so every client agrees on them without re-deriving the
// clock arithmetic.
type backgroundTaskRow struct {
	bgtask.Snapshot
	ElapsedSeconds int  `json:"elapsed_seconds"`
	Overdue        bool `json:"overdue"`
	Running        bool `json:"running"`
	// PendingPermission is set while a detached subagent behind this task is
	// blocked on a permission prompt; the web UI shows it in the parent chat.
	// The join happens here rather than in bgtask, which must not learn about
	// the ACP types.
	PendingPermission *detachedPermissionDTO `json:"pending_permission,omitempty"`
}

func newBackgroundTaskRow(snap bgtask.Snapshot, now time.Time) backgroundTaskRow {
	row := backgroundTaskRow{
		Snapshot:       snap,
		ElapsedSeconds: int(snap.Elapsed(now) / time.Second),
		Overdue:        snap.Overdue(now),
		Running:        !snap.Status.Finished(),
	}
	if snap.Agent != nil && row.Running {
		row.PendingPermission = pendingDetachedPermission(snap.Agent.SessionID)
	}
	return row
}

// backgroundRowsForSession renders every task of the session: the live pool and, under
// it, what the bundle recorded for an earlier process (bgtask.Pool.SessionTasks).
func backgroundRowsForSession(sessionID, sessionDir string, now time.Time) []backgroundTaskRow {
	snaps := bgtask.Default().SessionTasks(sessionID, sessionDir)
	rows := make([]backgroundTaskRow, 0, len(snaps))
	for _, snap := range snaps {
		rows = append(rows, newBackgroundTaskRow(snap, now))
	}
	return rows
}

func (s *Server) coddyBackgroundTasksList(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}

	now := time.Now()
	rows := backgroundRowsForSession(id, strings.TrimSpace(st.GetPersistedSessionDir()), now)
	running := 0
	for _, row := range rows {
		if row.Running {
			running++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.background_task_list",
		"sessionId": id,
		"running":   running,
		"data":      rows,
	})
}

func (s *Server) coddyBackgroundTaskGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	taskID := strings.TrimSpace(r.PathValue("task_id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}
	sessionDir := strings.TrimSpace(st.GetPersistedSessionDir())
	now := time.Now()

	tail := 0
	if v := strings.TrimSpace(r.URL.Query().Get("tail")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			http.Error(w, `{"error":{"message":"tail must be a non-negative integer"}}`, http.StatusBadRequest)
			return
		}
		tail = n
	}

	// The pool forgets tasks from an earlier process; the session bundle still has
	// the record and the log, and SessionTaskOutput reads whichever knows the task.
	output, snap, err := bgtask.Default().SessionTaskOutput(id, sessionDir, taskID, tail)
	if err != nil {
		http.Error(w, `{"error":{"message":"background task not found"}}`, http.StatusNotFound)
		return
	}
	writeBackgroundTask(w, id, newBackgroundTaskRow(snap, now), output)
}

func (s *Server) coddyBackgroundTaskStop(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	taskID := strings.TrimSpace(r.PathValue("task_id"))
	st := s.coddyEnsureLoaded(w, r, id)
	if st == nil {
		return
	}

	snap, err := bgtask.Default().Stop(id, taskID)
	if err != nil {
		// An unknown id is a 404; a task that exists but could not be
		// terminated is a server-side failure and must not read as "no such
		// task", which would tell the operator the process is gone.
		if errors.Is(err, bgtask.ErrNotFound) {
			http.Error(w, `{"error":{"message":"background task not found"}}`, http.StatusNotFound)
			return
		}
		s.log.Error("background task stop", "session", id, "task", taskID, "error", err)
		http.Error(w, `{"error":{"message":"could not stop the background task"}}`, http.StatusInternalServerError)
		return
	}

	output, _, _ := bgtask.Default().Output(id, taskID, 0)
	writeBackgroundTask(w, id, newBackgroundTaskRow(snap, time.Now()), output)
}

func writeBackgroundTask(w http.ResponseWriter, sessionID string, row backgroundTaskRow, output string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":    "coddy.background_task",
		"sessionId": sessionID,
		"task":      row,
		"output":    output,
	})
}
