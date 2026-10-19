package gitws

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureLocalWorktree returns a worktree for branch under WorktreesRoot of
// the main checkout, creating it when missing, without asking any remote: a
// new branch starts at the commit HEAD of repoDir points at, an existing one
// is checked out at its own tip. It is what a subagent that works in a
// worktree of its own gets (subagents isolation), so it needs no origin and
// never refuses the default branch for being the default; a branch checked
// out in the main checkout is refused like everywhere else. A new worktree
// gets the .worktreeinclude files, as EnsureWorktreeDetailed's does.
func EnsureLocalWorktree(repoDir, branch string) (WorktreeEnsured, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.HasPrefix(branch, "-") {
		return WorktreeEnsured{}, fmt.Errorf("invalid branch name %q", branch)
	}
	if !GitAvailable() {
		return WorktreeEnsured{}, fmt.Errorf("open a worktree for %q: %w", branch, ErrNeedsGitBinary)
	}
	if _, err := runGit(repoDir, "check-ref-format", "--branch", branch); err != nil {
		return WorktreeEnsured{}, fmt.Errorf("invalid branch name %q: %w", branch, err)
	}
	dirName := BranchDirName(branch)
	if dirName == "" {
		return WorktreeEnsured{}, fmt.Errorf("branch name has no usable directory name: %q", branch)
	}
	head, err := runGit(repoDir, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil || head == "" {
		return WorktreeEnsured{}, fmt.Errorf("HEAD of %s names no commit to start %q from", repoDir, branch)
	}
	list := listWorktrees(repoDir)
	mainRoot := ""
	for _, wt := range list {
		if wt.Main {
			mainRoot = wt.Path
			break
		}
	}
	if mainRoot == "" {
		return WorktreeEnsured{}, fmt.Errorf("cannot locate the main checkout of %s", repoDir)
	}
	root := WorktreesRoot(mainRoot)
	if err := checkWorktreesRoot(mainRoot, root); err != nil {
		return WorktreeEnsured{}, err
	}
	if path, found, err := reuseWorktree(repoDir, list, root, branch); found || err != nil {
		return WorktreeEnsured{Path: path}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return WorktreeEnsured{}, fmt.Errorf("worktrees root: %w", err)
	}
	if !isInside(mainRoot, root) {
		return WorktreeEnsured{}, fmt.Errorf("worktrees root %s resolves outside %s", root, mainRoot)
	}
	if err := writeWorktreesIgnore(root); err != nil {
		return WorktreeEnsured{}, err
	}
	path := filepath.Join(root, dirName)
	args := []string{"worktree", "add"}
	if refExists(repoDir, "refs/heads/"+branch) {
		args = append(args, "--", path, branch)
	} else {
		args = append(args, "--no-track", "-b", branch, "--", path, head)
	}
	if _, err := runGit(repoDir, args...); err != nil {
		return WorktreeEnsured{}, err
	}
	opened := WorktreeEnsured{Path: path, Created: true}
	opened.Included, opened.IncludeErr = CopyWorktreeIncludes(mainRoot, path)
	return opened, nil
}

// FolderIn names the folder of the worktree at worktree that stands where dir
// stands in its own checkout: <worktree>/pkg for <checkout>/pkg. It is the
// worktree's root when dir is the root of its checkout, is not in a git
// repository, or names a folder the worktree does not have (one git does not
// track).
func FolderIn(dir, worktree string) string {
	if !GitAvailable() {
		return worktree
	}
	prefix, err := runGit(dir, "rev-parse", "--show-prefix")
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if err != nil || prefix == "" || !safeRelative(prefix) {
		return worktree
	}
	folder := filepath.Join(worktree, filepath.FromSlash(prefix))
	if fi, err := os.Stat(folder); err != nil || !fi.IsDir() || !isInside(worktree, folder) {
		return worktree
	}
	return folder
}
