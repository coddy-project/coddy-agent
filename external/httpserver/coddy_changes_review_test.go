//go:build http

package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestSessionChangesRevertPreservesDiffsOnFailure(t *testing.T) {
	e := newChangesEnv(t)
	e.storeTurn(t, 1, session.WorkspaceChange{Path: "a.txt", Before: wsFile("old"), After: wsFile("agent")})
	writeInWorkspace(t, e.cwd, "a.txt", "user")
	rec := e.do(t, http.MethodPost, "/coddy/sessions/"+e.id+"/changes/revert")
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	diff, err := session.LoadWorkspaceDiff(e.sessionDir, 1)
	if err != nil || diff == nil {
		t.Fatalf("rollback evidence lost: %+v %v", diff, err)
	}
	got, err := os.ReadFile(filepath.Join(e.cwd, "a.txt"))
	if err != nil || string(got) != "user" {
		t.Fatalf("later edit lost: %q %v", got, err)
	}
}

func TestSessionChangesSkipSuccessfulSettingsOnlyRequest(t *testing.T) {
	e := newChangesEnv(t)
	st := e.srv.mgr.SessionByID(e.id)
	before := session.TakeWorkspaceSnapshot(e.cwd)
	live := e.srv.beginLiveTurn(e.id, e.cwd, before)
	writeInWorkspace(t, e.cwd, "stray.txt", "unrelated")
	e.srv.settleTurnDiff(st, before, live, 0, nil)
	e.srv.bgWG.Wait()
	turns, err := session.ListStoredTurnDiffs(e.sessionDir)
	if err != nil || len(turns) != 0 {
		t.Fatalf("request without a turn stored %v: %v", turns, err)
	}
}
