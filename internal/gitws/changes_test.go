package gitws

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commitRepo creates a repo whose HEAD holds the given files.
func commitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init", "-b", "main")
	for name, body := range files {
		writeFile(t, dir, name, body)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy",
		"commit", "-m", "init")
	return normPath(t, dir)
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func byPath(changes []WorkChange) map[string]WorkChange {
	out := make(map[string]WorkChange, len(changes))
	for _, c := range changes {
		out[c.Path] = c
	}
	return out
}

func TestUncommittedChangesReportsEveryTrackedKind(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{
			"keep.txt":     "same\n",
			"edit.txt":     "old\n",
			"remove.txt":   "bye\n",
			"dir/deep.txt": "deep\n",
		})
		writeFile(t, dir, "edit.txt", "new\n")
		writeFile(t, dir, "fresh.txt", "added\n")
		mustGit(t, dir, "add", "fresh.txt")
		if err := os.Remove(filepath.Join(dir, "remove.txt")); err != nil {
			t.Fatal(err)
		}

		changes, untracked, err := UncommittedChanges(dir)
		if err != nil {
			t.Fatalf("UncommittedChanges: %v", err)
		}
		if untracked != 0 {
			t.Fatalf("want no untracked files, got %d", untracked)
		}
		got := byPath(changes)
		if len(got) != 3 {
			t.Fatalf("want 3 changes, got %d: %+v", len(got), changes)
		}
		if c := got["edit.txt"]; c.Status != "modified" ||
			string(c.Before) != "old\n" || string(c.After) != "new\n" {
			t.Fatalf("edit.txt wrong: %+v", c)
		}
		if c := got["fresh.txt"]; c.Status != "added" || c.Before != nil ||
			string(c.After) != "added\n" {
			t.Fatalf("fresh.txt wrong: %+v", c)
		}
		if c := got["remove.txt"]; c.Status != "deleted" || c.After != nil ||
			string(c.Before) != "bye\n" {
			t.Fatalf("remove.txt wrong: %+v", c)
		}
		if _, ok := got["keep.txt"]; ok {
			t.Fatal("an unchanged file must not be reported")
		}
	})
}

// The reference UI counts untracked files and says so, but never reads them:
// a build directory would otherwise drag thousands of files into the diff.
func TestUncommittedChangesCountsUntrackedWithoutListingThem(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"a.txt": "a\n"})
		writeFile(t, dir, "junk1.txt", "x\n")
		writeFile(t, dir, "junk2.txt", "y\n")

		changes, untracked, err := UncommittedChanges(dir)
		if err != nil {
			t.Fatalf("UncommittedChanges: %v", err)
		}
		if untracked != 2 {
			t.Fatalf("want 2 untracked, got %d", untracked)
		}
		if len(changes) != 0 {
			t.Fatalf("untracked files must not be listed: %+v", changes)
		}
	})
}

// git show writes file bytes verbatim. Trimming them - which the package's own
// runGit does for command output - would silently corrupt every diff whose file
// starts or ends with whitespace.
func TestUncommittedChangesKeepsSurroundingWhitespace(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"pad.txt": "\n\nbody\n\n"})
		writeFile(t, dir, "pad.txt", "\n\nchanged\n\n")

		changes, _, err := UncommittedChanges(dir)
		if err != nil {
			t.Fatalf("UncommittedChanges: %v", err)
		}
		if len(changes) != 1 {
			t.Fatalf("want 1 change, got %+v", changes)
		}
		if string(changes[0].Before) != "\n\nbody\n\n" {
			t.Fatalf("before was trimmed: %q", changes[0].Before)
		}
		if string(changes[0].After) != "\n\nchanged\n\n" {
			t.Fatalf("after was trimmed: %q", changes[0].After)
		}
	})
}

// A rename is reported under the new path, with the old path's content as the
// "before" side, so the viewer shows what actually changed in the file.
func TestUncommittedChangesFollowsRenames(t *testing.T) {
	dir := commitRepo(t, map[string]string{"old-name.txt": "body\n"})
	mustGit(t, dir, "mv", "old-name.txt", "new-name.txt")

	changes, _, err := UncommittedChanges(dir)
	if err != nil {
		t.Fatalf("UncommittedChanges: %v", err)
	}
	got := byPath(changes)
	c, ok := got["new-name.txt"]
	if !ok {
		t.Fatalf("rename not reported under the new path: %+v", changes)
	}
	if string(c.Before) != "body\n" {
		t.Fatalf("rename lost the old content: %+v", c)
	}
}

