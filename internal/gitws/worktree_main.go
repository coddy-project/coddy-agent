package gitws

import (
	"os"
	"path/filepath"
	"strings"
)

// MainCheckoutFolder answers, for a folder inside a linked worktree, the same
// folder of the repository's main checkout: <main>/pkg for <worktree>/pkg. It
// is how a worktree shares what belongs to the project rather than to one
// checkout of it - the approvals of project configuration, a project MCP
// server, the cron of a project job. ok is false for a folder in no linked
// worktree: the main checkout itself, a plain folder, a repository of its own
// nested in a checkout, a submodule, a worktree of a bare repository (its
// storage named .git or not). The answer never leaves the main checkout: a
// folder the main checkout holds as a link to somewhere else answers none.
//
// It reads files and never runs git, so a hot path (every trust decision)
// may call it. A worktree counts only when git links it both ways: its .git
// file names <common>/worktrees/<name>, and that folder's gitdir file names
// this .git file back. A .git file is plain text, and one copied or written
// into another folder does not lend that folder the repository's identity:
// git registers a worktree in the repository's own storage, which a checkout
// cannot write.
func MainCheckoutFolder(dir string) (string, bool) {
	abs, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil || strings.TrimSpace(dir) == "" {
		return "", false
	}
	root, pointer, ok := nearestDotGit(abs)
	if !ok || pointer == "" {
		// No .git at all, or a .git directory: not a linked worktree.
		return "", false
	}
	gitDir := pointer
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	// <common>/worktrees/<name>: the layout git gives a linked worktree.
	if filepath.Base(filepath.Dir(gitDir)) != "worktrees" {
		return "", false
	}
	common := filepath.Dir(filepath.Dir(gitDir))
	if body, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		named := strings.TrimSpace(string(body))
		if !filepath.IsAbs(named) {
			named = filepath.Join(gitDir, named)
		}
		if !samePath(named, common) {
			return "", false
		}
	} else {
		return "", false
	}
	// A bare repository has worktrees but no main checkout, its storage
	// named .git (cloned into <folder>/.git) or not.
	if filepath.Base(common) != ".git" || isBare(common) {
		return "", false
	}
	back, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return "", false
	}
	backPath := strings.TrimSpace(string(back))
	if !filepath.IsAbs(backPath) {
		backPath = filepath.Join(gitDir, backPath)
	}
	if !samePath(backPath, filepath.Join(root, ".git")) {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	main := filepath.Dir(common)
	folder := filepath.Join(main, rel)
	// Never through a link out of the main checkout: a folder that is a link
	// there (committed in its tree, or shipped with storage of its own in an
	// archive) would lend the worktree the identity of wherever it points.
	if !isInside(main, folder) {
		return "", false
	}
	return folder, true
}

// isBare reports whether the repository whose common dir is common says
// core.bare = true in its config.
func isBare(common string) bool {
	body, err := os.ReadFile(filepath.Join(common, "config"))
	if err != nil {
		return false
	}
	section := ""
	for line := range strings.SplitSeq(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(strings.Trim(line, "[] \t"))
			continue
		}
		if section != "core" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "bare") {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "true", "yes", "on", "1":
				return true
			}
		}
	}
	return false
}

// nearestDotGit walks up from dir to the first folder holding a .git entry
// and returns that folder and, when the entry is a file, the gitdir it
// names; pointer is empty for a .git directory. A folder that does not exist
// yet is walked through like one that does.
func nearestDotGit(dir string) (root, pointer string, ok bool) {
	for d := dir; ; {
		entry := filepath.Join(d, ".git")
		// Stat, not Lstat: a .git that is a link to a directory is one, as
		// git reads it.
		if fi, err := os.Stat(entry); err == nil {
			if fi.IsDir() {
				return d, "", true
			}
			body, err := os.ReadFile(entry)
			if err != nil {
				return "", "", false
			}
			target, found := strings.CutPrefix(strings.TrimSpace(string(body)), "gitdir:")
			if !found {
				return "", "", false
			}
			return d, strings.TrimSpace(target), true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", "", false
		}
		d = parent
	}
}
