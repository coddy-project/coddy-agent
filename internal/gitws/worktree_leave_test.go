package gitws

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
