package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// tasksRemoteStand answers the three background-task routes the way
// external/httpserver/background_http.go does, and records what was asked of it.
func tasksRemoteStand(t *testing.T) (*Handler, *[]string) {
	t.Helper()
	var asked []string
	row := func(id string, status bgtask.Status) map[string]any {
		return map[string]any{
			"id": id, "session_id": "sess_remote", "kind": "command", "command": "make test",
			"status": status, "started_at": "2026-09-18T10:00:00Z",
			"elapsed_seconds": 65, "running": !status.Finished(),
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /coddy/sessions/{id}/background-tasks", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.RequestURI())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "coddy.background_task_list", "running": 1,
			"data": []any{row("bg_2", bgtask.StatusRunning), row("bg_1", bgtask.StatusSucceeded)},
		})
	})
	mux.HandleFunc("GET /coddy/sessions/{id}/background-tasks/{task}", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.RequestURI())
		if r.PathValue("task") == "bg_missing" {
			http.Error(w, `{"error":{"message":"background task not found"}}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "coddy.background_task", "task": row(r.PathValue("task"), bgtask.StatusRunning), "output": "ok  pkg/a\n",
		})
	})
	mux.HandleFunc("POST /coddy/sessions/{id}/background-tasks/{task}/stop", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.RequestURI())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "coddy.background_task", "task": row(r.PathValue("task"), bgtask.StatusStopped), "output": "terminated\n",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h, &asked
}

func TestRemoteBackgroundTasksAreReadAndStoppedOverREST(t *testing.T) {
	h, asked := tasksRemoteStand(t)
	ctx := context.Background()

	rows, err := h.BackgroundTasks(ctx, "sess_remote")
	if err != nil {
		t.Fatalf("BackgroundTasks(): %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "bg_2" || rows[0].Status != bgtask.StatusRunning || rows[1].Status != bgtask.StatusSucceeded {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Command != "make test" || rows[0].StartedAt.IsZero() {
		t.Fatalf("a row lost its fields on the way: %+v", rows[0])
	}

	output, snap, err := h.BackgroundTaskOutput(ctx, "sess_remote", "bg_2", 40)
	if err != nil || output != "ok  pkg/a\n" || snap.ID != "bg_2" {
		t.Fatalf("BackgroundTaskOutput() = %q, %+v, %v", output, snap, err)
	}

	stopped, err := h.StopBackgroundTask(ctx, "sess_remote", "bg_2")
	if err != nil || stopped.Status != bgtask.StatusStopped {
		t.Fatalf("StopBackgroundTask() = %+v, %v", stopped, err)
	}

	want := []string{
		"GET /coddy/sessions/sess_remote/background-tasks",
		"GET /coddy/sessions/sess_remote/background-tasks/bg_2?tail=40",
		"POST /coddy/sessions/sess_remote/background-tasks/bg_2/stop",
	}
	if len(*asked) != len(want) {
		t.Fatalf("requests = %v, want %v", *asked, want)
	}
	for i := range want {
		if (*asked)[i] != want[i] {
			t.Fatalf("request %d = %q, want %q", i, (*asked)[i], want[i])
		}
	}
}

func TestRemoteUnknownBackgroundTaskReadsAsNotFound(t *testing.T) {
	h, _ := tasksRemoteStand(t)
	// The console tells "no such task" from "the server is unreachable" the same way
	// in both modes, so a remote 404 is the pool's own error.
	if _, _, err := h.BackgroundTaskOutput(context.Background(), "sess_remote", "bg_missing", 0); !errors.Is(err, bgtask.ErrNotFound) {
		t.Fatalf("err = %v, want bgtask.ErrNotFound", err)
	}
}
