package gitws

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUncommittedChangesWithinASubdirectory(t *testing.T) {
	dir := commitRepo(t, map[string]string{"sub/a.txt": "old\n", "outside.txt": "old\n"})
	writeFile(t, dir, "sub/a.txt", "new\n")
	writeFile(t, dir, "outside.txt", "new\n")
	sub := filepath.Join(dir, "sub")
	changes, _, err := UncommittedChanges(sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "a.txt" || string(changes[0].Before) != "old\n" || string(changes[0].After) != "new\n" {
		t.Fatalf("subdirectory changes: %+v", changes)
	}
	change, err := UncommittedChangeFor(sub, "a.txt")
	if err != nil || change == nil || string(change.After) != "new\n" {
		t.Fatalf("file detail: %+v %v", change, err)
	}
}

func TestWorktreeChangesDoesNotFollowUntrackedSymlinks(t *testing.T) {
	dir := commitRepo(t, map[string]string{"a.txt": "old"})
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	changes, _, err := WorktreeChanges(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("external content exposed: %+v", changes)
	}
}
