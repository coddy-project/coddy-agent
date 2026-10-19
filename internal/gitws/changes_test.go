package gitws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
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

// migrationFolders are the folders the supervisor asks about; the package does
// not know they hold migrations.
var migrationFolders = []string{"migrations", "alembic/versions", "db/migrate", "db/changelog"}

// blobID is the id git itself gives the bytes of a file, so the tests hold the
// package's own hashing to git's rather than to a second copy of it.
func blobID(t *testing.T, dir, name string) string {
	t.Helper()
	return mustGit(t, dir, "hash-object", "--no-filters", "--", name)
}

// leveled is the hash of text whose line endings were already levelled to LF.
func leveled(s string) string {
	return plumbing.ComputeHash(plumbing.BlobObject, []byte(s)).String()
}

// appendWithShell appends a line the way an agent's shell loop does, outside
// any editor tool. Without a shell, or on Windows, it writes the file instead.
func appendWithShell(t *testing.T, sh, dir, name, line string) {
	t.Helper()
	if sh == "" || runtime.GOOS == "windows" {
		writeFile(t, dir, name, readFile(t, dir, name)+line+"\n")
		return
	}
	cmd := exec.Command(sh, "-c", `for f in "$1"; do printf '%s\n' "$2" >> "$f"; done`, "sh", name, line)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shell append to %s: %v\n%s", name, err, out)
	}
}

func mustBaseline(t *testing.T, dir string) FileBaseline {
	t.Helper()
	base, err := Baseline(context.Background(), dir, migrationFolders)
	if err != nil {
		t.Fatalf("Baseline: %v", err)
	}
	return base
}

func mustChangedSince(t *testing.T, dir string, base FileBaseline) map[string]string {
	t.Helper()
	got, err := ChangedSince(context.Background(), dir, base, migrationFolders)
	if err != nil {
		t.Fatalf("ChangedSince: %v", err)
	}
	return got
}

func requireChanged(t *testing.T, got, want map[string]string) {
	t.Helper()
	if !maps.Equal(got, want) {
		t.Fatalf("changed files\n got  %v\n want %v", got, want)
	}
}

// The supervisor does not care how a migration changed: an editor tool, a shell
// loop, a deletion, a rename or a commit all have to show up, and only files
// that existed at the start do.
func TestChangedSinceFindsAMigrationChangedByAnyMeans(t *testing.T) {
	sh, _ := exec.LookPath("sh")
	dir := commitRepo(t, map[string]string{
		"migrations/0001_init.sql":        "create table users();\n",
		"backend/app/migrations/0002.sql": "create table orders();\n",
		"alembic/versions/a1.py":          "revision = 'a1'\n",
		"db/migrate/001.rb":               "class Init; end\n",
		"db/changelog/c.xml":              "<changelog/>\n",
		"migrations/0004_eol.sql":         "create table a();\ncreate table b();\n",
		"src/main.go":                     "package main\n",
	})
	base := mustBaseline(t, dir)
	if base.Head != mustGit(t, dir, "rev-parse", "HEAD") || len(base.Dirty) != 0 {
		t.Fatalf("a clean working copy has its head and nothing dirty: %+v", base)
	}

	appendWithShell(t, sh, dir, "migrations/0001_init.sql", "alter table users add column age int;")
	if err := os.Remove(filepath.Join(dir, "db/migrate/001.rb")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "alembic/versions/a1.py"), filepath.Join(dir, "alembic/versions/a2.py")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "src/main.go", "package main\n\nfunc main() {}\n")
	// Added after the start, staged or not: the start commit never had it.
	writeFile(t, dir, "migrations/0003_new.sql", "create table fresh();\n")
	writeFile(t, dir, "db/migrate/002.rb", "class Staged; end\n")
	mustGit(t, dir, "add", "db/migrate/002.rb")
	// Neither a line-ending change nor a mode change is a change of content.
	writeFile(t, dir, "migrations/0004_eol.sql", "create table a();\r\ncreate table b();\r\n")
	if err := os.Chmod(filepath.Join(dir, "db/changelog/c.xml"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Committing an edit, and a deletion, does not hide them from the start.
	writeFile(t, dir, "backend/app/migrations/0002.sql", "create table orders(id int);\n")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy",
		"commit", "-m", "edit 0002", "--", "backend/app/migrations/0002.sql")

	requireChanged(t, mustChangedSince(t, dir, base), map[string]string{
		"migrations/0001_init.sql":        blobID(t, dir, "migrations/0001_init.sql"),
		"backend/app/migrations/0002.sql": blobID(t, dir, "backend/app/migrations/0002.sql"),
		"db/migrate/001.rb":               "",
		"alembic/versions/a1.py":          "",
	})
}

