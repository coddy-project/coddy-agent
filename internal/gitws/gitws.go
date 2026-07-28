// Package gitws inspects and manipulates git working copies for
// per-session workspace switching (folder, branch, worktree).
package gitws

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Clone shallow-clones url into dest. When ref is non-empty it clones that
// branch or tag. dest must not already exist.
func Clone(url, ref, dest string) error {
	if !GitAvailable() {
		return fmt.Errorf("git binary not found on PATH")
	}
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
		return fmt.Errorf("git binary not found on PATH")
	}
	_, err := runGit(dir, "pull", "--ff-only")
	return err
}

// Describe inspects dir. It never fails on plain folders: a non-repo dir
// (or a missing git binary) yields Info{IsGitRepo: false}.
func Describe(dir string) Info {
	info := Info{Path: dir}
	if abs, err := filepath.Abs(dir); err == nil {
		info.Path = abs
	}
	if !GitAvailable() {
		return info
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
	if len(info.Worktrees) > 0 {
		info.RepoRoot = info.Worktrees[0].Path
		info.IsWorktree = !samePath(toplevel, info.RepoRoot)
	} else {
		info.RepoRoot = toplevel
	}
	return info
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
	_, err := runGit(dir, "checkout", branch)
	return err
}

// EnsureWorktree returns the path of a worktree for branch, creating it
// under worktreesRoot when missing. Reports whether it was created.
func EnsureWorktree(repoDir, branch, worktreesRoot string) (string, bool, error) {
	if strings.TrimSpace(branch) == "" {
		return "", false, fmt.Errorf("empty branch name")
	}
	for _, wt := range listWorktrees(repoDir) {
		if wt.Branch == branch {
			return wt.Path, false, nil
		}
	}
	path := filepath.Join(worktreesRoot, BranchDirName(branch))
	if err := os.MkdirAll(worktreesRoot, 0o755); err != nil {
		return "", false, fmt.Errorf("worktrees root: %w", err)
	}
	if _, err := runGit(repoDir, "worktree", "add", path, branch); err != nil {
		return "", false, err
	}
	return path, true, nil
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
