package gitws

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// eachBackend runs body twice: once with git on PATH, where the package drives
// the git binary, and once with git hidden from PATH, where it has to answer
// with its built-in implementation. The fixtures are built with gitBin either
// way, so a machine without git skips both.
func eachBackend(t *testing.T, body func(t *testing.T)) {
	t.Helper()
	t.Run("system", func(t *testing.T) {
		if gitBin == "" {
			t.Skip("git binary not available")
		}
		body(t)
	})
	t.Run("builtin", func(t *testing.T) {
		if gitBin == "" {
			t.Skip("git binary not available to build the fixture")
		}
		hideGitBinary(t)
		body(t)
	})
}

// hideGitBinary leaves the test with a PATH that holds no git, which is what a
// machine without git looks like to the package.
func hideGitBinary(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	if GitAvailable() {
		t.Fatal("git is still reachable with an empty PATH")
	}
}

func TestDescribeLinkedWorktree(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := initRepo(t)
		linked := filepath.Join(t.TempDir(), "login")
		mustGit(t, dir, "worktree", "add", linked, "feature/login")
		linked = normPath(t, linked)

		info := Describe(linked)
		if !info.IsGitRepo {
			t.Fatalf("linked worktree not reported as a repository: %+v", info)
		}
		if info.Branch != "feature/login" {
			t.Fatalf("branch = %q, want feature/login", info.Branch)
		}
		if !info.IsWorktree {
			t.Fatalf("a linked worktree must be flagged: %+v", info)
		}
		if normPath(t, info.RepoRoot) != dir {
			t.Fatalf("repo root = %q, want the main checkout %q", info.RepoRoot, dir)
		}
		if info.BaseBranch != "main" {
			t.Fatalf("base branch = %q, want main", info.BaseBranch)
		}
		if len(info.Worktrees) != 2 {
			t.Fatalf("worktrees = %+v, want the main checkout and the linked one", info.Worktrees)
		}
		first := info.Worktrees[0]
		if !first.Main || normPath(t, first.Path) != dir || first.Branch != "main" {
			t.Fatalf("first worktree = %+v, want the main checkout on main", first)
		}
		second := info.Worktrees[1]
		if second.Main || normPath(t, second.Path) != linked || second.Branch != "feature/login" {
			t.Fatalf("second worktree = %+v, want %s on feature/login", second, linked)
		}
		if !slices.Contains(info.Branches, "main") || !slices.Contains(info.Branches, "feature/login") {
			t.Fatalf("branches = %v", info.Branches)
		}
		if got := normPath(t, MainCheckoutRoot(linked)); got != dir {
			t.Fatalf("main checkout root of the linked worktree = %q, want %q", got, dir)
		}

		// The main checkout itself is not a worktree, and sees the linked one.
		main := Describe(dir)
		if main.IsWorktree || len(main.Worktrees) != 2 {
			t.Fatalf("main checkout described as %+v", main)
		}
	})
}

// Opening a worktree needs `git worktree add`, which the built-in
// implementation does not have: it says so instead of half-building one.
func TestEnsureWorktreeWithoutGitBinary(t *testing.T) {
	dir := initRepo(t)
	hideGitBinary(t)
	path, created, err := EnsureWorktree(dir, "feature/login")
	if !errors.Is(err, ErrNeedsGitBinary) {
		t.Fatalf("EnsureWorktree = %q, %t, %v; want ErrNeedsGitBinary", path, created, err)
	}
	if _, statErr := os.Stat(WorktreesRoot(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("the worktrees folder was created anyway: %v", statErr)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func mustNotExist(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(name))); !os.IsNotExist(err) {
		t.Fatalf("%s should be gone, stat = %v", name, err)
	}
}

// Discarding everything puts the working copy back at HEAD: edits and
// deletions are restored, the index forgets what was staged, and the files HEAD
// does not hold are deleted - except the ones git ignores, which were never
// part of the change set.
func TestDiscardEverything(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{
			".gitignore": "*.log\n",
			"keep.txt":   "same\n",
			"edit.txt":   "old\n",
			"staged.txt": "old staged\n",
			"remove.txt": "bye\n",
		})
		writeFile(t, dir, "edit.txt", "new\n")
		writeFile(t, dir, "staged.txt", "new staged\n")
		mustGit(t, dir, "add", "staged.txt")
		if err := os.Remove(filepath.Join(dir, "remove.txt")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, "added.txt", "added to the index\n")
		mustGit(t, dir, "add", "added.txt")
		writeFile(t, dir, "fresh/deep/new.txt", "untracked\n")
		writeFile(t, dir, "debug.log", "ignored\n")

		if err := Discard(dir, nil); err != nil {
			t.Fatalf("Discard: %v", err)
		}

		if got := readFile(t, dir, "edit.txt"); got != "old\n" {
			t.Fatalf("edit.txt = %q, want HEAD's content", got)
		}
		if got := readFile(t, dir, "staged.txt"); got != "old staged\n" {
			t.Fatalf("staged.txt = %q, want HEAD's content", got)
		}
		if got := readFile(t, dir, "remove.txt"); got != "bye\n" {
			t.Fatalf("remove.txt = %q, want it restored", got)
		}
		mustNotExist(t, dir, "added.txt")
		mustNotExist(t, dir, "fresh/deep/new.txt")
		// The folders that only held the deleted file go with it.
		mustNotExist(t, dir, "fresh")
		if got := readFile(t, dir, "debug.log"); got != "ignored\n" {
			t.Fatalf("an ignored file was touched: %q", got)
		}
		if status := mustGit(t, dir, "status", "--porcelain"); status != "" {
			t.Fatalf("working copy not clean after discarding everything:\n%s", status)
		}
		changes, skipped, err := WorktreeChanges(dir)
		if err != nil || len(changes) != 0 || skipped != 0 {
			t.Fatalf("change set after discard = %+v, %d, %v", changes, skipped, err)
		}
	})
}