// A migration already edited when the goal was set is the user's work, not the
// agent's, until it changes again - and going back to the committed text is a
// change from what the start saw.
func TestChangedSinceDoesNotBlameAnEditPendingAtTheStart(t *testing.T) {
	const name = "migrations/0001_init.sql"
	dir := commitRepo(t, map[string]string{name: "create table users();\n", "src/main.go": "package main\n"})
	writeFile(t, dir, name, "create table users(id int);\n")
	writeFile(t, dir, "src/main.go", "package main // edited\n")

	base := mustBaseline(t, dir)
	requireChanged(t, base.Dirty, map[string]string{name: blobID(t, dir, name)})
	requireChanged(t, mustChangedSince(t, dir, base), map[string]string{})

	writeFile(t, dir, name, "create table users(id int, name text);\n")
	requireChanged(t, mustChangedSince(t, dir, base), map[string]string{name: blobID(t, dir, name)})

	writeFile(t, dir, name, "create table users();\n")
	requireChanged(t, mustChangedSince(t, dir, base), map[string]string{name: blobID(t, dir, name)})

	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	requireChanged(t, mustChangedSince(t, dir, base), map[string]string{name: ""})
}

// Only the text of a file is compared, never its line endings, and that holds
// for the content a file had at the start as much as for the commit's: the
// hash ChangedSince returns is the one of the levelled text.
func TestChangedSinceIgnoresLineEndingsOfAFileDirtyAtTheStart(t *testing.T) {
	const (
		lf   = "migrations/0001_lf.sql"
		crlf = "migrations/0002_crlf.sql"
	)
	dir := commitRepo(t, map[string]string{lf: "a\nb\n", crlf: "c\nd\n"})
	writeFile(t, dir, lf, "a\nB\n")
	writeFile(t, dir, crlf, "c\r\nD\r\n")
	base := mustBaseline(t, dir)
	requireChanged(t, base.Dirty, map[string]string{lf: leveled("a\nB\n"), crlf: leveled("c\nD\n")})

	writeFile(t, dir, lf, "a\r\nB\r\n")
	writeFile(t, dir, crlf, "c\nD\n")
	requireChanged(t, mustChangedSince(t, dir, base), map[string]string{})

	writeFile(t, dir, lf, "a\r\nZ\r\n")
	writeFile(t, dir, crlf, "c\nY\n")
	requireChanged(t, mustChangedSince(t, dir, base), map[string]string{
		lf: leveled("a\nZ\n"), crlf: leveled("c\nY\n"),
	})
}

