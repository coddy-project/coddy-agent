//go:build http && unix

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestSessionDeleteForeignWorkConflict(t *testing.T) {
	for _, kind := range []string{"prompt", "task"} {
		t.Run(kind, func(t *testing.T) {
			srv, mgr, store := bulkDeleteServer(t)
			id := storeSession(t, mgr, store, "fixture")
			dir := store.SessionPath(id)
			if kind == "prompt" {
				unlock, ok, err := platform.TryLockFile(filepath.Join(dir, ".coddy-turn.lock"))
				if err != nil || !ok {
					t.Fatalf("lock: acquired=%v error=%v", ok, err)
				}
				t.Cleanup(unlock)
			} else {
				pool := bgtask.NewWithRunner(bgtask.Config{}, bgtask.NewCommandRunner())
				pool.SetSessionDir(id, dir)
				task, err := pool.Start(bgtask.Spec{SessionID: id, Kind: bgtask.KindCommand, Command: "sleep 60"})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = pool.Stop(id, task.ID) })
			}
			rec := httptest.NewRecorder()
			srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/coddy/sessions/"+id, nil))
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), id) || strings.Contains(rec.Body.String(), dir) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if !store.HasPersistedSnapshot(id) {
				t.Fatal("bundle removed")
			}
		})
	}
}

func TestSessionDeleteUnexpectedErrorIsGeneric(t *testing.T) {
	srv, mgr, store := bulkDeleteServer(t)
	id := storeSession(t, mgr, store, "fixture")
	dir := store.SessionPath(id)
	if err := os.Mkdir(filepath.Join(dir, ".coddy-turn.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/coddy/sessions/"+id, nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), dir) || strings.Contains(rec.Body.String(), "partially") || !strings.Contains(rec.Body.String(), "delete failed") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