// Naming paths discards exactly those, and leaves every other change alone.
func TestDiscardOnlyTheNamedPaths(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"a.txt": "a1\n", "b.txt": "b1\n"})
		writeFile(t, dir, "a.txt", "a2\n")
		writeFile(t, dir, "b.txt", "b2\n")
		writeFile(t, dir, "new.txt", "new\n")
		writeFile(t, dir, "other.txt", "other\n")

		if err := Discard(dir, []string{"a.txt", "new.txt"}); err != nil {
			t.Fatalf("Discard: %v", err)
		}
		if got := readFile(t, dir, "a.txt"); got != "a1\n" {
			t.Fatalf("a.txt = %q, want restored", got)
		}
		mustNotExist(t, dir, "new.txt")
		if got := readFile(t, dir, "b.txt"); got != "b2\n" {
			t.Fatalf("b.txt = %q, it was not named and must keep its edit", got)
		}
		if got := readFile(t, dir, "other.txt"); got != "other\n" {
			t.Fatalf("other.txt = %q, it was not named and must stay", got)
		}
	})
}

// The paths come from a request. One that is not in the change set - unchanged,
// ignored, outside the folder, missing - is refused, and the refusal touches
// nothing, not even the paths of the same call that were fine.
func TestDiscardRefusesPathsOutsideTheChangeSet(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{
			".gitignore": "*.log\n",
			"keep.txt":   "same\n",
			"edit.txt":   "old\n",
		})
		writeFile(t, dir, "edit.txt", "new\n")
		writeFile(t, dir, "debug.log", "ignored\n")
		outside := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-outside.txt")
		if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(outside) })

		for _, paths := range [][]string{
			{"keep.txt"},
			{"debug.log"},
			{"../" + filepath.Base(outside)},
			{"missing.txt"},
			{""},
			{"edit.txt", "keep.txt"},
		} {
			if err := Discard(dir, paths); !errors.Is(err, ErrNotChanged) {
				t.Fatalf("Discard(%q) = %v, want ErrNotChanged", paths, err)
			}
		}
		if got := readFile(t, dir, "edit.txt"); got != "new\n" {
			t.Fatalf("a refused call touched edit.txt: %q", got)
		}
		if got := readFile(t, dir, "debug.log"); got != "ignored\n" {
			t.Fatalf("a refused call touched debug.log: %q", got)
		}
		if b, err := os.ReadFile(outside); err != nil || string(b) != "outside\n" {
			t.Fatalf("a refused call touched a file outside the folder: %q, %v", b, err)
		}
	})
}

// A session that runs in a folder of a repository discards what that folder
// holds, by paths relative to it, and nothing above it.
func TestDiscardWithinASubdirectory(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"sub/a.txt": "old\n", "sub/b.txt": "old\n", "outside.txt": "old\n"})
		writeFile(t, dir, "sub/a.txt", "new\n")
		writeFile(t, dir, "sub/b.txt", "new\n")
		writeFile(t, dir, "outside.txt", "new\n")
		sub := filepath.Join(dir, "sub")

		if err := Discard(sub, []string{"a.txt"}); err != nil {
			t.Fatalf("Discard(a.txt): %v", err)
		}
		if got := readFile(t, sub, "a.txt"); got != "old\n" {
			t.Fatalf("sub/a.txt = %q, want restored", got)
		}
		if got := readFile(t, sub, "b.txt"); got != "new\n" {
			t.Fatalf("sub/b.txt = %q, it was not named", got)
		}
		if err := Discard(sub, nil); err != nil {
			t.Fatalf("Discard(all): %v", err)
		}
		if got := readFile(t, sub, "b.txt"); got != "old\n" {
			t.Fatalf("sub/b.txt = %q, want restored", got)
		}
		if got := readFile(t, dir, "outside.txt"); got != "new\n" {
			t.Fatalf("outside.txt = %q, it lies above the folder and must keep its edit", got)
		}
	})
}

