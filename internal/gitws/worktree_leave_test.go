package gitws

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func commitFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", name)
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "add "+name)
	return mustGit(t, dir, "rev-parse", "HEAD")
}

func openedWorktree(t *testing.T, dir, branch string) LinkedWorktree {
	t.Helper()
	path, _, err := EnsureWorktree(dir, branch)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := WorktreeOf(path)
	if err != nil {
		t.Fatalf("WorktreeOf(%s): %v", path, err)
	}
	return wt
}

func TestWorktreeOfNamesTheWorktreeAndItsMainCheckout(t *testing.T) {
	dir := initRepo(t)
	path, _, err := EnsureWorktree(dir, "feature/login")
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(path, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// From a folder inside the worktree too: the session may have gone down.
	for _, from := range []string{path, sub} {
		wt, err := WorktreeOf(from)
		if err != nil {
			t.Fatalf("WorktreeOf(%s): %v", from, err)
		}
		if !samePath(wt.Path, path) || !samePath(wt.MainCheckout, dir) || wt.Branch != "feature/login" || !wt.Managed {
			t.Fatalf("WorktreeOf(%s) = %+v, want %s on feature/login, main %s, managed", from, wt, path, dir)
		}
	}
}

func TestWorktreeOfTheMainCheckoutIsNotALinkedWorktree(t *testing.T) {
	dir := initRepo(t)
	for _, from := range []string{dir, t.TempDir()} {
		if _, err := WorktreeOf(from); !errors.Is(err, ErrNotLinkedWorktree) {
			t.Fatalf("WorktreeOf(%s) error = %v, want ErrNotLinkedWorktree", from, err)
		}
	}
}

func TestWorktreeOfAWorktreeOutsideTheManagedRootIsNotManaged(t *testing.T) {
	dir := initRepo(t)
	elsewhere := filepath.Join(t.TempDir(), "mine")
	mustGit(t, dir, "worktree", "add", "-q", "-b", "feature/mine", elsewhere)
	wt, err := WorktreeOf(elsewhere)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Managed {
		t.Fatalf("a worktree the operator added at %s is reported as managed", elsewhere)
	}
	if err := CheckRemovable(wt, true); err == nil || !strings.Contains(err.Error(), "not a worktree Coddy opened") {
		t.Fatalf("CheckRemovable = %v, want a refusal of a worktree Coddy did not open", err)
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Fatalf("the operator's worktree is gone: %v", err)
	}
}

func TestRemoveWorktreeRefusesUncommittedChanges(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/dirty")
	if err := os.WriteFile(filepath.Join(wt.Path, "draft.txt"), []byte("half done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveWorktree(wt, false); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("RemoveWorktree = %v, want a refusal naming the uncommitted changes", err)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "draft.txt")); err != nil {
		t.Fatalf("the uncommitted file is gone after a refusal: %v", err)
	}
}

func TestRemoveWorktreeDiscardsUncommittedChangesWhenAsked(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/scratch")
	if err := os.WriteFile(filepath.Join(wt.Path, "draft.txt"), []byte("throw away\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveWorktree(wt, true); err != nil {
		t.Fatalf("RemoveWorktree(discard) = %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree %s still there: %v", wt.Path, err)
	}
	if strings.Contains(mustGit(t, dir, "worktree", "list"), wt.Path) {
		t.Fatalf("git still lists %s", wt.Path)
	}
}

// Removing a worktree never loses a commit: a branch git does not count as
// merged stays, and the answer says so.
func TestRemoveWorktreeKeepsABranchWithUnmergedCommits(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/work")
	commit := commitFile(t, wt.Path, "work.txt", "done\n")
	removal, err := RemoveWorktree(wt, true)
	if err != nil {
		t.Fatal(err)
	}
	if removal.BranchDeleted || removal.BranchKept == "" {
		t.Fatalf("removal = %+v, want the branch kept with a reason", removal)
	}
	if got := mustGit(t, dir, "rev-parse", "feature/work"); got != commit {
		t.Fatalf("feature/work = %s, want its commit %s", got, commit)
	}
}

func TestRemoveWorktreeDeletesAMergedBranch(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/nothing")
	removal, err := RemoveWorktree(wt, false)
	if err != nil {
		t.Fatal(err)
	}
	if !removal.BranchDeleted {
		t.Fatalf("removal = %+v, want the merged branch deleted", removal)
	}
	if refs := mustGit(t, dir, "branch", "--list", "feature/nothing"); refs != "" {
		t.Fatalf("branch still listed: %q", refs)
	}
}

func TestEnsureWorktreeFromHeadStartsAtTheCallersCommit(t *testing.T) {
	dir := initRepo(t)
	base := openedWorktree(t, dir, "feature/base")
	head := commitFile(t, base.Path, "base.txt", "first part\n")

	path, created, err := EnsureWorktreeFrom(base.Path, "feature/next", BaseHead)
	if err != nil || !created {
		t.Fatalf("EnsureWorktreeFrom = %q, %t, %v", path, created, err)
	}
	if got := mustGit(t, path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("feature/next starts at %s, want feature/base's %s", got, head)
	}
	// The new tree still belongs to the main checkout, not to the worktree
	// it was opened from.
	if !isInside(WorktreesRoot(dir), path) {
		t.Fatalf("worktree %s is not under %s", path, WorktreesRoot(dir))
	}
}

func TestEnsureWorktreeFromRefusesAnUnknownBase(t *testing.T) {
	dir := initRepo(t)
	if _, _, err := EnsureWorktreeFrom(dir, "feature/x", WorktreeBase("tip")); err == nil || !strings.Contains(err.Error(), "tip") {
		t.Fatalf("EnsureWorktreeFrom(tip) = %v, want a refusal naming the base", err)
	}
}

// A worktree whose HEAD is on commits no branch holds - a detached HEAD with
// work of its own - is never removed, discard or not: those commits would be
// unreachable once the worktree is gone.
func TestRemoveWorktreeRefusesCommitsNoBranchHolds(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/detached")
	mustGit(t, wt.Path, "checkout", "-q", "--detach")
	orphan := commitFile(t, wt.Path, "loose.txt", "on no branch\n")
	wt.Branch = ""
	for _, discard := range []bool{false, true} {
		if _, err := RemoveWorktree(wt, discard); err == nil || !strings.Contains(err.Error(), "no branch, remote branch or tag holds") {
			t.Fatalf("RemoveWorktree(discard=%t) = %v, want a refusal naming the loose commits", discard, err)
		}
	}
	if got := mustGit(t, wt.Path, "rev-parse", "HEAD"); got != orphan {
		t.Fatalf("HEAD = %s, want the loose commit %s still there", got, orphan)
	}
}

func TestRemoveWorktreeRefusesALockedWorktree(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/locked")
	mustGit(t, dir, "worktree", "lock", wt.Path)
	if _, err := RemoveWorktree(wt, true); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("RemoveWorktree of a locked worktree = %v, want a refusal", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("the locked worktree is gone: %v", err)
	}
}

// A worktree with initialised submodules keeps their own commits in its git
// directory, and a forced removal takes them along: it is refused, discard or
// not.
func TestRemoveWorktreeRefusesInitialisedSubmodules(t *testing.T) {
	dir := initRepo(t)
	lib := t.TempDir()
	mustGit(t, lib, "init", "-q", "-b", "main")
	commitFile(t, lib, "lib.txt", "library\n")
	wt := openedWorktree(t, dir, "feature/sub")
	mustGit(t, wt.Path, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
	mustGit(t, wt.Path, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-q", "-m", "add lib")
	for _, discard := range []bool{false, true} {
		if _, err := RemoveWorktree(wt, discard); err == nil || !strings.Contains(err.Error(), "submodules") {
			t.Fatalf("RemoveWorktree(discard=%t) = %v, want a refusal naming the submodules", discard, err)
		}
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("the worktree is gone: %v", err)
	}
}

// A repository that hides untracked files from git status
// (status.showUntrackedFiles=no) still has them counted: a removal without
// discard would delete them.
func TestRemoveWorktreeSeesUntrackedFilesGitStatusHides(t *testing.T) {
	dir := initRepo(t)
	mustGit(t, dir, "config", "status.showUntrackedFiles", "no")
	wt := openedWorktree(t, dir, "feature/hidden")
	if err := os.WriteFile(filepath.Join(wt.Path, "draft.txt"), []byte("only here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveWorktree(wt, false); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("RemoveWorktree = %v, want a refusal naming the uncommitted file", err)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "draft.txt")); err != nil {
		t.Fatalf("the untracked file is gone: %v", err)
	}
}

// Discarding is not losing: before a worktree with uncommitted work goes, its
// index and its files are saved the way git stash saves them, under
// refs/coddy/worktree-snapshots/, and `git stash apply --index` brings back
// exactly what was there: a file staged and then edited again (both
// versions), a file added with --force past .gitignore, a changed file and an
// untracked one.
func TestRemoveWorktreeKeepsDiscardedWorkAsASnapshot(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/snap")
	commitFile(t, wt.Path, ".gitignore", "build/\n")
	commitFile(t, wt.Path, "kept.txt", "committed\n")
	commitFile(t, wt.Path, "twice.txt", "committed\n")
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(wt.Path, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("kept.txt", "changed, not committed\n")
	write("twice.txt", "staged\n")
	mustGit(t, wt.Path, "add", "twice.txt")
	write("twice.txt", "edited after staging\n")
	write("build/out.bin", "forced past .gitignore\n")
	mustGit(t, wt.Path, "add", "-f", "build/out.bin")
	write("new.txt", "never added\n")
	before := mustGit(t, wt.Path, "status", "--porcelain")

	removal, err := RemoveWorktree(wt, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(removal.Snapshot, SnapshotRefPrefix) {
		t.Fatalf("removal = %+v, want a snapshot ref under %s", removal, SnapshotRefPrefix)
	}
	// The branch stayed (it has commits of its own); the snapshot brings the
	// work back onto it, index and files alike.
	back := filepath.Join(t.TempDir(), "back")
	mustGit(t, dir, "worktree", "add", "-q", back, "feature/snap")
	mustGit(t, back, "stash", "apply", "--index", removal.Snapshot)
	if after := mustGit(t, back, "status", "--porcelain"); after != before {
		t.Fatalf("status after restoring:\n%s\nwant:\n%s", after, before)
	}
	if staged := mustGit(t, back, "show", ":twice.txt"); staged != "staged" {
		t.Fatalf("the staged version of twice.txt = %q, want %q", staged, "staged")
	}
	for name, want := range map[string]string{
		"kept.txt": "changed, not committed\n", "twice.txt": "edited after staging\n",
		"build/out.bin": "forced past .gitignore\n", "new.txt": "never added\n",
	} {
		got, err := os.ReadFile(filepath.Join(back, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Fatalf("%s after restoring = %q, %v; want %q", name, got, err, want)
		}
	}
}

// A snapshot never replaces an earlier one, even one taken in the same second
// of a worktree with the same name.
func TestSnapshotsOfTheSameWorktreeInOneSecondAreKeptApart(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/twice")
	now := time.Now()
	var refs []string
	for _, body := range []string{"first\n", "second\n"} {
		if err := os.WriteFile(filepath.Join(wt.Path, "draft.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ref, err := snapshotWorktree(wt, now)
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}
	if refs[0] == refs[1] {
		t.Fatalf("both snapshots are %s", refs[0])
	}
	for i, want := range []string{"first", "second"} {
		if got := mustGit(t, dir, "show", refs[i]+":draft.txt"); got != want {
			t.Fatalf("%s holds %q, want %q", refs[i], got, want)
		}
	}
}

// A git repository of its own inside the worktree would be kept by a
// snapshot as a bare pointer, its commits and files lost with the worktree:
// the removal is refused, discard or not, before anything moves.
func TestRemoveWorktreeRefusesANestedRepository(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/nested")
	nested := filepath.Join(wt.Path, "dep")
	mustGit(t, wt.Path, "init", "-q", nested)
	commitFile(t, nested, "lib.txt", "only here\n")
	for _, discard := range []bool{false, true} {
		if err := CheckRemovable(wt, discard); err == nil || !strings.Contains(err.Error(), "dep") {
			t.Fatalf("CheckRemovable(discard=%t) = %v, want a refusal naming dep", discard, err)
		}
	}
	if st, err := InspectWorktree(wt); err != nil || len(st.Nested) != 1 || st.Nested[0] != "dep" {
		t.Fatalf("InspectWorktree = %+v, %v; want dep among the nested repositories", st, err)
	}
	if _, err := RemoveWorktree(wt, true); err == nil {
		t.Fatal("RemoveWorktree(discard) removed a worktree holding a repository of its own")
	}
	if _, err := os.Stat(filepath.Join(nested, "lib.txt")); err != nil {
		t.Fatalf("the nested repository is gone: %v", err)
	}
}

// git status folds an untracked folder into one line, so a repository deeper
// inside one is found only when the snapshot is built: it refuses then, and
// the worktree stays.
func TestSnapshotRefusesARepositoryInsideAnUntrackedFolder(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/vendor")
	nested := filepath.Join(wt.Path, "vendor", "dep")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, wt.Path, "init", "-q", nested)
	commitFile(t, nested, "lib.txt", "only here\n")
	if _, err := RemoveWorktree(wt, true); err == nil || !strings.Contains(err.Error(), "vendor/dep") {
		t.Fatalf("RemoveWorktree(discard) = %v, want a refusal naming vendor/dep", err)
	}
	if _, err := os.Stat(filepath.Join(nested, "lib.txt")); err != nil {
		t.Fatalf("the nested repository is gone: %v", err)
	}
	if refs := mustGit(t, dir, "for-each-ref", SnapshotRefPrefix); refs != "" {
		t.Fatalf("a refused snapshot left refs: %q", refs)
	}
}

// Only what a snapshot keeps is forced away: a worktree with nothing
// uncommitted but ignored files (build output) goes without --force.
func TestRemoveWorktreeWithOnlyIgnoredFilesNeedsNoForce(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/built")
	commitFile(t, wt.Path, ".gitignore", "build/\n")
	if err := os.MkdirAll(filepath.Join(wt.Path, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "build", "out.bin"), []byte("output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	removal, err := RemoveWorktree(wt, true)
	if err != nil {
		t.Fatal(err)
	}
	if removal.Snapshot != "" {
		t.Fatalf("snapshot %q of ignored files only", removal.Snapshot)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree %s still there: %v", wt.Path, err)
	}
}

// A clean worktree leaves no snapshot behind.
func TestRemoveWorktreeOfCleanWorkMakesNoSnapshot(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/clean")
	removal, err := RemoveWorktree(wt, true)
	if err != nil {
		t.Fatal(err)
	}
	if removal.Snapshot != "" {
		t.Fatalf("snapshot %q of a clean worktree", removal.Snapshot)
	}
	if refs := mustGit(t, dir, "for-each-ref", SnapshotRefPrefix); refs != "" {
		t.Fatalf("snapshot refs left: %q", refs)
	}
}

func TestInspectWorktreeCountsWhatRemovalWouldTouch(t *testing.T) {
	dir := initRepo(t)
	wt := openedWorktree(t, dir, "feature/look")
	commitFile(t, wt.Path, "a.txt", "one\n")
	commitFile(t, wt.Path, "b.txt", "two\n")
	if err := os.WriteFile(filepath.Join(wt.Path, "c.txt"), []byte("three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := InspectWorktree(wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Uncommitted != 1 || st.Unpushed != 2 || st.LooseCommit != "" || st.Locked || st.Submodules {
		t.Fatalf("state = %+v, want 1 uncommitted, 2 unpushed and nothing else", st)
	}
}
