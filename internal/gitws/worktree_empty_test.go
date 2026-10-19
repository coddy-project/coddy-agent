package gitws

import (
	"os"
	"path/filepath"
	"testing"
)

// A worktree holds nothing of its own while it has no uncommitted change and
// its branch carries no commit the main checkout, its upstream or origin's
// default branch does not already have: leaving it, nothing is lost by
// removing it.
func TestHoldsNothingFollowsWhatTheWorktreeHasOfItsOwn(t *testing.T) {
	main := initRepo(t)
	commitFile(t, main, ".gitignore", "build/\n")
	mustGit(t, main, "push", "-q", "origin", "main")
	wt := openedWorktree(t, main, "feature/empty")
	holds := func(want bool, why string) {
		t.Helper()
		got, err := HoldsNothing(wt)
		if err != nil {
			t.Fatalf("%s: %v", why, err)
		}
		if got != want {
			t.Fatalf("%s: HoldsNothing = %t, want %t", why, got, want)
		}
	}
	holds(true, "a fresh worktree")
	writeFile(t, wt.Path, "build/out.bin", "ignored output\n")
	holds(true, "only ignored files")
	writeFile(t, wt.Path, "draft.txt", "untracked\n")
	holds(false, "an untracked file")
	if err := os.Remove(filepath.Join(wt.Path, "draft.txt")); err != nil {
		t.Fatal(err)
	}
	commitFile(t, wt.Path, "work.txt", "a commit of its own\n")
	holds(false, "a commit of its own")
	mustGit(t, main, "merge", "-q", "--ff-only", "feature/empty")
	holds(true, "its commit merged into the main checkout")
}

// A worktree on a detached HEAD holds nothing while a branch, a remote branch
// or a tag holds its commit, and something once it carries a commit of its
// own.
func TestHoldsNothingOnADetachedHead(t *testing.T) {
	main := initRepo(t)
	path := filepath.Join(WorktreesRoot(main), "detached")
	mustGit(t, main, "worktree", "add", "-q", "--detach", path, "HEAD")
	wt, err := WorktreeOf(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := HoldsNothing(wt); err != nil || !got {
		t.Fatalf("HoldsNothing(detached at main) = %t, %v; want true", got, err)
	}
	commitFile(t, path, "loose.txt", "on no branch\n")
	if got, err := HoldsNothing(wt); err != nil || got {
		t.Fatalf("HoldsNothing(a loose commit) = %t, %v; want false", got, err)
	}
}

// A branch goes with its worktree when nothing is lost: every commit of it
// another branch holds, here the branch it was stacked on.
func TestRemoveWorktreeDeletesABranchAnotherBranchHolds(t *testing.T) {
	main := initRepo(t)
	parent := openedWorktree(t, main, "feature/parent")
	held := commitFile(t, parent.Path, "parent.txt", "the parent's work\n")
	path, _, err := EnsureWorktreeFrom(parent.Path, "feature/child", BaseHead)
	if err != nil {
		t.Fatal(err)
	}
	child, err := WorktreeOf(path)
	if err != nil {
		t.Fatal(err)
	}
	removal, err := RemoveWorktree(child, false)
	if err != nil {
		t.Fatal(err)
	}
	if !removal.BranchDeleted || removal.BranchKept != "" {
		t.Fatalf("removal = %+v, want feature/child deleted: feature/parent holds its commit", removal)
	}
	if got := mustGit(t, main, "rev-parse", "feature/parent"); got != held {
		t.Fatalf("feature/parent = %s, want %s untouched", got, held)
	}
}