func TestDiscardOutsideARepository(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "a.txt", "a\n")
		if err := Discard(dir, nil); !errors.Is(err, ErrNotRepository) {
			t.Fatalf("Discard in a plain folder = %v, want ErrNotRepository", err)
		}
		if got := readFile(t, dir, "a.txt"); got != "a\n" {
			t.Fatalf("a plain folder was touched: %q", got)
		}
	})
}

// git reports a staged rename under its new path; discarding it brings the old
// file back and removes the new one.
func TestDiscardRename(t *testing.T) {
	dir := commitRepo(t, map[string]string{"old-name.txt": "body\n"})
	mustGit(t, dir, "mv", "old-name.txt", "new-name.txt")

	if err := Discard(dir, []string{"new-name.txt"}); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if got := readFile(t, dir, "old-name.txt"); got != "body\n" {
		t.Fatalf("old-name.txt = %q, want restored", got)
	}
	mustNotExist(t, dir, "new-name.txt")
	if status := mustGit(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("working copy not clean:\n%s", status)
	}
}

// A file the change set lists with backslashes on Windows is named with
// slashes by a browser; both spellings name the same file.
func TestDiscardAcceptsSlashPaths(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"deep/a.txt": "old\n"})
		writeFile(t, dir, "deep/a.txt", "new\n")
		if err := Discard(dir, []string{"deep/a.txt"}); err != nil {
			t.Fatalf("Discard: %v", err)
		}
		if got := readFile(t, dir, "deep/a.txt"); !strings.HasPrefix(got, "old") {
			t.Fatalf("deep/a.txt = %q, want restored", got)
		}
	})
}

// git treats a repository nested in the folder as one opaque entry: it is
// never read file by file, and a discard leaves everything in it alone.
func TestANestedRepositoryIsOneEntry(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"a.txt": "a\n"})
		nested := filepath.Join(dir, "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		mustGit(t, nested, "init", "-b", "main")
		writeFile(t, nested, "inner.txt", "inner\n")
		mustGit(t, nested, "add", "-A")
		mustGit(t, nested, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "inner")
		writeFile(t, dir, "loose.txt", "loose\n")

		changes, skipped, err := WorktreeChanges(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range changes {
			if strings.HasPrefix(filepath.ToSlash(c.Path), "nested/") {
				t.Fatalf("a file of the nested repository was listed: %+v", c)
			}
		}
		if len(changes) != 1 || skipped != 1 {
			t.Fatalf("changes = %+v, skipped = %d; want loose.txt and the nested repository skipped", changes, skipped)
		}
		if err := Discard(dir, nil); err != nil {
			t.Fatalf("Discard: %v", err)
		}
		mustNotExist(t, dir, "loose.txt")
		if got := readFile(t, nested, "inner.txt"); got != "inner\n" {
			t.Fatalf("the nested repository was touched: %q", got)
		}
		if _, err := os.Stat(filepath.Join(nested, ".git", "HEAD")); err != nil {
			t.Fatalf("the nested repository lost its git dir: %v", err)
		}
	})
}

// Like git, a clone refuses a destination that already holds something, and a
// refused or failed clone never removes what was there.
func TestCloneRefusesANonEmptyDestination(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		src := commitRepo(t, map[string]string{"SKILL.md": "x\n"})
		dest := t.TempDir()
		writeFile(t, dest, "keep.txt", "precious\n")
		if err := Clone(src, "", dest); err == nil {
			t.Fatal("a clone into a non-empty folder must fail")
		}
		if got := readFile(t, dest, "keep.txt"); got != "precious\n" {
			t.Fatalf("the destination's content was touched: %q", got)
		}
		// A failed clone into an empty folder leaves the folder in place.
		empty := t.TempDir()
		if err := Clone(filepath.Join(t.TempDir(), "missing"), "", empty); err == nil {
			t.Fatal("cloning a missing repository must fail")
		}
		if _, err := os.Stat(empty); err != nil {
			t.Fatalf("the empty destination was removed: %v", err)
		}
	})
}

