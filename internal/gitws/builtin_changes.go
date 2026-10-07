package gitws

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// builtinWorkspace is the opened repository the change listing works against.
// Status paths are relative to the worktree root, so prefix keeps track of how
// far below it dir sits ("" or a slash path ending in /).
type builtinWorkspace struct {
	repo   *git.Repository
	wt     *git.Worktree
	root   string
	prefix string
	status git.Status
	head   *object.Tree // nil when the repository has no commit yet
}

func openBuiltinWorkspace(dir string) (*builtinWorkspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	repo, err := openRepo(abs)
	if err != nil {
		return nil, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, err
	}
	root := wt.Filesystem.Root()
	// Both sides resolved through symlinks, or a symlinked worktree root
	// would make every repo-relative path look like it sits outside dir.
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%s is outside the worktree root %s", dir, root)
	}
	bw := &builtinWorkspace{repo: repo, wt: wt, root: root}
	if rel != "." {
		bw.prefix = filepath.ToSlash(rel) + "/"
	}
	// The user's and the system's ignore files, which go-git does not read.
	wt.Excludes = append(wt.Excludes, globalExcludes()...)
	status, err := wt.Status()
	if err != nil {
		return nil, err
	}
	bw.status = status
	if head, err := repo.Head(); err == nil {
		if commit, err := repo.CommitObject(head.Hash()); err == nil {
			bw.head, _ = commit.Tree()
		}
	}
	return bw, nil
}

// under strips the workspace prefix off a repo-relative status path; false
// reports a path that does not belong to dir.
func (bw *builtinWorkspace) under(path string) (string, bool) {
	if bw.prefix == "" {
		return path, true
	}
	return strings.CutPrefix(path, bw.prefix)
}

// onDiskFile reports whether the worktree holds a file at the repo-relative
// path; a directory where a file should be counts as absent.
func (bw *builtinWorkspace) onDiskFile(path string) bool {
	info, err := os.Lstat(filepath.Join(bw.root, filepath.FromSlash(path)))
	return err == nil && !info.IsDir()
}

// nestedRepo reports the folder of a repository nested in the worktree that
// holds the repo-relative path, "" when there is none. git lists such a
// repository as one entry, "nested/", and never reads into it; go-git's
// status walks its files like any others.
func (bw *builtinWorkspace) nestedRepo(path string, seen map[string]bool) string {
	for d := slashDir(path); d != ""; d = slashDir(d) {
		nested, known := seen[d]
		if !known {
			_, err := os.Lstat(filepath.Join(bw.root, filepath.FromSlash(d), ".git"))
			nested = err == nil
			seen[d] = nested
		}
		if nested {
			// The outermost one is the entry git reports.
			if outer := bw.nestedRepo(d, seen); outer != "" {
				return outer
			}
			return d
		}
	}
	return ""
}

// slashDir is path.Dir for a repo-relative slash path, "" at the top.
func slashDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

// changeSet reads the status into the same record list `git diff HEAD
// --name-status` produces - except renames, which read as a deletion plus an
// addition - plus every untracked path, all relative to the workspace dir.
func (bw *builtinWorkspace) changeSet() (records []changedRecord, untracked []string) {
	seen := map[string]bool{}
	nestedListed := map[string]bool{}
	for path, fs := range bw.status {
		if fs.Worktree == git.Untracked {
			// A nested repository is one untracked entry with a trailing
			// slash, as `git ls-files --others` prints it: nothing in it is
			// read, and a discard cannot remove a folder that is not empty.
			if nested := bw.nestedRepo(path, seen); nested != "" {
				path = nested + "/"
				if nestedListed[path] {
					continue
				}
				nestedListed[path] = true
			}
		}
		rel, ok := bw.under(path)
		if !ok {
			continue
		}
		if fs.Worktree == git.Untracked {
			untracked = append(untracked, rel)
			continue
		}
		if fs.Staging == git.Unmodified && fs.Worktree == git.Unmodified {
			continue
		}
		// go-git's status codes do not line up with name-status codes, so the
		// kind is decided against HEAD's tree and the disk instead. Without a
		// commit yet, nothing is in HEAD and everything is an addition.
		inHead := false
		if bw.head != nil {
			_, err := bw.head.File(path)
			inHead = err == nil
		}
		onDisk := bw.onDiskFile(path)
		var code byte
		switch {
		case inHead && onDisk:
			code = 'M'
		case onDisk:
			code = 'A'
		case inHead:
			code = 'D'
		case fs.Staging == git.Added:
			// Added to the index and deleted since: nothing to show, but a
			// discard still drops the entry.
			code = 'A'
		default:
			continue
		}
		records = append(records, changedRecord{code: code, oldPath: rel, newPath: rel})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].newPath < records[j].newPath })
	sort.Strings(untracked)
	return records, untracked
}

