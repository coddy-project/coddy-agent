package gitws

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotLinkedWorktree answers a request to leave a worktree from a folder
// that is not in one: the main checkout, or a folder outside any repository.
var ErrNotLinkedWorktree = errors.New("not in a linked worktree")

// LinkedWorktree is the linked worktree a folder belongs to, and the main
// checkout of its repository.
type LinkedWorktree struct {
	// Path is the root of the worktree.
	Path string
	// Branch is the branch checked out there, empty on a detached HEAD.
	Branch string
	// MainCheckout is the root of the repository's main checkout.
	MainCheckout string
	// Managed reports whether the worktree sits under WorktreesRoot of the
	// main checkout: one Coddy opened, and the only kind it removes.
	Managed bool
}

// WorktreeOf finds the linked worktree that holds dir, which may be any
// folder inside it. The main checkout, a folder outside git and a submodule
// answer ErrNotLinkedWorktree.
func WorktreeOf(dir string) (LinkedWorktree, error) {
	if !GitAvailable() {
		return LinkedWorktree{}, fmt.Errorf("find the worktree of %s: %w", dir, ErrNeedsGitBinary)
	}
	top, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return LinkedWorktree{}, fmt.Errorf("%w: %s is not inside a git repository", ErrNotLinkedWorktree, dir)
	}
	list := listWorktrees(dir)
	if len(list) == 0 {
		return LinkedWorktree{}, fmt.Errorf("cannot list the worktrees of %s", top)
	}
	main := list[0].Path
	if samePath(top, main) {
		return LinkedWorktree{}, fmt.Errorf("%w: %s is the main checkout", ErrNotLinkedWorktree, main)
	}
	for _, wt := range list[1:] {
		if samePath(wt.Path, top) {
			return LinkedWorktree{
				Path:         wt.Path,
				Branch:       wt.Branch,
				MainCheckout: main,
				Managed:      isInside(WorktreesRoot(main), wt.Path),
			}, nil
		}
	}
	return LinkedWorktree{}, fmt.Errorf("%w: git does not list %s among the worktrees of %s", ErrNotLinkedWorktree, top, main)
}

// CheckRemovable says whether RemoveWorktree may remove wt, and why not. Only
// a worktree Coddy opened (one under WorktreesRoot), not one git keeps locked,
// and never one whose HEAD is on commits no branch, remote branch or tag holds
// (a detached HEAD with work of its own): those would be lost with it,
// whatever discard says. Without discard, nothing uncommitted and no
// initialised submodule either, which git removes only by force. It changes
// nothing, so a caller can ask before it moves anything.
func CheckRemovable(wt LinkedWorktree, discard bool) error {
	if !wt.Managed {
		return fmt.Errorf("%s is not a worktree Coddy opened (those are under %s); remove it with git worktree remove yourself",
			wt.Path, WorktreesRoot(wt.MainCheckout))
	}
	if gitDir, err := runGit(wt.Path, "rev-parse", "--absolute-git-dir"); err == nil {
		if _, err := os.Stat(filepath.Join(gitDir, "locked")); err == nil {
			return fmt.Errorf("the worktree %s is locked; unlock it first (git worktree unlock %s)", wt.Path, wt.Path)
		}
	}
	unheld, err := runGit(wt.Path, "rev-list", "-n", "1", "HEAD", "--not", "--branches", "--remotes", "--tags")
	if err != nil {
		return err
	}
	if unheld != "" {
		return fmt.Errorf("the worktree %s is on commits no branch holds (%s); create a branch for them first, or they are lost with the worktree",
			wt.Path, unheld)
	}
	if discard {
		return nil
	}
	status, err := runGit(wt.Path, "status", "--porcelain")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("the worktree %s has %d uncommitted change(s); commit them, or discard them to remove it anyway",
			wt.Path, len(strings.Split(status, "\n")))
	}
	if submodules, _ := runGit(wt.Path, "submodule", "status"); submodules != "" {
		for line := range strings.SplitSeq(submodules, "\n") {
			if line != "" && !strings.HasPrefix(line, "-") {
				return fmt.Errorf("the worktree %s has initialised submodules, which git removes only by force with their working trees; discard them to remove it anyway",
					wt.Path)
			}
		}
	}
	return nil
}

// Removal is what RemoveWorktree did besides removing the worktree.
type Removal struct {
	// BranchDeleted reports that the worktree's branch went with it.
	BranchDeleted bool
	// BranchKept says why the branch stayed, when it did.
	BranchKept string
}

// RemoveWorktree removes wt once CheckRemovable allows it, with its
// uncommitted changes when discard says so. Its branch goes too only when git
// counts it as merged (`git branch -d`: into the main checkout's HEAD or the
// branch's upstream), so no commit is lost: a branch with work of its own
// stays, and Removal.BranchKept says why. Run it from outside wt.
func RemoveWorktree(wt LinkedWorktree, discard bool) (Removal, error) {
	if err := CheckRemovable(wt, discard); err != nil {
		return Removal{}, err
	}
	args := []string{"worktree", "remove"}
	if discard {
		args = append(args, "--force")
	}
	args = append(args, "--", wt.Path)
	if _, err := runGit(wt.MainCheckout, args...); err != nil {
		return Removal{}, err
	}
	var removal Removal
	if wt.Branch == "" {
		return removal, nil
	}
	if _, err := runGit(wt.MainCheckout, "branch", "-d", wt.Branch); err != nil {
		removal.BranchKept = fmt.Sprintf("branch %s has commits that are not merged into the main checkout's HEAD or its upstream, so it stays (git branch -D %s deletes it)",
			wt.Branch, wt.Branch)
		return removal, nil
	}
	removal.BranchDeleted = true
	return removal, nil
}