// A repository with no commit yet still has edits: files added to the index
// and new files. They are listed as additions and a discard deletes them.
func TestAnUnbornRepository(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := t.TempDir()
		mustGit(t, dir, "init", "-q", "-b", "main")
		writeFile(t, dir, "staged.txt", "staged\n")
		mustGit(t, dir, "add", "staged.txt")
		writeFile(t, dir, "loose.txt", "loose\n")
		dir = normPath(t, dir)

		changes, _, err := WorktreeChanges(dir)
		if err != nil {
			t.Fatal(err)
		}
		got := byPath(changes)
		if len(got) != 2 || got["staged.txt"].Status != "added" || got["loose.txt"].Status != "added" ||
			string(got["staged.txt"].After) != "staged\n" {
			t.Fatalf("changes of an unborn repository = %+v", changes)
		}
		if c, err := WorktreeChangeFor(dir, "staged.txt"); err != nil || c == nil || c.Status != "added" {
			t.Fatalf("WorktreeChangeFor(staged.txt) = %+v, %v", c, err)
		}

		if err := Discard(dir, []string{"staged.txt"}); err != nil {
			t.Fatalf("Discard(staged.txt): %v", err)
		}
		mustNotExist(t, dir, "staged.txt")
		if got := readFile(t, dir, "loose.txt"); got != "loose\n" {
			t.Fatalf("loose.txt was not named: %q", got)
		}
		if err := Discard(dir, nil); err != nil {
			t.Fatalf("Discard(all): %v", err)
		}
		mustNotExist(t, dir, "loose.txt")
		if status := mustGit(t, dir, "status", "--porcelain"); status != "" {
			t.Fatalf("an unborn repository not clean after discarding everything:\n%s", status)
		}
	})
}

// Discarding everything leaves nothing staged either, even where the index
// differs from HEAD while the file on disk does not: a staged edit written back,
// a staged new file deleted since.
func TestDiscardEverythingClearsTheIndex(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := commitRepo(t, map[string]string{"a.txt": "old\n", "b.txt": "b\n"})
		writeFile(t, dir, "a.txt", "new\n")
		mustGit(t, dir, "add", "a.txt")
		writeFile(t, dir, "a.txt", "old\n")
		writeFile(t, dir, "gone.txt", "staged then deleted\n")
		mustGit(t, dir, "add", "gone.txt")
		if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, "b.txt", "B\n")

		if err := Discard(dir, nil); err != nil {
			t.Fatalf("Discard: %v", err)
		}
		if status := mustGit(t, dir, "status", "--porcelain"); status != "" {
			t.Fatalf("the index kept changes after discarding everything:\n%s", status)
		}
		mustNotExist(t, dir, "gone.txt")
	})
}

// The files git ignores through the user's own configuration - the file
// core.excludesFile names, or the XDG default when it names none - are no
// edits either, and a discard never deletes them.
func TestGlobalExcludesAreHonoured(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		xdg := filepath.Join(home, "xdg")
		t.Setenv("XDG_CONFIG_HOME", xdg)
		named := filepath.Join(home, "global-ignore")
		writeFile(t, home, "global-ignore", "*.iml\n")
		writeFile(t, home, ".gitconfig", "[core]\n\texcludesfile = "+filepath.ToSlash(named)+"\n")

		dir := commitRepo(t, map[string]string{"a.txt": "a\n"})
		writeFile(t, dir, "project.iml", "ide settings\n")
		writeFile(t, dir, "loose.txt", "loose\n")

		changes, _, err := WorktreeChanges(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := byPath(changes)["project.iml"]; ok {
			t.Fatalf("a file the user's excludes file ignores was listed: %+v", changes)
		}
		if err := Discard(dir, nil); err != nil {
			t.Fatalf("Discard: %v", err)
		}
		if got := readFile(t, dir, "project.iml"); got != "ide settings\n" {
			t.Fatalf("a discard deleted a file git ignores: %q", got)
		}
		mustNotExist(t, dir, "loose.txt")

		// Without core.excludesFile, git reads $XDG_CONFIG_HOME/git/ignore.
		writeFile(t, home, ".gitconfig", "[user]\n\tname = coddy\n")
		writeFile(t, xdg, "git/ignore", "*.log\n")
		writeFile(t, dir, "debug.log", "noise\n")
		changes, _, err = WorktreeChanges(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := byPath(changes)["debug.log"]; ok {
			t.Fatalf("a file the XDG ignore file covers was listed: %+v", changes)
		}
	})
}
