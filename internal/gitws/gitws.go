// Package gitws inspects and manipulates git working copies for
// per-session workspace switching (folder, branch, worktree) and the Edits
// view of a chat.
//
// Every operation drives the git binary when it is on PATH and falls back to
// a built-in implementation on go-git when it is not (builtin.go,
// builtin_changes.go), chosen per call by GitAvailable. The built-in one has
// no rename detection and cannot open a linked worktree (ErrNeedsGitBinary).
package gitws

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Worktree describes one entry from `git worktree list`.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Main   bool   `json:"main"`
}

// Info describes the git state of a workspace folder.
type Info struct {
	Path       string     `json:"path"`
	IsGitRepo  bool       `json:"is_git_repo"`
	RepoRoot   string     `json:"repo_root,omitempty"`
	BaseBranch string     `json:"base_branch,omitempty"`
	Branch     string     `json:"branch,omitempty"`
	Branches   []string   `json:"branches,omitempty"`
	IsWorktree bool       `json:"is_worktree"`
	Worktrees  []Worktree `json:"worktrees,omitempty"`
}

// GitAvailable reports whether the git binary is on PATH.
func GitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// These commands run synchronously inside request handlers and agent
	// turns: a remote that answers an auth prompt on the terminal would hang
	// them for good, so every credential prompt fails instead of asking.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	platform.AdaptCommand(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Clone shallow-clones url into dest. When ref is non-empty it clones that
// branch or tag. dest must not exist or be an empty directory.
func Clone(url, ref, dest string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return fmt.Errorf("empty clone url")
	}
	// A url or ref beginning with "-" would be parsed as a git option; reject it
	// rather than let it inject flags (e.g. --upload-pack).
	if strings.HasPrefix(url, "-") {
		return fmt.Errorf("refusing clone url that looks like an option: %q", url)
	}
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("refusing ref that looks like an option: %q", ref)
	}
	if !GitAvailable() {
		return builtinClone(url, ref, dest)
	}
	// Disable the ext:: transport (arbitrary command execution via clone URL).
	args := []string{"-c", "protocol.ext.allow=never", "clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	// "--" stops option parsing so url/dest are always positional arguments.
	args = append(args, "--", url, dest)
	// Run from the parent so a relative dest resolves predictably.
	_, err := runGit(filepath.Dir(dest), args...)
	return err
}

// Pull fast-forwards the working copy at dir. Used to refresh an existing clone.
func Pull(dir string) error {
	if !GitAvailable() {
		return builtinPull(dir)
	}
	_, err := runGit(dir, "pull", "--ff-only")
	return err
}

