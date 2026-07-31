//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
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

// attachBackgroundWaker lets a finished task that asked for it start an
// autonomous turn, which is what makes a session usable while nobody watches
// it. The turn goes through the manager's normal prompt path, so it takes the
// composer turn lock and waits for any turn already in flight.
func (s *Server) attachBackgroundWaker() {
	waker := agent.NewBackgroundWaker(s.log, func(ctx context.Context, sessionID, instruction string) error {
		if bgtask.Default().Draining() {
			return nil
		}
		if s.mgr.SessionByID(sessionID) == nil {
			if _, err := s.mgr.HandleSessionLoad(ctx, acp.SessionLoadParams{
				SessionID: sessionID,
				CWD:       s.defaultCWD,
			}); err != nil {
				return err
			}
		}
		s.bgWG.Add(1)
		defer s.bgWG.Done()
		_, err := s.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
			SessionID: sessionID,
			Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: instruction}},
		}, planRunNoopSender{}, nil)
		return err
	})
	waker.Attach(bgtask.Default())
}

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
}

func newBackgroundTaskRow(snap bgtask.Snapshot, now time.Time) backgroundTaskRow {
	return backgroundTaskRow{
		Snapshot:       snap,
		ElapsedSeconds: int(snap.Elapsed(now) / time.Second),
		Overdue:        snap.Overdue(now),
		Running:        !snap.Status.Finished(),
	}
}

// backgroundRowsForSession merges the live pool with what the session bundle
// recorded, so tasks from a previous process still appear (as orphaned) instead
// of vanishing from the drawer after a restart.
func backgroundRowsForSession(sessionID, sessionDir string, now time.Time) []backgroundTaskRow {
	live := bgtask.Default().List(sessionID)
	seen := make(map[string]bool, len(live))
	rows := make([]backgroundTaskRow, 0, len(live))
	for _, snap := range live {
		seen[snap.ID] = true
		rows = append(rows, newBackgroundTaskRow(snap, now))
	}

	for _, snap := range bgtask.LoadPersisted(sessionDir) {
		if seen[snap.ID] {
			continue
		}
		snap.SessionID = sessionID
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

	output, snap, err := bgtask.Default().Output(id, taskID, tail)
	if err != nil {
		// The pool forgets tasks from an earlier process; the session bundle
		// still has the record and the log.
		row, ok := findPersistedTask(sessionDir, taskID)
		if !ok {
			http.Error(w, `{"error":{"message":"background task not found"}}`, http.StatusNotFound)
			return
		}
		row.SessionID = id
		persisted, dropped, _ := bgtask.PersistedOutput(sessionDir, taskID)
		row.OutputTruncated = row.OutputTruncated || dropped
		writeBackgroundTask(w, id, newBackgroundTaskRow(row, now), tailLines(persisted, tail))
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

func findPersistedTask(sessionDir, taskID string) (bgtask.Snapshot, bool) {
	for _, snap := range bgtask.LoadPersisted(sessionDir) {
		if snap.ID == taskID {
			return snap, true
		}
	}
	return bgtask.Snapshot{}, false
}

// tailLines trims text to its last n lines, matching what the pool does for a
// live task so both paths answer the same shape.
func tailLines(text string, n int) string {
	if n <= 0 || text == "" {
		return text
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
