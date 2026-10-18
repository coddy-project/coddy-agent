package gitws

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// WorktreeState is what a worktree holds that removing it would touch.
type WorktreeState struct {
	// Uncommitted counts the changed, staged and untracked entries git
	// reports (ignored files are not counted).
	Uncommitted int
	// Unpushed counts the commits of HEAD no remote branch holds.
	Unpushed int
	// LooseCommit names the newest commit of HEAD no branch, remote branch or
	// tag holds (a detached HEAD with work of its own); empty when there is
	// none.
	LooseCommit string
	// Locked reports a `git worktree lock`.
	Locked bool
	// Submodules reports initialised submodules, which git removes only by
	// force, with their working trees.
	Submodules bool
}

// InspectWorktree reads what wt holds that removing it would touch. It
// changes nothing.
func InspectWorktree(wt LinkedWorktree) (WorktreeState, error) {
	var st WorktreeState
	if gitDir, err := runGit(wt.Path, "rev-parse", "--absolute-git-dir"); err == nil {
		if _, err := os.Stat(filepath.Join(gitDir, "locked")); err == nil {
			st.Locked = true
		}
	}
	loose, err := runGit(wt.Path, "rev-list", "-n", "1", "HEAD", "--not", "--branches", "--remotes", "--tags")
	if err != nil {
		return st, err
	}
	st.LooseCommit = loose
	if n, err := runGit(wt.Path, "rev-list", "--count", "HEAD", "--not", "--remotes"); err == nil {
		st.Unpushed, _ = strconv.Atoi(n)
	}
	// Spelled out: a repository with status.showUntrackedFiles=no would hide
	// the untracked files a removal deletes, and one ignoring submodules their
	// changes.
	status, err := runGit(wt.Path, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=none")
	if err != nil {
		return st, err
	}
	if status != "" {
		st.Uncommitted = len(strings.Split(status, "\n"))
	}
	if submodules, _ := runGit(wt.Path, "submodule", "status"); submodules != "" {
		for line := range strings.SplitSeq(submodules, "\n") {
			if line != "" && !strings.HasPrefix(line, "-") {
				st.Submodules = true
				break
			}
		}
	}
	return st, nil
}

// CheckRemovable says whether RemoveWorktree may remove wt, and why not. Only
// a worktree Coddy opened (one under WorktreesRoot), not one git keeps locked,
// and never one whose HEAD is on commits no branch, remote branch or tag holds
// (a detached HEAD with work of its own), nor one with initialised submodules,
// whose own commits live in the worktree's git directory: those would be lost
// with it, whatever discard says. Without discard, nothing uncommitted
// either. It changes nothing, so a caller can ask before it moves anything.
func CheckRemovable(wt LinkedWorktree, discard bool) error {
	if !wt.Managed {
		return fmt.Errorf("%s is not a worktree Coddy opened (those are under %s); remove it with git worktree remove yourself",
			wt.Path, WorktreesRoot(wt.MainCheckout))
	}
	st, err := InspectWorktree(wt)
	if err != nil {
		return err
	}
	switch {
	case st.Locked:
		return fmt.Errorf("the worktree %s is locked; unlock it first (git worktree unlock %s)", wt.Path, wt.Path)
	case st.LooseCommit != "":
		return fmt.Errorf("the worktree %s is on commits no branch, remote branch or tag holds (%s); create a branch for them first, or they are lost with the worktree",
			wt.Path, st.LooseCommit)
	case st.Submodules:
		return fmt.Errorf("the worktree %s has initialised submodules, whose own commits live in its git directory and would go with it; remove it by hand (git worktree remove --force) once they are pushed",
			wt.Path)
	case discard:
		return nil
	case st.Uncommitted > 0:
		return fmt.Errorf("the worktree %s has %d uncommitted change(s); commit them, or discard them to remove it anyway (they are kept as a snapshot)",
			wt.Path, st.Uncommitted)
	}
	return nil
}

// SnapshotRefPrefix is where Coddy keeps the uncommitted work of a worktree it
// removed with discard, one ref per removal.
const SnapshotRefPrefix = "refs/coddy/worktree-snapshots/"

// snapshotWorktree records what wt holds that HEAD does not - changed, staged
// and untracked files, .gitignore respected - as one commit on top of HEAD,
// under SnapshotRefPrefix, and returns the ref. The commit is built in an
// index of its own, so the worktree and its index stay as they are; it is
// authored by Coddy, since nobody wrote it as a commit. Bringing the work back
// is `git cherry-pick --no-commit <ref>` on the branch, or
// `git worktree add <folder> <ref>` for a tree of it.
func snapshotWorktree(wt LinkedWorktree, now time.Time) (string, error) {
	index, err := os.CreateTemp("", "coddy-snapshot-index-*")
	if err != nil {
		return "", err
	}
	indexPath := index.Name()
	_ = index.Close()
	_ = os.Remove(indexPath)
	defer func() { _ = os.Remove(indexPath) }()
	env := []string{
		"GIT_INDEX_FILE=" + indexPath,
		"GIT_AUTHOR_NAME=Coddy", "GIT_AUTHOR_EMAIL=coddy@localhost",
		"GIT_COMMITTER_NAME=Coddy", "GIT_COMMITTER_EMAIL=coddy@localhost",
	}
	if _, err := runGitEnv(wt.Path, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := runGitEnv(wt.Path, env, "add", "-A"); err != nil {
		return "", err
	}
	tree, err := runGitEnv(wt.Path, env, "write-tree")
	if err != nil {
		return "", err
	}
	name := BranchDirName(wt.Branch)
	if name == "" {
		name = filepath.Base(wt.Path)
	}
	message := fmt.Sprintf("Uncommitted work of the worktree %s (%s), saved by Coddy before removing it", wt.Path, branchOrHead(wt.Branch))
	commit, err := runGitEnv(wt.Path, env, "commit-tree", tree, "-p", "HEAD", "-m", message)
	if err != nil {
		return "", err
	}
	ref := SnapshotRefPrefix + name + "-" + now.UTC().Format("20060102T150405Z")
	if _, err := runGit(wt.Path, "update-ref", ref, commit); err != nil {
		return "", err
	}
	return ref, nil
}

func branchOrHead(branch string) string {
	if branch == "" {
		return "detached HEAD"
	}
	return "branch " + branch
}

// Removal is what RemoveWorktree did besides removing the worktree.
type Removal struct {
	// Snapshot is the ref that keeps the uncommitted work discard dropped,
	// empty when there was none.
	Snapshot string
	// BranchDeleted reports that the worktree's branch went with it.
	BranchDeleted bool
	// BranchKept says why the branch stayed, when it did.
	BranchKept string
}

// RemoveWorktree removes wt once CheckRemovable allows it. With discard its
// uncommitted work goes too, but it is first saved as a snapshot commit
// (Removal.Snapshot) that brings it back, and a removal whose snapshot fails
// does not happen. Its branch goes only when git counts it as merged
// (`git branch -d`: into the main checkout's HEAD or the branch's upstream), so
// no commit is lost: a branch with work of its own stays, and
// Removal.BranchKept says why. Run it from outside wt.
func RemoveWorktree(wt LinkedWorktree, discard bool) (Removal, error) {
	if err := CheckRemovable(wt, discard); err != nil {
		return Removal{}, err
	}
	var removal Removal
	if discard {
		st, err := InspectWorktree(wt)
		if err != nil {
			return Removal{}, err
		}
		if st.Uncommitted > 0 {
			ref, err := snapshotWorktree(wt, time.Now())
			if err != nil {
				return Removal{}, fmt.Errorf("save the uncommitted work of %s before removing it: %w", wt.Path, err)
			}
			removal.Snapshot = ref
		}
	}
	args := []string{"worktree", "remove"}
	if discard {
		args = append(args, "--force")
	}
	args = append(args, "--", wt.Path)
	if _, err := runGit(wt.MainCheckout, args...); err != nil {
		return removal, err
	}
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

// PruneWorktrees makes git forget the worktrees whose folders are gone, so
// a branch one of them held can be opened in a worktree again.
func PruneWorktrees(dir string) error {
	if !GitAvailable() {
		return nil
	}
	_, err := runGit(dir, "worktree", "prune")
	return err
}