// A folder that is not a repository is an empty change set, not an error: the
// viewer disables the scope instead of showing a failure.
func TestUncommittedChangesOutsideARepo(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		changes, untracked, err := UncommittedChanges(t.TempDir())
		if err != nil {
			t.Fatalf("want no error outside a repo, got %v", err)
		}
		if len(changes) != 0 || untracked != 0 {
			t.Fatalf("want an empty result, got %+v / %d", changes, untracked)
		}
	})
}

// A repository storing LF blobs next to a CRLF working copy (git's autocrlf, the
// normal setup on Windows) must not read as "every line changed": git applies
// its line-ending filter when it diffs, and reading the blob straight past that
// filter would report a whole-file rewrite for a one-line edit.
func TestUncommittedChangesIgnoresLineEndingOnlyDifferences(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"crlf.txt": "alpha\nbeta\ngamma\n"})
		// Same text, Windows endings, with exactly one real edit.
		writeFile(t, dir, "crlf.txt", "alpha\r\nBETA\r\ngamma\r\n")

		changes, _, err := UncommittedChanges(dir)
		if err != nil {
			t.Fatalf("UncommittedChanges: %v", err)
		}
		if len(changes) != 1 {
			t.Fatalf("want 1 change, got %+v", changes)
		}
		before, after := string(changes[0].Before), string(changes[0].After)
		if strings.Contains(before, "\r") || strings.Contains(after, "\r") {
			t.Fatalf("sides must be normalised: before=%q after=%q", before, after)
		}
		if before != "alpha\nbeta\ngamma\n" {
			t.Fatalf("before wrong: %q", before)
		}
		if after != "alpha\nBETA\ngamma\n" {
			t.Fatalf("after wrong: %q", after)
		}
	})
}

// A file that differs only in its line endings changed nothing a reader cares
// about, so it must not be listed at all.
func TestUncommittedChangesDropsPureLineEndingChurn(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"same.txt": "one\ntwo\n"})
		writeFile(t, dir, "same.txt", "one\r\ntwo\r\n")

		changes, _, err := UncommittedChanges(dir)
		if err != nil {
			t.Fatalf("UncommittedChanges: %v", err)
		}
		if len(changes) != 0 {
			t.Fatalf("line-ending-only churn must not be reported: %+v", changes)
		}
	})
}

// The review window loads one patch at a time, so asking for a single file must
// not read every other changed file's blob out of the object store: on a large
// working copy that is one git subprocess per file, per request.
func TestUncommittedChangeForReadsOneFile(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{
			"a.txt": "a1\n",
			"b.txt": "b1\n",
			"c.txt": "c1\n",
		})
		writeFile(t, dir, "a.txt", "a2\n")
		writeFile(t, dir, "b.txt", "b2\n")

		change, err := UncommittedChangeFor(dir, "b.txt")
		if err != nil {
			t.Fatalf("UncommittedChangeFor: %v", err)
		}
		if change == nil {
			t.Fatal("b.txt changed but was not reported")
		}
		if change.Status != "modified" ||
			string(change.Before) != "b1\n" || string(change.After) != "b2\n" {
			t.Fatalf("b.txt wrong: %+v", change)
		}
	})
}

// A file the working copy did not touch is absent, not an error: the HTTP layer
// turns that into the same 404 every other scope gives.
func TestUncommittedChangeForUnknownPath(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"a.txt": "a1\n"})
		writeFile(t, dir, "a.txt", "a2\n")

		for _, p := range []string{"c.txt", "../escape", ""} {
			change, err := UncommittedChangeFor(dir, p)
			if err != nil {
				t.Fatalf("path %q: %v", p, err)
			}
			if change != nil {
				t.Fatalf("path %q must not resolve: %+v", p, change)
			}
		}
	})
}

