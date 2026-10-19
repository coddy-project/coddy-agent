package gitws

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The most a pointer file (.git, commondir, gitdir) or a repository config is
// read up to: git's own are a line, or a few kilobytes.
const (
	pointerFileLimit = 4 << 10
	configFileLimit  = 1 << 20
)

// MainCheckoutFolder answers, for a folder inside a linked worktree, the same
// folder of the repository's main checkout: <main>/pkg for <worktree>/pkg. It
// is how a worktree shares what belongs to the project rather than to one
// checkout of it - the approvals of project configuration, a project MCP
// server, the cron of a project job. ok is false for a folder in no linked
// worktree: the main checkout itself, a plain folder, a repository of its own
// nested in a checkout, a submodule, a worktree of a bare repository (its
// storage named .git or not). The answer never leaves the main checkout: a
// folder the main checkout holds as a link to somewhere else, or one in a
// repository nested there, answers none. Only regular files are read, each
// up to a small limit, so a .git that is a link, a FIFO or a device never
// blocks the caller.
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
	// Read the storage where it really is, as git does: every path below is
	// derived from it, and a relative back-link resolved from a path that
	// runs through a link of the checkout's own would land in that checkout.
	gitDir, err = filepath.EvalSymlinks(filepath.Clean(gitDir))
	if err != nil {
		return "", false
	}
	// <common>/worktrees/<name>: the layout git gives a linked worktree.
	if filepath.Base(filepath.Dir(gitDir)) != "worktrees" {
		return "", false
	}
	common := filepath.Dir(filepath.Dir(gitDir))
	if body, err := readSmallFile(filepath.Join(gitDir, "commondir"), pointerFileLimit); err == nil {
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
	back, err := readSmallFile(filepath.Join(gitDir, "gitdir"), pointerFileLimit)
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
	// Nor into a repository of its own nested in the main checkout: that is
	// another project, with approvals of its own. Walked where the folder
	// really is, so a link in the main checkout into such a repository counts.
	realMain, err := filepath.EvalSymlinks(main)
	if err != nil {
		return "", false
	}
	for d := resolveExisting(folder); d != realMain; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return "", false
		}
		parent := filepath.Dir(d)
		if parent == d {
			// Above the main checkout: isInside already refused that.
			return "", false
		}
		d = parent
	}
	return folder, true
}

// resolveExisting resolves the links of the longest part of path that
// exists and keeps the rest as written: where a folder that may not exist
// yet would really be.
func resolveExisting(path string) string {
	for p := path; ; {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			rel, err := filepath.Rel(p, path)
			if err != nil {
				return path
			}
			return filepath.Join(real, rel)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return path
		}
		p = parent
	}
}

// readSmallFile reads a regular file of at most limit bytes. Anything else -
// a link, a FIFO, a device, a file too large - is refused (openRegular), so a
// lookup on a hot path never blocks or fills memory.
func readSmallFile(path string, limit int64) ([]byte, error) {
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return body, nil
}

// isBare reports whether the repository whose common dir is common says
// core.bare = true in its config.
func isBare(common string) bool {
	body, err := readSmallFile(filepath.Join(common, "config"), configFileLimit)
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
			if i := strings.IndexAny(value, "#;"); i >= 0 {
				value = value[:i]
			}
			switch strings.ToLower(strings.Trim(strings.TrimSpace(value), `"`)) {
			case "true", "yes", "on", "1":
				return true
			}
		}
	}
	return false
}

// nearestDotGit walks up from dir to the first folder holding a .git entry
// and returns that folder and, when the entry is a file, the gitdir it
// names; pointer is empty for a .git directory (or a link to one, as git reads
// it). A .git that is any other kind of entry - a link to a file, a FIFO, a
// device - or a file too large ends the walk with nothing, as git refuses it.
// A folder that does not exist yet is walked through like one that does.
func nearestDotGit(dir string) (root, pointer string, ok bool) {
	for d := dir; ; {
		entry := filepath.Join(d, ".git")
		if fi, err := os.Lstat(entry); err == nil {
			switch {
			case fi.IsDir():
				return d, "", true
			case fi.Mode()&os.ModeSymlink != 0:
				if target, err := os.Stat(entry); err == nil && target.IsDir() {
					return d, "", true
				}
				return "", "", false
			}
			body, err := readSmallFile(entry, pointerFileLimit)
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