// Describe inspects dir. It never fails on plain folders: a non-repo dir
// yields Info{IsGitRepo: false}.
func Describe(dir string) Info {
	info := Info{Path: dir}
	if abs, err := filepath.Abs(dir); err == nil {
		info.Path = abs
	}
	if !GitAvailable() {
		return builtinDescribe(info)
	}
	toplevel, err := runGit(info.Path, "rev-parse", "--show-toplevel")
	if err != nil || toplevel == "" {
		return info
	}
	info.IsGitRepo = true

	if branch, err := runGit(info.Path, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && branch != "HEAD" {
		info.Branch = branch
	}
	if refs, err := runGit(info.Path, "for-each-ref", "--format=%(refname:short)", "refs/heads"); err == nil && refs != "" {
		info.Branches = strings.Split(refs, "\n")
	}

	info.Worktrees = listWorktrees(info.Path)
	info.BaseBranch = defaultBranch(info.Path)
	if len(info.Worktrees) > 0 {
		info.RepoRoot = info.Worktrees[0].Path
		info.IsWorktree = !samePath(toplevel, info.RepoRoot)
	} else {
		info.RepoRoot = toplevel
	}
	return info
}

// defaultBranch reads the local origin/HEAD without contacting the remote.
func defaultBranch(dir string) string {
	ref, err := runGit(dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(ref, "origin/")
}

// MainCheckoutRoot identifies the main checkout using git's common directory.
// A plain directory yields an empty string.
func MainCheckoutRoot(dir string) string {
	if !GitAvailable() {
		return builtinMainCheckoutRoot(dir)
	}
	common, err := runGit(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || filepath.Base(common) != ".git" {
		return ""
	}
	return filepath.Dir(common)
}

// listWorktrees parses `git worktree list --porcelain`; the first entry is
// always the main worktree.
func listWorktrees(dir string) []Worktree {
	out, err := runGit(dir, "worktree", "list", "--porcelain")
	if err != nil || out == "" {
		return nil
	}
	var list []Worktree
	for block := range strings.SplitSeq(out, "\n\n") {
		var wt Worktree
		for line := range strings.SplitSeq(block, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				wt.Path = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "branch refs/heads/"):
				wt.Branch = strings.TrimPrefix(line, "branch refs/heads/")
			}
		}
		if wt.Path == "" {
			continue
		}
		wt.Main = len(list) == 0
		list = append(list, wt)
	}
	return list
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// Checkout switches the working copy at dir to branch in place.
func Checkout(dir, branch string) error {
	if strings.TrimSpace(branch) == "" {
		return fmt.Errorf("empty branch name")
	}
	if !GitAvailable() {
		return builtinCheckout(dir, branch)
	}
	_, err := runGit(dir, "checkout", branch)
	return err
}

// WorktreesRoot is where a repository keeps the worktrees Coddy opens in it:
// one folder inside the checkout, next to the rest of the project-local Coddy
// state, the way Claude Code and Codex keep theirs. Keeping them in the
// repository rather than in the agent home means a worktree is found where the
// project is, and the operator never has to ignore a stray folder of their own.
func WorktreesRoot(repoRoot string) string {
	return filepath.Join(repoRoot, ".coddy", "worktrees")
}

// EnsureWorktree returns the path of a worktree for branch, creating it under
// WorktreesRoot of the main checkout when missing. Reports whether it was
// created. repoDir may be any working copy of the repository, a linked worktree
// included: the new tree always belongs to the main checkout's root.
func EnsureWorktree(repoDir, branch string) (string, bool, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", false, fmt.Errorf("empty branch name")
	}
	// `git worktree add <path> --detach` is a valid invocation, so a branch
	// beginning with "-" would be read as an option and silently build a
	// worktree nobody asked for. Reject it the way Clone rejects such a ref.
	if strings.HasPrefix(branch, "-") {
		return "", false, fmt.Errorf("refusing branch name that looks like an option: %q", branch)
	}
	dirName := BranchDirName(branch)
	if dirName == "" {
		return "", false, fmt.Errorf("branch name has no usable directory name: %q", branch)
	}
	// The built-in backend has no `git worktree add`; it says so before
	// creating anything on disk rather than half-building a tree.
	if !GitAvailable() {
		return "", false, fmt.Errorf("open a worktree for %q: %w", branch, ErrNeedsGitBinary)
	}
	if _, err := runGit(repoDir, "check-ref-format", "--branch", branch); err != nil {
		return "", false, fmt.Errorf("invalid branch name %q: %w", branch, err)
	}
	// The tree belongs to the main checkout even when we were handed a linked
	// worktree. An empty listing means git could not describe the repository at
	// all; stop here rather than guess a root and leave the worktrees folder
	// behind on the way to a failing `git worktree add`.
	list := listWorktrees(repoDir)
	mainRoot := ""
	for _, wt := range list {
		if wt.Main {
			mainRoot = wt.Path
			break
		}
	}
	if mainRoot == "" {
		return "", false, fmt.Errorf("cannot locate the main checkout of %s", repoDir)
	}
	root := WorktreesRoot(mainRoot)
	// `.coddy` is repository content, so a checkout can ship it - or the
	// worktrees folder under it - as a symlink aimed anywhere. Resolve what
	// already exists and refuse to write outside the checkout the worktree
	// belongs to, before the reuse loop's ignore-file repair or MkdirAll
	// below follow one.
	if err := checkWorktreesRoot(mainRoot, root); err != nil {
		return "", false, err
	}
	// Refusing the default branch and branches tracking it is a local
	// decision whenever origin/HEAD is already resolved; it must not wait on
	// the network. A base git has not resolved yet is checked again after
	// the fetch below.
	if err := refuseDefaultBranch(repoDir, branch, defaultBranch(repoDir)); err != nil {
		return "", false, err
	}
	// Reusing an existing worktree needs no origin at all: keep the fast
	// path offline-capable.
	for _, wt := range list {
		if wt.Branch != branch {
			continue
		}
		if wt.Main {
			return "", false, fmt.Errorf("branch %q is checked out in the main checkout; switch it away before creating a worktree", branch)
		}
		// One of ours that lost its ignore file - `git clean -xdf` deletes the
		// file and keeps the worktrees - would stay visible in git status
		// forever, because every later call ends here. Put it back.
		if isInside(root, wt.Path) {
			if err := writeWorktreesIgnore(root); err != nil {
				return "", false, err
			}
		}
		return wt.Path, false, nil
	}

	if _, err := runGit(repoDir, "fetch", "origin"); err != nil {
		return "", false, fmt.Errorf("refresh origin before creating a worktree: %w", err)
	}
	if _, err := runGit(repoDir, "remote", "set-head", "origin", "-a"); err != nil {
		return "", false, fmt.Errorf("resolve origin/HEAD: %w", err)
	}
	base := defaultBranch(repoDir)
	if base == "" {
		return "", false, fmt.Errorf("origin/HEAD does not name a default branch")
	}
	// The default branch may have moved with the fetch, or been unknown
	// locally before it.
	if err := refuseDefaultBranch(repoDir, branch, base); err != nil {
		return "", false, err
	}
	fastForwardBase(repoDir, mainRoot, base, list)

	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", false, fmt.Errorf("worktrees root: %w", err)
	}
	// The same containment check as above, on the directories MkdirAll
	// actually produced.
	if !isInside(mainRoot, root) {
		return "", false, fmt.Errorf("worktrees root %s resolves outside %s", root, mainRoot)
	}
	if err := writeWorktreesIgnore(root); err != nil {
		return "", false, err
	}
	path := filepath.Join(root, dirName)
	args := []string{"worktree", "add"}
	switch {
	case refExists(repoDir, "refs/heads/"+branch):
		// A local branch is reused at its own tip; it is not rebased onto
		// the fetched default branch.
		args = append(args, "--", path, branch)
	case refExists(repoDir, "refs/remotes/origin/"+branch):
		// The branch exists on origin but has no local tip: materialize it
		// instead of silently redefining it at origin/<base>.
		args = append(args, "--track", "-b", branch, "--", path, "origin/"+branch)
	default:
		args = append(args, "--no-track", "-b", branch)
		args = append(args, "--", path, "origin/"+base)
	}
	if _, err := runGit(repoDir, args...); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// refuseDefaultBranch rejects the default branch itself and local branches
// that track it. An empty base means origin/HEAD is not resolved yet and
// nothing can be proven here; the caller checks again after fetching.
func refuseDefaultBranch(repoDir, branch, base string) error {
	if base == "" {
		return nil
	}
	if branch == base || branch == "origin/"+base {
		return fmt.Errorf("worktrees require a feature branch; %q is the default branch", branch)
	}
	if upstream, _ := runGit(repoDir, "for-each-ref", "--format=%(upstream:short)", "refs/heads/"+branch); upstream == "origin/"+base {
		return fmt.Errorf("branch %q tracks the default branch origin/%s", branch, base)
	}
	return nil
}

// checkWorktreesRoot verifies that every component of root below mainRoot
// that already exists resolves inside mainRoot. `.coddy` and `worktrees`
// are repository content a checkout can ship as symlinks aimed anywhere;
// a dangling symlink (existing entry that does not resolve) is refused too,
// so MkdirAll never creates directories outside the checkout.
func checkWorktreesRoot(mainRoot, root string) error {
	for _, dir := range []string{filepath.Join(mainRoot, ".coddy"), root} {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			if _, statErr := os.Lstat(dir); statErr != nil {
				continue // nothing there; MkdirAll creates real directories
			}
			return fmt.Errorf("worktrees root %s does not resolve: %w", dir, err)
		}
		if !isInside(mainRoot, resolved) {
			return fmt.Errorf("worktrees root %s resolves outside %s", dir, mainRoot)
		}
	}
	return nil
}