// The caller says "unavailable" for an error and "no changes" for an empty
// result, so a question the package cannot answer must never come back empty.
func TestBaselineAndChangedSinceFailWhenTheyCannotAnswer(t *testing.T) {
	live := context.Background()
	cancelled, cancel := context.WithCancel(live)
	cancel()
	expired, stop := context.WithDeadline(live, time.Now().Add(-time.Second))
	defer stop()

	repo := commitRepo(t, map[string]string{"migrations/0001.sql": "a\n"})
	start := mustBaseline(t, repo)
	plain := t.TempDir()
	unborn := t.TempDir()
	mustGit(t, unborn, "init", "-b", "main")

	cases := []struct {
		name string
		run  func() error
		want error // nil: any error will do
	}{
		{"baseline outside a repository", func() error { _, err := Baseline(live, plain, migrationFolders); return err }, ErrNotRepository},
		{"changed-since outside a repository", func() error { _, err := ChangedSince(live, plain, start, migrationFolders); return err }, ErrNotRepository},
		{"baseline without a commit", func() error { _, err := Baseline(live, unborn, migrationFolders); return err }, nil},
		{"changed-since without a head", func() error {
			_, err := ChangedSince(live, repo, FileBaseline{}, migrationFolders)
			return err
		}, nil},
		{"changed-since from a commit the repository lacks", func() error {
			_, err := ChangedSince(live, repo, FileBaseline{Head: strings.Repeat("ab", 20)}, migrationFolders)
			return err
		}, nil},
		{"changed-since from an abbreviated head", func() error {
			_, err := ChangedSince(live, repo, FileBaseline{Head: start.Head[:12]}, migrationFolders)
			return err
		}, nil},
		{"changed-since from a branch name", func() error {
			_, err := ChangedSince(live, repo, FileBaseline{Head: "main"}, migrationFolders)
			return err
		}, nil},
		{"changed-since from a head that reads as an option", func() error {
			_, err := ChangedSince(live, repo, FileBaseline{Head: "--output=" + filepath.Join(plain, "x")}, migrationFolders)
			return err
		}, nil},
		{"baseline with a cancelled context", func() error { _, err := Baseline(cancelled, repo, migrationFolders); return err }, context.Canceled},
		{"changed-since with a cancelled context", func() error { _, err := ChangedSince(cancelled, repo, start, migrationFolders); return err }, context.Canceled},
		{"baseline past its deadline", func() error { _, err := Baseline(expired, repo, migrationFolders); return err }, context.DeadlineExceeded},
		{"changed-since past its deadline", func() error { _, err := ChangedSince(expired, repo, start, migrationFolders); return err }, context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("want an error, got an answer")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
	if _, statErr := os.Stat(filepath.Join(plain, "x")); statErr == nil {
		t.Fatal("a head that looks like an option reached git")
	}
}

// Without the git binary the comparison says so: it does not fall back to
// go-git, which would have to load whole blobs unbounded. A folder that is no
// repository at all is still reported as that.
func TestBaselineAndChangedSinceNeedTheGitBinary(t *testing.T) {
	repo := commitRepo(t, map[string]string{"migrations/0001.sql": "a\n"})
	start := mustBaseline(t, repo)
	plain := t.TempDir()
	hideGitBinary(t)

	live := context.Background()
	for _, tc := range []struct {
		name string
		run  func() (any, error)
		want error
	}{
		{"baseline in a repository", func() (any, error) { return Baseline(live, repo, migrationFolders) }, ErrNeedsGitBinary},
		{"changed-since in a repository", func() (any, error) { return ChangedSince(live, repo, start, migrationFolders) }, ErrNeedsGitBinary},
		{"baseline in a plain folder", func() (any, error) { return Baseline(live, plain, migrationFolders) }, ErrNotRepository},
		{"changed-since in a plain folder", func() (any, error) { return ChangedSince(live, plain, start, migrationFolders) }, ErrNotRepository},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.run(); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// A user's `diff.relative=true` makes `git diff` print paths relative to the
// folder it runs in; the workspace below the repository root must still find
// its files, under paths relative to itself and with nothing from outside.
func TestChangedSinceHoldsWhenGitDiffPrintsRelativePaths(t *testing.T) {
	repo := commitRepo(t, map[string]string{
		"app/migrations/0001.sql":   "a\n",
		"other/migrations/0001.sql": "b\n",
	})
	mustGit(t, repo, "config", "diff.relative", "true")
	dir := filepath.Join(repo, "app")
	base := mustBaseline(t, dir)

	writeFile(t, repo, "app/migrations/0001.sql", "a2\n")
	writeFile(t, repo, "other/migrations/0001.sql", "b2\n")
	requireChanged(t, mustChangedSince(t, dir, base), map[string]string{
		"migrations/0001.sql": blobID(t, repo, "app/migrations/0001.sql"),
	})
}

// A child that git leaves behind holding its stdout (a credential helper, a
// hook, a pager) must not keep the caller past the end of its context: the
// pipe's reader is abandoned shortly after git is killed.
func TestRunGitRawContextEndsWithItsContextWhenAChildHoldsThePipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake git is a shell script")
	}
	bin := t.TempDir()
	spawned := filepath.Join(bin, "spawned")
	// The script marks the moment its child holds the pipe, so the context ends
	// after that and never before.
	script := "#!/bin/sh\nsleep 6 &\n: > '" + spawned + "'\nsleep 6\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		err error
		at  time.Time
	}
	done := make(chan outcome, 1)
	go func() {
		_, err := runGitRawContext(ctx, t.TempDir(), "status")
		done <- outcome{err, time.Now()}
	}()
	for waitUntil := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(spawned); err == nil {
			break
		}
		if time.Now().After(waitUntil) {
			cancel()
			t.Fatal("the fake git never started its child")
		}
	}
	cancelledAt := time.Now()
	cancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatal("a git that was cancelled answered")
		}
		if took := got.at.Sub(cancelledAt); took > 3*time.Second {
			t.Fatalf("runGitRawContext returned %v after its context ended: a child holding the pipe outlived it", took)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runGitRawContext did not return")
	}
}

