package gitws

import (
	"fmt"
	"strconv"
	"strings"
)

// OwnWork says what wt holds of its own that removing it would lose, or ""
// when it holds nothing: uncommitted changes (ignored files do not count,
// but a file the main checkout's .worktreeinclude names that differs from the
// main checkout's copy does: ChangedIncludes), commits of its branch or of a
// detached HEAD that no other branch, remote branch or tag holds, and what
// CheckRemovable keeps a worktree for - a lock, initialised submodules, a git
// repository of its own inside it. It changes nothing.
func OwnWork(wt LinkedWorktree) (string, error) {
	st, err := InspectWorktree(wt)
	if err != nil {
		return "", err
	}
	switch {
	case st.Uncommitted > 0:
		return fmt.Sprintf("%d uncommitted change(s)", st.Uncommitted), nil
	case st.LooseCommit != "":
		return fmt.Sprintf("commits no branch, remote branch or tag holds (%s)", st.LooseCommit), nil
	case st.Locked:
		return "a git worktree lock", nil
	case st.Submodules:
		return "initialised submodules", nil
	case len(st.Nested) > 0:
		return "git repositories of its own (" + strings.Join(st.Nested, ", ") + ")", nil
	}
	changed, err := ChangedIncludes(wt.MainCheckout, wt.Path)
	if err != nil {
		return "", err
	}
	if len(changed) > 0 {
		return fmt.Sprintf("files %s names that differ from the main checkout's (%s)", WorktreeIncludeFile, strings.Join(changed, ", ")), nil
	}
	if wt.Branch == "" {
		return "", nil
	}
	n, err := commitsOfItsOwn(wt.Path, wt.Branch, "HEAD")
	if err != nil {
		return "", err
	}
	if n > 0 {
		return fmt.Sprintf("%d commit(s) of branch %s that no other branch, remote branch or tag holds", n, wt.Branch), nil
	}
	return "", nil
}

// HoldsNothing reports whether removing wt, and its branch, would lose
// nothing (OwnWork has nothing to say).
func HoldsNothing(wt LinkedWorktree) (bool, error) {
	own, err := OwnWork(wt)
	return own == "" && err == nil, err
}

// commitsOfItsOwn counts the commits of rev that no branch other than branch,
// no remote branch and no tag holds: what deleting branch would lose.
func commitsOfItsOwn(dir, branch, rev string) (int, error) {
	// --exclude drops the branch itself from the --branches that follows; a
	// branch name holds none of the characters a glob would read.
	out, err := runGit(dir, "rev-list", "--count", rev, "--not", "--exclude="+branch, "--branches", "--remotes", "--tags")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// HeadOf names the commit HEAD of the checkout at dir is on.
func HeadOf(dir string) (string, error) {
	return runGit(dir, "rev-parse", "--verify", "-q", "HEAD^{commit}")
}

// DeleteBranchIfHeld deletes branch when every commit of it another branch,
// a remote branch or a tag holds, so nothing is lost, and otherwise says why
// it stays. A branch some worktree has checked out is refused by git.
func DeleteBranchIfHeld(dir, branch string) (deleted bool, kept string) {
	tip, err := runGit(dir, "rev-parse", "--verify", "-q", "refs/heads/"+branch)
	if err != nil || tip == "" {
		return false, ""
	}
	n, err := commitsOfItsOwn(dir, branch, tip)
	switch {
	case err != nil:
		return false, fmt.Sprintf("branch %s stays: %v", branch, err)
	case n > 0:
		return false, fmt.Sprintf("branch %s has %d commit(s) no other branch, remote branch or tag holds, so it stays (git branch -D %s deletes it)",
			branch, n, branch)
	}
	if _, err := runGit(dir, "branch", "-D", branch); err != nil {
		return false, fmt.Sprintf("branch %s stays: %v", branch, err)
	}
	return true, ""
}