func TestWorktreeChangesIncludesUntrackedFiles(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"tracked.txt": "old\n"})
		writeFile(t, dir, "tracked.txt", "new\n")
		writeFile(t, dir, "fresh.txt", "brand new\n")
		writeFile(t, dir, "deep/nested.txt", "also new\n")

		changes, skipped, err := WorktreeChanges(dir)
		if err != nil {
			t.Fatalf("WorktreeChanges: %v", err)
		}
		if skipped != 0 {
			t.Fatalf("nothing should be skipped here, got %d", skipped)
		}
		got := byPath(changes)
		if len(got) != 3 {
			t.Fatalf("want 3 changes, got %d: %+v", len(got), changes)
		}
		if c := got["tracked.txt"]; c.Status != "modified" || string(c.Before) != "old\n" {
			t.Fatalf("tracked.txt wrong: %+v", c)
		}
		// An untracked file has no previous version, so it reads as an addition.
		fresh := got["fresh.txt"]
		if fresh.Status != "added" || fresh.Before != nil ||
			string(fresh.After) != "brand new\n" {
			t.Fatalf("fresh.txt wrong: %+v", fresh)
		}
		if c := got[filepath.FromSlash("deep/nested.txt")]; string(c.After) != "also new\n" {
			t.Fatalf("nested untracked file missing or wrong: %+v", c)
		}
	})
}

// .gitignore is what keeps a build directory out of the review, so the scope
// must honour it exactly as git does.
func TestWorktreeChangesHonoursGitignore(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{
			".gitignore": "ignored/\n*.log\n",
			"a.txt":      "a\n",
		})
		writeFile(t, dir, "ignored/junk.txt", "junk\n")
		writeFile(t, dir, "debug.log", "noise\n")
		writeFile(t, dir, "kept.txt", "kept\n")

		changes, _, err := WorktreeChanges(dir)
		if err != nil {
			t.Fatalf("WorktreeChanges: %v", err)
		}
		got := byPath(changes)
		if _, ok := got["kept.txt"]; !ok {
			t.Fatalf("an untracked, unignored file must be listed: %+v", changes)
		}
		for _, unwanted := range []string{"debug.log", filepath.FromSlash("ignored/junk.txt")} {
			if _, ok := got[unwanted]; ok {
				t.Fatalf("%s is ignored by git and must not appear", unwanted)
			}
		}
	})
}

// A workspace with a huge pile of new files is not a review, so the scope shows
// what it can and says how much it left out rather than reading all of it.
func TestWorktreeChangesCapsUntrackedCount(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"a.txt": "a\n"})
		for i := 0; i < maxUntrackedFiles+25; i++ {
			writeFile(t, dir, fmt.Sprintf("new%04d.txt", i), "x\n")
		}

		changes, skipped, err := WorktreeChanges(dir)
		if err != nil {
			t.Fatalf("WorktreeChanges: %v", err)
		}
		if len(changes) != maxUntrackedFiles {
			t.Fatalf("want %d changes, got %d", maxUntrackedFiles, len(changes))
		}
		if skipped != 25 {
			t.Fatalf("want 25 skipped, got %d", skipped)
		}
	})
}

func TestWorktreeChangeForResolvesAnUntrackedFile(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"a.txt": "a\n"})
		writeFile(t, dir, "fresh.txt", "brand new\n")

		change, err := WorktreeChangeFor(dir, "fresh.txt")
		if err != nil {
			t.Fatalf("WorktreeChangeFor: %v", err)
		}
		if change == nil || change.Status != "added" ||
			string(change.After) != "brand new\n" {
			t.Fatalf("fresh.txt wrong: %+v", change)
		}
	})
}

// The path arrives from a request, so a file git does not report as untracked
// must not become readable through this route.
func TestWorktreeChangeForRefusesFilesGitDoesNotList(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{
			".gitignore": "secret.txt\n",
			"a.txt":      "a\n",
		})
		writeFile(t, dir, "secret.txt", "do not read\n")

		for _, p := range []string{"secret.txt", "a.txt", "../escape.txt", "missing.txt"} {
			change, err := WorktreeChangeFor(dir, p)
			if err != nil {
				t.Fatalf("path %q: %v", p, err)
			}
			if change != nil {
				t.Fatalf("path %q must not resolve: %+v", p, change)
			}
		}
	})
}