// headBlob reads a file out of HEAD's tree. The record paths are relative to
// the workspace dir, so the prefix goes back on before the tree lookup.
func (bw *builtinWorkspace) headBlob(rel string) ([]byte, error) {
	f, err := bw.head.File(bw.prefix + rel)
	if err != nil {
		return nil, err
	}
	r, err := f.Reader()
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// builtinUncommittedChanges is UncommittedChanges without the git binary.
func builtinUncommittedChanges(dir string) ([]WorkChange, int, error) {
	bw, err := openBuiltinWorkspace(dir)
	if err != nil {
		return nil, 0, nil
	}
	records, untracked := bw.changeSet()
	changes, err := assembleChanges(dir, records, bw.headBlob)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, len(untracked), nil
}

// builtinUncommittedChangeFor is UncommittedChangeFor without the git binary.
func builtinUncommittedChangeFor(dir, path string) (*WorkChange, error) {
	bw, err := openBuiltinWorkspace(dir)
	if err != nil {
		return nil, nil
	}
	records, _ := bw.changeSet()
	want := filepath.FromSlash(path)
	for _, rec := range records {
		if filepath.FromSlash(rec.newPath) != want || indexOnly(dir, rec) {
			continue
		}
		change, err := buildWorkChange(dir, rec.code, rec.oldPath, rec.newPath, bw.headBlob)
		if err != nil {
			return nil, err
		}
		if isLineEndingChurn(rec.code, change) {
			return nil, nil
		}
		return &change, nil
	}
	return nil, nil
}

// builtinUntrackedPaths is the untracked listing `git ls-files --others
// --exclude-standard` produces, honouring .gitignore the same way.
func builtinUntrackedPaths(dir string) []string {
	bw, err := openBuiltinWorkspace(dir)
	if err != nil {
		return nil
	}
	_, untracked := bw.changeSet()
	return untracked
}

// builtinDiscardChangeSet is the change set a discard picks from without the
// git binary: go-git's status already compares the index with HEAD and the
// disk with the index, so a staged change the disk undid is in it too.
func builtinDiscardChangeSet(dir string) ([]changedRecord, []string, error) {
	bw, err := openBuiltinWorkspace(dir)
	if err != nil {
		return nil, nil, err
	}
	records, untracked := bw.changeSet()
	return records, untracked, nil
}

// builtinDiscard puts the index entries and the working files HEAD holds back
// through go-git's restore, then drops the index entries of the paths HEAD
// does not hold; removing those files is the caller's shared step.
func builtinDiscard(dir string, restore, drop []string) error {
	bw, err := openBuiltinWorkspace(dir)
	if err != nil {
		return err
	}
	if len(restore) > 0 {
		files := make([]string, 0, len(restore))
		for _, rel := range restore {
			files = append(files, bw.prefix+rel)
		}
		err := bw.wt.Restore(&git.RestoreOptions{
			Staged:   true,
			Worktree: true,
			Files:    files,
		})
		if err != nil {
			return err
		}
	}
	if len(drop) > 0 {
		idx, err := bw.repo.Storer.Index()
		if err != nil {
			return err
		}
		doomed := make(map[string]bool, len(drop))
		for _, rel := range drop {
			doomed[bw.prefix+rel] = true
		}
		idx.Entries = slices.DeleteFunc(idx.Entries, func(e *index.Entry) bool {
			return doomed[e.Name]
		})
		if err := bw.repo.Storer.SetIndex(idx); err != nil {
			return err
		}
	}
	return nil
}