// refExists reports whether ref resolves to a commit.
func refExists(repoDir, ref string) bool {
	_, err := runGit(repoDir, "show-ref", "--verify", "--quiet", ref)
	return err == nil
}

// fastForwardBase advances a local default branch only when its old commit is
// an ancestor of origin's and no checked-out copy would be changed behind its
// worktree. A dirty main checkout is left alone; the new branch still uses the
// fetched remote commit.
func fastForwardBase(repoDir, mainRoot, base string, list []Worktree) {
	old, err := runGit(repoDir, "rev-parse", "refs/heads/"+base)
	if err != nil {
		return
	}
	next, err := runGit(repoDir, "rev-parse", "refs/remotes/origin/"+base)
	if err != nil || old == next {
		return
	}
	if _, err := runGit(repoDir, "merge-base", "--is-ancestor", old, next); err != nil {
		return
	}
	for _, wt := range list {
		if wt.Branch != base {
			continue
		}
		if !wt.Main {
			return
		}
		if status, err := runGit(mainRoot, "status", "--porcelain"); err != nil || status != "" {
			return
		}
		_, _ = runGit(mainRoot, "merge", "--ff-only", "origin/"+base)
		return
	}
	_, _ = runGit(repoDir, "update-ref", "refs/heads/"+base, next, old)
}

// writeWorktreesIgnore keeps the worktrees root out of the main checkout's
// `git status`. A .gitignore holding "*" ignores everything below it, the file
// itself included, so the folder stays invisible without a line in the
// repository's own ignore list. Whatever is already there belongs to the
// operator and is left alone: O_EXCL both settles the race between two callers
// and refuses to follow a symlink standing in for the file.
func writeWorktreesIgnore(root string) error {
	path := filepath.Join(root, ".gitignore")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("worktrees ignore file: %w", err)
	}
	if _, err := f.WriteString("*\n"); err != nil {
		_ = f.Close()
		return fmt.Errorf("worktrees ignore file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("worktrees ignore file: %w", err)
	}
	return nil
}

// isInside reports whether path sits below root, both resolved through any
// symlinks on the way, so a worktree elsewhere is never mistaken for one of
// the ones this package manages.
func isInside(root, path string) bool {
	a, errA := filepath.EvalSymlinks(root)
	if errA != nil {
		a = filepath.Clean(root)
	}
	b, errB := filepath.EvalSymlinks(path)
	if errB != nil {
		b = filepath.Clean(path)
	}
	rel, err := filepath.Rel(a, b)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// BranchDirName maps a branch name to a filesystem-safe directory name.
func BranchDirName(branch string) string {
	mapped := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', ' ', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		return r
	}, strings.TrimSpace(branch))
	return strings.Trim(mapped, "-.")
}
