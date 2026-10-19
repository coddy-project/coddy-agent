//go:build cli

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// A turn that moved the session to another folder - a worktree it opened
// or the main checkout it came back to - leaves the header listing the
// context of the folder the next turn works in, not the one it left.
func TestHeaderFollowsTheSessionToAnotherFolder(t *testing.T) {
	app := newReasoningApp(t)
	st := app.mgr.SessionByID(app.sessionID)
	if st == nil {
		t.Fatal("no session")
	}
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "AGENTS.md"), []byte("worktree rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The header names a workspace document by its path in the folder.
	const want = "AGENTS.md"
	app.header.SetExpanded(true)
	if rows := renderedRows(app.header, 400); rows[want] {
		t.Fatalf("the header lists %s before the session went there", want)
	}

	app.turnSessionID = app.sessionID
	st.SetCWD(elsewhere)
	app.applyLoopMessage(updateMsg{update: turnDone{sessionID: app.sessionID}})

	if rows := renderedRows(app.header, 400); !rows[want] {
		t.Fatalf("after the turn moved the session the header does not list %s:\n%v", want, rows)
	}
}
