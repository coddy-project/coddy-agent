package gitws

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var (
	// ErrNotRepository answers a call that needs a repository in a folder
	// that is not inside one.
	ErrNotRepository = errors.New("not a git repository")
	// ErrNotChanged refuses a path that is not part of the folder's change
	// set: unchanged, ignored, outside the folder or missing.
	ErrNotChanged = errors.New("path is not among the working copy changes")
	// ErrNeedsGitBinary answers an operation the built-in implementation does
	// not have, such as opening a linked worktree.
	ErrNeedsGitBinary = errors.New("this operation needs the git binary on PATH")
)

// Discard puts the working copy of dir back at HEAD for the given paths, or for
// every change WorktreeChanges reports when paths is empty.
func Discard(dir string, paths []string) error {
	if !Describe(dir).IsGitRepo {
		return fmt.Errorf("%w: %s", ErrNotRepository, dir)
	}
	records, untracked, err := discardChangeSet(dir)
	if err != nil {
		return err
	}
	sel, err := selectDiscard(records, untracked, paths)
	if err != nil {
		return err
	}
	// restore covers the paths HEAD holds, drop the ones it does not (staged
	// additions, the new side of a rename); both get deleted with the
	// untracked files afterwards.
	var restore, drop []string
	for _, rec := range sel.records {
		switch rec.code {
		case 'A', 'C':
			drop = append(drop, rec.newPath)
		case 'R':
			restore = append(restore, rec.oldPath)
			drop = append(drop, rec.newPath)
		default: // M, D, T, U
			restore = append(restore, rec.newPath)
		}
	}
	if GitAvailable() {
		if err := gitDiscardIndex(dir, sel.records, restore, hasHead(dir)); err != nil {
			return err
		}
	} else if err := builtinDiscard(dir, restore, drop); err != nil {
		return err
	}
	return deleteWorktreeFiles(dir, append(drop, sel.untracked...))
}

// discardChangeSet is the change set a discard can pick from: the tracked
// records, the index entries that differ from HEAD where the disk does not,
// and every untracked path, all relative to dir. In a repository without a
// commit every file of the index is an addition.
func discardChangeSet(dir string) ([]changedRecord, []string, error) {
	if !GitAvailable() {
		return builtinDiscardChangeSet(dir)
	}
	if !hasHead(dir) {
		// No commit yet: everything in the index is an addition to drop.
		records, err := indexRecords(dir)
		if err != nil {
			return nil, nil, err
		}
		return records, untrackedPaths(dir), nil
	}
	records, err := changedRecords(dir)
	if err != nil {
		return nil, nil, err
	}
	// The index can differ from HEAD where the disk does not; those paths are
	// part of what a discard puts back.
	staged, err := stagedRecords(dir)
	if err != nil {
		return nil, nil, err
	}
	seen := make(map[string]bool, 2*len(records))
	for _, rec := range records {
		seen[rec.oldPath], seen[rec.newPath] = true, true
	}
	for _, rec := range staged {
		if !seen[rec.newPath] {
			records = append(records, rec)
		}
	}
	return records, untrackedPaths(dir), nil
}

// discardSelection is the part of the change set a call asked for.
type discardSelection struct {
	records   []changedRecord
	untracked []string
}

// selectDiscard matches the requested paths against the change set. One bad
// entry refuses the whole call before anything is touched.
func selectDiscard(records []changedRecord, untracked []string, paths []string) (discardSelection, error) {
	if len(paths) == 0 {
		return discardSelection{records: records, untracked: untracked}, nil
	}
	var sel discardSelection
	for _, p := range paths {
		rel := filepath.Clean(filepath.FromSlash(p))
		if rel == "." || rel == "" || filepath.IsAbs(rel) ||
			rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return discardSelection{}, fmt.Errorf("%w: %s", ErrNotChanged, p)
		}
		matched := false
		for _, rec := range records {
			if filepath.FromSlash(rec.newPath) == rel {
				sel.records = append(sel.records, rec)
				matched = true
				break
			}
		}
		if !matched {
			for _, u := range untracked {
				if filepath.FromSlash(u) == rel {
					sel.untracked = append(sel.untracked, u)
					matched = true
					break
				}
			}
		}
		if !matched {
			return discardSelection{}, fmt.Errorf("%w: %s", ErrNotChanged, p)
		}
	}
	return sel, nil
}

// gitDiscardIndex resets every touched path's index entry back to HEAD and
// checks the HEAD-held ones out; what is left (added and renamed-away files,
// the untracked pile) is plain file deletion shared with the built-in backend.
// Without a commit there is no HEAD to reset to: the entries are removed from
// the index instead, all of them additions.
func gitDiscardIndex(dir string, records []changedRecord, restore []string, head bool) error {
	var index []string
	for _, rec := range records {
		index = append(index, rec.oldPath)
		if rec.newPath != rec.oldPath {
			index = append(index, rec.newPath)
		}
	}
	if !head {
		return runGitPaths(dir, index, "--literal-pathspecs", "rm", "--cached", "-q", "--ignore-unmatch", "--")
	}
	if err := runGitPaths(dir, index, "--literal-pathspecs", "reset", "-q", "HEAD", "--"); err != nil {
		return err
	}
	return runGitPaths(dir, restore, "--literal-pathspecs", "checkout", "-q", "--")
}

// runGitPaths runs one git command per hundred paths, so a large discard stays
// under the argument limit.
func runGitPaths(dir string, paths []string, args ...string) error {
	for len(paths) > 0 {
		n := min(len(paths), 100)
		if _, err := runGit(dir, append(slices.Clone(args), paths[:n]...)...); err != nil {
			return err
		}
		paths = paths[n:]
	}
	return nil
}

// deleteWorktreeFiles removes dir-relative paths through an os.Root so a
// symlinked parent can never redirect a delete outside the folder - the
// symlink itself is removed instead. Each emptied parent directory goes too,
// but never dir itself or anything above it.
func deleteWorktreeFiles(dir string, rels []string) error {
	if len(rels) == 0 {
		return nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	var firstErr error
	for _, rel := range rels {
		p := filepath.FromSlash(rel)
		if err := root.Remove(p); err != nil {
			// A nested repository is listed as its folder, which is never
			// empty: it stays, the way git leaves it. Anything else that
			// could not be removed is reported.
			if !os.IsNotExist(err) && !strings.HasSuffix(rel, "/") && firstErr == nil {
				firstErr = fmt.Errorf("remove %s: %w", rel, err)
			}
			continue
		}
		for parent := filepath.Dir(p); parent != "."; parent = filepath.Dir(parent) {
			if err := root.Remove(parent); err != nil {
				break // not empty or already gone; leave the rest alone
			}
		}
	}
	return firstErr
}