// The workspace is a folder of the repository: its paths are relative to it and
// the rest of the repository is none of its business, however it changed.
func TestChangedSinceSeesOnlyTheWorkspaceFolder(t *testing.T) {
	repo := commitRepo(t, map[string]string{
		"app/migrations/0001.sql":   "a\n",
		"app/src/main.go":           "package main\n",
		"other/migrations/0001.sql": "b\n",
		"migrations/0001.sql":       "c\n",
	})
	dir := filepath.Join(repo, "app")
	writeFile(t, repo, "other/migrations/0001.sql", "b pending\n")

	inApp, atRoot := mustBaseline(t, dir), mustBaseline(t, repo)
	if len(inApp.Dirty) != 0 || len(atRoot.Dirty) != 1 || atRoot.Head != inApp.Head {
		t.Fatalf("dirty files belong to the folder asked about: app %+v, root %+v", inApp, atRoot)
	}

	writeFile(t, repo, "app/migrations/0001.sql", "a2\n")
	writeFile(t, repo, "app/src/main.go", "package main // edited\n")
	writeFile(t, repo, "other/migrations/0001.sql", "b2\n")
	writeFile(t, repo, "migrations/0001.sql", "c2\n")

	requireChanged(t, mustChangedSince(t, dir, inApp), map[string]string{
		"migrations/0001.sql": blobID(t, repo, "app/migrations/0001.sql"),
	})
	requireChanged(t, mustChangedSince(t, repo, atRoot), map[string]string{
		"app/migrations/0001.sql":   blobID(t, repo, "app/migrations/0001.sql"),
		"other/migrations/0001.sql": blobID(t, repo, "other/migrations/0001.sql"),
		"migrations/0001.sql":       blobID(t, repo, "migrations/0001.sql"),
	})
}

// git does not stat a file whose index entry says assume-unchanged or
// skip-worktree, so a migration rewritten under either flag is invisible to
// `git diff`. It still counts: the flagged files under the folders are compared
// by content with the commit, and one that already differed at the start is the
// user's until it changes again.
func TestChangedSinceComparesFilesGitDoesNotStat(t *testing.T) {
	const (
		assumed = "migrations/0001_assumed.sql"
		skipped = "migrations/0002_skipped.sql"
		both    = "migrations/0003_both.sql"
		gone    = "migrations/0004_gone.sql"
		pending = "migrations/0005_pending.sql"
		quiet   = "migrations/0006_quiet.sql"
		fresh   = "migrations/0007_fresh.sql"
	)
	dir := commitRepo(t, map[string]string{
		assumed: "create table a();\n", skipped: "create table b();\n", both: "create table c();\n",
		gone: "create table d();\n", pending: "create table e();\n", quiet: "create table f();\n",
		"src/main.go": "package main\n",
	})
	mustGit(t, dir, "update-index", "--assume-unchanged", assumed, gone, pending, quiet)
	mustGit(t, dir, "update-index", "--skip-worktree", skipped, both)
	mustGit(t, dir, "update-index", "--assume-unchanged", both) // ls-files -v says "s"
	writeFile(t, dir, pending, "create table e(id int);\n")

	base := mustBaseline(t, dir)
	requireChanged(t, base.Dirty, map[string]string{pending: blobID(t, dir, pending)})

	writeFile(t, dir, assumed, "create table a(id int);\n")
	writeFile(t, dir, skipped, "create table b(id int);\n")
	writeFile(t, dir, both, "create table c(id int);\n")
	if err := os.Remove(filepath.Join(dir, gone)); err != nil {
		t.Fatal(err)
	}
	// Added after the start, then flagged: the commit never had it.
	writeFile(t, dir, fresh, "create table g();\n")
	mustGit(t, dir, "add", fresh)
	mustGit(t, dir, "update-index", "--assume-unchanged", fresh)

	want := map[string]string{
		assumed: blobID(t, dir, assumed),
		skipped: blobID(t, dir, skipped),
		both:    blobID(t, dir, both),
		gone:    "",
	}
	requireChanged(t, mustChangedSince(t, dir, base), want)

	writeFile(t, dir, pending, "create table e(id int, n text);\n")
	want[pending] = blobID(t, dir, pending)
	requireChanged(t, mustChangedSince(t, dir, base), want)
}

