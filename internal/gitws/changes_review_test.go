package gitws

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUncommittedChangesWithinASubdirectory(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
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
	})
}

func TestWorktreeChangesDoesNotFollowUntrackedSymlinks(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"a.txt": "old"})
		outside := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		changes, skipped, err := WorktreeChanges(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 0 {
			t.Fatalf("external content exposed: %+v", changes)
		}
		if skipped != 1 {
			t.Fatalf("untracked symlink must be counted as skipped, got %d", skipped)
		}
		change, err := WorktreeChangeFor(dir, "link.txt")
		if err != nil {
			t.Fatal(err)
		}
		if change != nil {
			t.Fatalf("untracked symlink must not have a detail: %+v", change)
		}
	})
}

func TestWorktreeChangesTrackedSymlinksUseLinkText(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"a.txt": "old"})
		outside := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		target := filepath.ToSlash(outside)
		assertChange := func(path, status string, before, after []byte) {
			t.Helper()
			changes, skipped, err := WorktreeChanges(dir)
			if err != nil {
				t.Fatal(err)
			}
			if skipped != 0 {
				t.Fatalf("tracked symlink was skipped: %d", skipped)
			}
			got := byPath(changes)[path]
			gotBefore := filepath.ToSlash(string(got.Before))
			gotAfter := filepath.ToSlash(string(got.After))
			wantBefore := filepath.ToSlash(string(before))
			wantAfter := filepath.ToSlash(string(after))
			if got.Status != status || gotBefore != wantBefore || gotAfter != wantAfter {
				t.Fatalf("%s: got %+v, want status=%s before=%q after=%q", path, got, status, before, after)
			}
			detail, err := WorktreeChangeFor(dir, path)
			if err != nil {
				t.Fatal(err)
			}
			if detail == nil || detail.Status != status || filepath.ToSlash(string(detail.Before)) != wantBefore || filepath.ToSlash(string(detail.After)) != wantAfter {
				t.Fatalf("%s detail: %+v", path, detail)
			}
			if string(got.Before) == "secret" || string(got.After) == "secret" {
				t.Fatalf("%s exposed the link target's contents", path)
			}
		}

		if err := os.Symlink(outside, filepath.Join(dir, "added-link")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		mustGit(t, dir, "add", "added-link")
		assertChange("added-link", "added", nil, []byte(target))

		if err := os.Symlink(outside, filepath.Join(dir, "deleted-link")); err != nil {
			t.Fatal(err)
		}
		mustGit(t, dir, "add", "deleted-link")
		mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-m", "links")
		if err := os.Remove(filepath.Join(dir, "deleted-link")); err != nil {
			t.Fatal(err)
		}
		assertChange("deleted-link", "deleted", []byte(target), nil)
	})
}
