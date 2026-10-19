package gitws

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// WorktreeStatus is where one worktree of a repository stands.
type WorktreeStatus struct {
	Path   string `json:"path"`
	Branch string `json:"branch,omitempty"`
	// Main marks the main checkout; Managed a worktree under WorktreesRoot.
	Main    bool `json:"main,omitempty"`
	Managed bool `json:"managed,omitempty"`
	// Head is the short commit HEAD points at, Subject the first line of its
	// message and CommittedAt its committer date (RFC 3339).
	Head        string `json:"head,omitempty"`
	Subject     string `json:"subject,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	// Uncommitted counts the changed, staged and untracked entries; Unpushed
	// the commits of HEAD no remote branch holds.
	Uncommitted int `json:"uncommitted"`
	Unpushed    int `json:"unpushed"`
	// Ahead and Behind compare HEAD with origin's default branch.
	Ahead  int `json:"ahead,omitempty"`
	Behind int `json:"behind,omitempty"`
	// Locked reports a `git worktree lock`; Missing a folder that is gone.
	Locked  bool `json:"locked,omitempty"`
	Missing bool `json:"missing,omitempty"`
}

// ListWorktreeStatus lists every worktree of the repository dir belongs to,
// the main checkout first, with where each stands, and names the main
// checkout and origin's default branch. It only reads.
func ListWorktreeStatus(dir string) (mainCheckout, base string, list []WorktreeStatus, err error) {
	if !GitAvailable() {
		return "", "", nil, fmt.Errorf("list the worktrees of %s: %w", dir, ErrNeedsGitBinary)
	}
	worktrees := listWorktrees(dir)
	if len(worktrees) == 0 {
		return "", "", nil, fmt.Errorf("%s is not inside a git repository", dir)
	}
	mainCheckout = worktrees[0].Path
	base = defaultBranch(mainCheckout)
	for _, wt := range worktrees {
		st := WorktreeStatus{Path: wt.Path, Branch: wt.Branch, Main: wt.Main, Locked: wt.Locked}
		if !wt.Main {
			st.Managed = isInside(WorktreesRoot(mainCheckout), wt.Path)
		}
		if info, err := os.Stat(wt.Path); err != nil || !info.IsDir() {
			st.Missing = true
			list = append(list, st)
			continue
		}
		if line, err := runGit(wt.Path, "log", "-1", "--format=%h%x00%s%x00%cI", "HEAD"); err == nil {
			if parts := strings.SplitN(line, "\x00", 3); len(parts) == 3 {
				st.Head, st.Subject, st.CommittedAt = parts[0], parts[1], parts[2]
			}
		}
		if status, err := runGit(wt.Path, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=none"); err == nil && status != "" {
			st.Uncommitted = len(strings.Split(status, "\n"))
		}
		if n, err := runGit(wt.Path, "rev-list", "--count", "HEAD", "--not", "--remotes"); err == nil {
			st.Unpushed, _ = strconv.Atoi(n)
		}
		if base != "" {
			if counts, err := runGit(wt.Path, "rev-list", "--left-right", "--count", "origin/"+base+"...HEAD"); err == nil {
				if f := strings.Fields(counts); len(f) == 2 {
					st.Behind, _ = strconv.Atoi(f[0])
					st.Ahead, _ = strconv.Atoi(f[1])
				}
			}
		}
		if gitDir, err := runGit(wt.Path, "rev-parse", "--absolute-git-dir"); err == nil && !wt.Main {
			if _, err := os.Stat(filepath.Join(gitDir, "locked")); err == nil {
				st.Locked = true
			}
		}
		list = append(list, st)
	}
	return mainCheckout, base, list, nil
}
