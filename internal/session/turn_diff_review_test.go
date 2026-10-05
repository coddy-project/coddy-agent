package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreWorkspaceFilesPreservesLaterEdits(t *testing.T) {
	for _, kind := range []string{"modified", "added", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			cwd, dir := t.TempDir(), t.TempDir()
			ch := WorkspaceChange{Path: "a.txt", Before: file("old"), After: file("agent")}
			if kind == "added" {
				ch.Before = nil
			}
			if kind == "deleted" {
				ch.After = nil
			}
			storeTurn(t, dir, 1, ch)
			writeWorkspaceFile(t, cwd, "a.txt", "user")
			if _, err := RestoreWorkspaceFiles(cwd, dir, 0); err == nil {
				t.Fatal("rollback accepted a file changed after the turn")
			}
			got, err := os.ReadFile(filepath.Join(cwd, "a.txt"))
			if err != nil || string(got) != "user" {
				t.Fatalf("later edit lost: %q %v", got, err)
			}
		})
	}
}

func TestRestoreWorkspaceFilesRejectsTraversal(t *testing.T) {
	parent := t.TempDir()
	cwd := filepath.Join(parent, "workspace")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := writeWorkspaceFile(t, parent, "outside.txt", "keep")
	dir := t.TempDir()
	storeTurn(t, dir, 1, WorkspaceChange{Path: filepath.Join("..", "outside.txt"), Before: file("bad"), After: file("keep")})
	if _, err := RestoreWorkspaceFiles(cwd, dir, 0); err == nil {
		t.Fatal("traversing rollback accepted")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "keep" {
		t.Fatalf("outside file changed: %q %v", got, err)
	}
}

func TestRestoreWorkspaceFilesRejectsSymlinkEscape(t *testing.T) {
	cwd, outside, dir := t.TempDir(), t.TempDir(), t.TempDir()
	p := writeWorkspaceFile(t, outside, "a.txt", "keep")
	if err := os.Symlink(outside, filepath.Join(cwd, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	storeTurn(t, dir, 1, WorkspaceChange{Path: filepath.Join("link", "a.txt"), Before: file("bad"), After: file("keep")})
	if _, err := RestoreWorkspaceFiles(cwd, dir, 0); err == nil {
		t.Fatal("symlink escape accepted")
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "keep" {
		t.Fatalf("outside file changed: %q %v", got, err)
	}
}

func TestRestoreWorkspaceFilesReportsUnreadableDiff(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(TurnDiffsDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(TurnDiffsDir(dir), "turn_1.json"), []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreWorkspaceFiles(t.TempDir(), dir, 0); err == nil {
		t.Fatal("unreadable diff reported success")
	}
}

func TestWorkspaceDiffDoesNotDeleteFilesPastSizeLimit(t *testing.T) {
	cwd := t.TempDir()
	p := writeWorkspaceFile(t, cwd, "a.txt", "old")
	before := TakeWorkspaceSnapshot(cwd)
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxFileSizeBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, compute := range []func(string, *WorkspaceSnapshot) (*WorkspaceDiff, error){ComputeWorkspaceDiff, LiveWorkspaceDiff} {
		diff, err := compute(cwd, before)
		if err != nil {
			t.Fatal(err)
		}
		if diff != nil {
			t.Fatalf("an omitted file was reported deleted: %+v", diff)
		}
	}
}

func TestLatestTurnChangesIncludesAnEmptyTurn(t *testing.T) {
	dir := t.TempDir()
	storeTurn(t, dir, 1, WorkspaceChange{Path: "a.txt", After: file("one")})
	if err := StoreWorkspaceDiff(dir, 2, nil); err != nil {
		t.Fatal(err)
	}
	n, err := LatestTurnNumber(dir)
	if err != nil || n != 2 {
		t.Fatalf("latest turn = %d, %v; want 2", n, err)
	}
}

func TestWorkspaceDiffDoesNotAddPreviouslyOversizedFiles(t *testing.T) {
	cwd := t.TempDir()
	p := writeWorkspaceFile(t, cwd, "a.txt", "old")
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxFileSizeBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	before := TakeWorkspaceSnapshot(cwd)
	writeWorkspaceFile(t, cwd, "a.txt", "small")
	for _, compute := range []func(string, *WorkspaceSnapshot) (*WorkspaceDiff, error){ComputeWorkspaceDiff, LiveWorkspaceDiff} {
		diff, err := compute(cwd, before)
		if err != nil {
			t.Fatal(err)
		}
		if diff != nil {
			t.Fatalf("pre-existing file reported added: %+v", diff)
		}
	}
}

func TestRestoreWorkspaceFilesPreflightsAllFiles(t *testing.T) {
	cwd, dir := t.TempDir(), t.TempDir()
	writeWorkspaceFile(t, cwd, "a.txt", "agent")
	writeWorkspaceFile(t, cwd, "z.txt", "user")
	storeTurn(t, dir, 1,
		WorkspaceChange{Path: "a.txt", Before: file("old"), After: file("agent")},
		WorkspaceChange{Path: "z.txt", Before: file("old"), After: file("agent")})
	if _, err := RestoreWorkspaceFiles(cwd, dir, 0); err == nil {
		t.Fatal("expected conflict")
	}
	got, err := os.ReadFile(filepath.Join(cwd, "a.txt"))
	if err != nil || string(got) != "agent" {
		t.Fatalf("preflight modified a file: %q %v", got, err)
	}
}

func TestRestoreWorkspaceFilesRejectsAnotherWorkspace(t *testing.T) {
	old, cwd, dir := t.TempDir(), t.TempDir(), t.TempDir()
	diff := &WorkspaceDiff{CWD: old, Changes: []WorkspaceChange{{Path: "a.txt", Before: file("old"), After: file("agent")}}}
	if err := StoreWorkspaceDiff(dir, 1, diff); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, cwd, "a.txt", "agent")
	if _, err := RestoreWorkspaceFiles(cwd, dir, 0); err == nil {
		t.Fatal("rollback accepted another workspace")
	}
}

func TestWorkspaceDiffExcludesSessionStore(t *testing.T) {
	cwd := t.TempDir()
	store := filepath.Join(cwd, "sessions")
	writeWorkspaceFile(t, cwd, "sessions/session.json", "before")
	before := TakeWorkspaceSnapshot(cwd, store)
	writeWorkspaceFile(t, cwd, "sessions/session.json", "after")
	writeWorkspaceFile(t, cwd, "a.txt", "agent")
	diff, err := ComputeWorkspaceDiff(cwd, before)
	if err != nil || diff == nil || len(diff.Changes) != 1 || diff.Changes[0].Path != "a.txt" {
		t.Fatalf("session bookkeeping captured: %+v, %v", diff, err)
	}
}

func TestRestoreWorkspaceFilesCollapsesTurnsAndCanRetry(t *testing.T) {
	cwd, dir := t.TempDir(), t.TempDir()
	storeTurn(t, dir, 1, WorkspaceChange{Path: "a.txt", Before: file("old"), After: file("one")})
	storeTurn(t, dir, 2, WorkspaceChange{Path: "a.txt", Before: file("one"), After: file("two")})
	writeWorkspaceFile(t, cwd, "a.txt", "two")
	if _, err := RestoreWorkspaceFiles(cwd, dir, 0); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(cwd, "a.txt"))
	if err != nil || string(got) != "old" {
		t.Fatalf("wrong original content: %q, %v", got, err)
	}
	if _, err := RestoreWorkspaceFiles(cwd, dir, 0); err != nil {
		t.Fatalf("retry failed after files were already restored: %v", err)
	}
}