// The folders are matched on the path from the repository root, so a workspace
// that is itself a migration folder (or sits below one) still sees its files,
// under paths relative to the workspace, and nothing outside it.
func TestChangedSinceInsideAMigrationFolder(t *testing.T) {
	for _, tc := range []struct {
		name, workspace, folder string
		// the workspace itself is no migration folder: a file beside the
		// folder is not a migration
		decoy bool
	}{
		{"db", "db", "db/migrate", true},
		{"migrations", "migrations", "migrations", false},
		{"db/migrate", "db/migrate", "db/migrate", false},
		{"nested db", "app/db", "app/db/changelog", true},
		{"below a migration folder", "migrations/sub", "migrations/sub", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pendingFull, cleanFull := tc.folder+"/001.sql", tc.folder+"/002.sql"
			repo := commitRepo(t, map[string]string{
				pendingFull: "a\n", cleanFull: "b\n",
				tc.workspace + "/seed.sql": "seed\n",
				"other/migrations/x.sql":   "x\n",
				"src/main.go":              "package main\n",
			})
			dir := filepath.Join(repo, filepath.FromSlash(tc.workspace))
			// What the workspace calls these files.
			pending := strings.TrimPrefix(pendingFull, tc.workspace+"/")
			clean := strings.TrimPrefix(cleanFull, tc.workspace+"/")
			writeFile(t, repo, pendingFull, "a pending\n")

			base := mustBaseline(t, dir)
			requireChanged(t, base.Dirty, map[string]string{pending: blobID(t, repo, pendingFull)})
			requireChanged(t, mustChangedSince(t, dir, base), map[string]string{})

			writeFile(t, repo, cleanFull, "b2\n")
			writeFile(t, repo, tc.workspace+"/seed.sql", "seed2\n")
			writeFile(t, repo, "other/migrations/x.sql", "x2\n")
			writeFile(t, repo, "src/main.go", "package main // edited\n")
			want := map[string]string{clean: blobID(t, repo, cleanFull)}
			if !tc.decoy {
				want["seed.sql"] = blobID(t, repo, tc.workspace+"/seed.sql")
			}
			requireChanged(t, mustChangedSince(t, dir, base), want)
		})
	}
}

// No file is read whole past the cap: a candidate over it is an error saying so,
// whichever side is the big one, and never a quiet "unchanged".
func TestComparisonRefusesAFileOverTheCap(t *testing.T) {
	const name = "migrations/0001_big.sql"
	atCap := strings.Repeat("x", maxCompareBytes)
	t.Run("working copy over the cap", func(t *testing.T) {
		dir := commitRepo(t, map[string]string{name: "small\n"})
		base := FileBaseline{Head: mustGit(t, dir, "rev-parse", "HEAD")}

		writeFile(t, dir, name, atCap)
		requireChanged(t, mustChangedSince(t, dir, base), map[string]string{name: blobID(t, dir, name)})

		writeFile(t, dir, name, atCap+"x")
		_, err := ChangedSince(context.Background(), dir, base, migrationFolders)
		if err == nil || !strings.Contains(err.Error(), "larger than") || !strings.Contains(err.Error(), name) {
			t.Fatalf("ChangedSince on a file one byte over the cap: got %v, want an error naming the file and the limit", err)
		}
		_, err = Baseline(context.Background(), dir, migrationFolders)
		if err == nil || !strings.Contains(err.Error(), "larger than") {
			t.Fatalf("Baseline on a file one byte over the cap: got %v, want an error naming the limit", err)
		}
	})
	t.Run("committed file over the cap", func(t *testing.T) {
		dir := commitRepo(t, map[string]string{name: atCap + "x"})
		base := FileBaseline{Head: mustGit(t, dir, "rev-parse", "HEAD")}
		writeFile(t, dir, name, "small\n")
		_, err := ChangedSince(context.Background(), dir, base, migrationFolders)
		if err == nil || !strings.Contains(err.Error(), "larger than") {
			t.Fatalf("ChangedSince on a committed file over the cap: got %v, want an error naming the limit", err)
		}
	})
}
