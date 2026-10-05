package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
)

// toolStateDirs are the folders a tool keeps its own bookkeeping in: the
// editor's settings and the version control client's administrative area.
//
// Nothing in them is anyone's edit. An IDE rewrites .idea/workspace.xml when
// the caret moves; svn rewrites wc.db and a pristine copy of every file it
// touches, which is why one svn command buries a three-line edit under a
// hundred binary .svn-base blobs. They are excluded twice over - skipped when
// the workspace is walked, and dropped again when a recording is read back, so
// a session recorded before this rule is as clean as one recorded after.
var toolStateDirs = map[string]bool{
	".git": true, ".svn": true,
	".idea": true, ".vscode": true,
}

// ignoredDirs are skipped when walking the workspace on top of toolStateDirs.
//
// These hold generated content rather than bookkeeping: recording them would
// bury the edits, but a file under them is still a file, so - unlike tool state
// - they are only skipped at capture time. What a working copy reports under
// them stays visible.
var ignoredDirs = map[string]bool{
	"node_modules": true, "__pycache__": true,
	".venv": true, "venv": true, ".tox": true,
	"vendor": true, ".vendor": true,
	"dist": true, "build": true, ".next": true, "out": true,
	"target": true, ".gradle": true,
	".cache": true, ".sass-cache": true, ".mypy_cache": true,
}

// IsToolStatePath reports whether a path lies inside one of the tool state
// folders, at any depth.
//
// Matching whole segments keeps ordinary files - docs/idea.md, .ideas/plan.md,
// git-notes.txt - out of it. Both separators are levelled because a recording
// holds OS-shaped paths while a caller may well ask with forward slashes.
func IsToolStatePath(path string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if toolStateDirs[segment] {
			return true
		}
	}
	return false
}

const (
	maxFileSizeBytes  = 10 * 1024 * 1024  // skip files larger than 10 MB
	maxTotalSizeBytes = 100 * 1024 * 1024 // stop reading content after 100 MB total
	maxScanDepth      = 20
)

// WorkspaceFile holds the content and permissions of a single file.
type WorkspaceFile struct {
	Content []byte      `json:"content"` // binary content (encoding/json encodes as base64)
	Mode    fs.FileMode `json:"mode"`

	// size and modTime are the stat the content was read with. They live only in
	// memory - a stored diff never needs them - and let LiveWorkspaceDiff skip
	// rereading a file that has not been touched since the snapshot.
	size    int64
	modTime time.Time
}

// WorkspaceChange records what happened to one file during a turn.
type WorkspaceChange struct {
	Path   string         `json:"path"`
	Before *WorkspaceFile `json:"before,omitempty"` // nil → file was created this turn
	After  *WorkspaceFile `json:"after,omitempty"`  // nil → file was deleted this turn
}

// WorkspaceDiff is the set of file changes captured during one turn.
type WorkspaceDiff struct {
	Changes []WorkspaceChange `json:"changes"`
	CWD     string            `json:"cwd,omitempty"`
}

// WorkspaceSnapshot is a pre-turn snapshot used to compute the delta afterwards.
type WorkspaceSnapshot struct {
	files    map[string]*WorkspaceFile // relative path → state
	paths    map[string]bool           // includes files omitted by the content limits
	complete bool
	exclude  []string
}

// TakeWorkspaceSnapshot records the current state of all files under cwd.
// Returns a non-nil snapshot even when cwd is empty (snapshot will be empty).
func TakeWorkspaceSnapshot(cwd string, exclude ...string) *WorkspaceSnapshot {
	snap := &WorkspaceSnapshot{files: make(map[string]*WorkspaceFile), paths: make(map[string]bool), exclude: exclude}
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return snap
	}
	defer func() { _ = root.Close() }()
	snap.complete = walkWorkspace(cwd, exclude, func(rel string) { snap.paths[rel] = true }, func(rel, path string, info fs.FileInfo) bool {
		content, err := root.ReadFile(rel)
		if err != nil {
			return false
		}
		snap.files[rel] = &WorkspaceFile{
			Content: content, Mode: info.Mode(),
			size: info.Size(), modTime: info.ModTime(),
		}
		return true
	})
	return snap
}

// walkWorkspace visits every file a snapshot covers, in lexical order and under
// the snapshot's limits: tool state, generated folders and git worktrees are
// skipped, files over maxFileSizeBytes are left out, and content is read until
// maxTotalSizeBytes have been taken. Paths are still observed after the cap.
// visit reports whether it took the file,
// and only a taken file counts toward that total.
//
// The snapshot and the live diff both walk through here, so a file one of them
// reaches the other reaches too - otherwise a workspace past the size cap would
// show files beyond it as created the moment the two walks disagreed.
func walkWorkspace(cwd string, exclude []string, observe func(string), visit func(rel, path string, info fs.FileInfo) bool) bool {
	if cwd == "" {
		return true
	}
	complete := true
	var totalBytes int64
	_ = filepath.WalkDir(cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			complete = false
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			for _, excluded := range exclude {
				if excluded != "" && sameWorkspace(path, excluded) {
					return filepath.SkipDir
				}
			}
			// The worktrees folder holds whole checkouts of other branches: a
			// turn here neither reads them nor rolls them back.
			if ignoredDirs[d.Name()] || toolStateDirs[d.Name()] || gitws.IsWorktreesRoot(path) {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(cwd, path)
			if strings.Count(rel, string(os.PathSeparator)) >= maxScanDepth {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(cwd, path)
		if observe != nil {
			observe(rel)
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, pipes, etc.
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxFileSizeBytes {
			return nil
		}
		if totalBytes+info.Size() > maxTotalSizeBytes {
			return nil // keep listing paths without reading their contents
		}
		if visit(rel, path, info) {
			totalBytes += info.Size()
		}
		return nil
	})
	return complete
}

// LiveWorkspaceDiff is ComputeWorkspaceDiff for a turn that is still running.
//
// It compares the same way, except that a file whose size, modification time
// and mode still match the snapshot is taken as unchanged without being read.
// The changes card, open during a turn, asks after every tool call; reading the
// whole workspace that often would cost what the end-of-turn diff costs, many
// times a turn, while a stat walk costs a directory listing. The trade is a
// same-size rewrite that also restores the old timestamp, which this misses
// until the turn ends - the stored diff, the one a rollback replays, still
// compares contents.
func LiveWorkspaceDiff(cwd string, before *WorkspaceSnapshot) (*WorkspaceDiff, error) {
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	beforeFiles := map[string]*WorkspaceFile{}
	if before != nil {
		beforeFiles = before.files
	}
	var changes []WorkspaceChange
	seen := make(map[string]bool)
	var exclude []string
	if before != nil {
		exclude = before.exclude
	}
	walkWorkspace(cwd, exclude, nil, func(rel, path string, info fs.FileInfo) bool {
		bf := beforeFiles[rel]
		if bf == nil && before != nil && (before.paths[rel] || !before.complete) {
			return false
		}
		if bf != nil && bf.size == info.Size() && bf.modTime.Equal(info.ModTime()) && bf.Mode == info.Mode() {
			seen[rel] = true
			return true
		}
		content, err := root.ReadFile(rel)
		if err != nil {
			return false
		}
		seen[rel] = true
		af := &WorkspaceFile{Content: content, Mode: info.Mode(), size: info.Size(), modTime: info.ModTime()}
		switch {
		case bf == nil:
			changes = append(changes, WorkspaceChange{Path: rel, After: af})
		case string(bf.Content) != string(af.Content) || bf.Mode != af.Mode:
			changes = append(changes, WorkspaceChange{Path: rel, Before: bf, After: af})
		}
		return true
	})
	for rel, bf := range beforeFiles {
		if !seen[rel] && workspaceFileMissing(cwd, rel) {
			changes = append(changes, WorkspaceChange{Path: rel, Before: bf})
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return &WorkspaceDiff{Changes: changes, CWD: cwd}, nil
}

// ComputeWorkspaceDiff compares the current state of cwd against the before snapshot
// and returns a WorkspaceDiff describing what changed. Returns nil diff if nothing changed.
func ComputeWorkspaceDiff(cwd string, before *WorkspaceSnapshot) (*WorkspaceDiff, error) {
	var exclude []string
	if before != nil {
		exclude = before.exclude
	}
	after := TakeWorkspaceSnapshot(cwd, exclude...)
	beforeFiles := make(map[string]*WorkspaceFile)
	if before != nil {
		beforeFiles = before.files
	}

	var changes []WorkspaceChange
	seen := make(map[string]bool)

	// Files that exist after the turn.
	for rel, af := range after.files {
		seen[rel] = true
		bf := beforeFiles[rel]
		if bf == nil {
			if before != nil && (before.paths[rel] || !before.complete) {
				continue
			}
			// New file.
			changes = append(changes, WorkspaceChange{Path: rel, After: af})
		} else if string(bf.Content) != string(af.Content) || bf.Mode != af.Mode {
			// Modified file.
			changes = append(changes, WorkspaceChange{Path: rel, Before: bf, After: af})
		}
	}

	// Files that existed before but are gone now.
	for rel, bf := range beforeFiles {
		if !seen[rel] && workspaceFileMissing(cwd, rel) {
			changes = append(changes, WorkspaceChange{Path: rel, Before: bf})
		}
	}

	if len(changes) == 0 {
		return nil, nil
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return &WorkspaceDiff{Changes: changes, CWD: cwd}, nil
}

// An omitted file (unreadable or past a capture limit) is not a deletion.
func workspaceFileMissing(cwd, rel string) bool {
	_, err := os.Lstat(filepath.Join(cwd, rel))
	return os.IsNotExist(err)
}

// TurnDiffsDir returns the directory where per-turn workspace diffs are stored.
func TurnDiffsDir(sessionDir string) string {
	return filepath.Join(sessionDir, "diffs")
}

// StoreWorkspaceDiff writes the diff to <sessionDir>/diffs/turn_<n>.json atomically.
// Empty turns are stored too, so the last-turn scope does not show an older edit.
func StoreWorkspaceDiff(sessionDir string, turnN int, diff *WorkspaceDiff) error {
	if diff == nil {
		diff = &WorkspaceDiff{}
	}
	dir := TurnDiffsDir(sessionDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(diff)
	if err != nil {
		return fmt.Errorf("marshal workspace diff: %w", err)
	}
	p := filepath.Join(dir, fmt.Sprintf("turn_%d.json", turnN))
	return writeBytesAtomic(p, data)
}

// LoadWorkspaceDiff reads a stored turn diff. Returns nil if the file does not exist.
func LoadWorkspaceDiff(sessionDir string, turnN int) (*WorkspaceDiff, error) {
	p := filepath.Join(TurnDiffsDir(sessionDir), fmt.Sprintf("turn_%d.json", turnN))
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var diff WorkspaceDiff
	if err := json.Unmarshal(data, &diff); err != nil {
		return nil, fmt.Errorf("parse workspace diff: %w", err)
	}
	return &diff, nil
}

// ListStoredTurnDiffs returns turn indices that have stored diff files, sorted descending.
func ListStoredTurnDiffs(sessionDir string) ([]int, error) {
	dir := TurnDiffsDir(sessionDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var nums []int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "turn_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		s := strings.TrimPrefix(name, "turn_")
		s = strings.TrimSuffix(s, ".json")
		if n, err := strconv.Atoi(s); err == nil {
			nums = append(nums, n)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(nums)))
	return nums, nil
}

// RestoreWorkspaceFiles restores workspace files by reversing all turn diffs with
// turn number > afterTurn. Pure Go, no external tools required.
func RestoreWorkspaceFiles(cwd, sessionDir string, afterTurn int) (string, error) {
	if cwd == "" {
		return "no workspace cwd; file rollback skipped", nil
	}
	all, err := ListStoredTurnDiffs(sessionDir)
	if err != nil {
		return "", err
	}

	// Collapse the selected turns to their endpoints. Preflight every file
	// before writing any, and allow already-restored endpoints on a retry.
	byPath := make(map[string]*WorkspaceChange)
	for _, n := range all {
		if n <= afterTurn {
			break
		}
		diff, err := LoadWorkspaceDiff(sessionDir, n)
		if err != nil {
			return "", fmt.Errorf("load turn %d: %w", n, err)
		}
		if diff == nil {
			continue
		}
		if diff.CWD != "" && !sameWorkspace(cwd, diff.CWD) {
			return "", fmt.Errorf("%w: turn %d belongs to another workspace", ErrWorkspaceConflict, n)
		}
		for _, ch := range diff.Changes {
			if existing := byPath[ch.Path]; existing != nil {
				existing.Before = ch.Before
			} else {
				copy := ch
				byPath[ch.Path] = &copy
			}
		}
	}
	if len(byPath) == 0 {
		return "no file changes to roll back", nil
	}
	diff := &WorkspaceDiff{}
	for _, ch := range byPath {
		diff.Changes = append(diff.Changes, *ch)
	}
	sort.Slice(diff.Changes, func(i, j int) bool { return diff.Changes[i].Path < diff.Changes[j].Path })
	restored, err := reverseWorkspaceDiff(cwd, diff)
	return fmt.Sprintf("restored %d file(s)", restored), err
}

// ErrWorkspaceConflict means rollback would overwrite a later edit or a file
// outside the workspace captured by the session.
var ErrWorkspaceConflict = errors.New("workspace changed since the recorded turn")

func sameWorkspace(a, b string) bool {
	canonical := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		p = filepath.Clean(p)
		if runtime.GOOS == "windows" {
			p = strings.ToLower(p)
		}
		return p
	}
	return canonical(a) == canonical(b)
}

func workspaceFilesEqual(a, b *WorkspaceFile) bool {
	if a == nil || b == nil {
		return a == b
	}
	return bytes.Equal(a.Content, b.Content) &&
		(runtime.GOOS == "windows" || a.Mode.Perm() == b.Mode.Perm())
}

// reverseWorkspaceDiff restores each file to its Before state (or removes if Before is nil),
// and reports how many it wrote.
func reverseWorkspaceDiff(cwd string, diff *WorkspaceDiff) (int, error) {
	root, err := os.OpenRoot(cwd)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	var pending []WorkspaceChange
	for _, ch := range diff.Changes {
		if IsToolStatePath(ch.Path) {
			// Recorded by an older build. Putting wc.db or .git/index back to
			// what it held before the session would leave the client describing
			// a tree that is no longer there - a rollback must not break the
			// working copy it was asked to clean up.
			continue
		}
		if !filepath.IsLocal(ch.Path) {
			return 0, fmt.Errorf("%w: invalid path %q", ErrWorkspaceConflict, ch.Path)
		}
		var current *WorkspaceFile
		info, err := root.Lstat(ch.Path)
		if err == nil {
			if !info.Mode().IsRegular() {
				return 0, fmt.Errorf("%w: %s is not a regular file", ErrWorkspaceConflict, ch.Path)
			}
			content, err := root.ReadFile(ch.Path)
			if err != nil {
				return 0, err
			}
			current = &WorkspaceFile{Content: content, Mode: info.Mode()}
		} else if !os.IsNotExist(err) {
			return 0, err
		}
		if workspaceFilesEqual(current, ch.Before) {
			continue
		}
		if !workspaceFilesEqual(current, ch.After) {
			return 0, fmt.Errorf("%w: %s", ErrWorkspaceConflict, ch.Path)
		}
		pending = append(pending, ch)
	}
	restored := 0
	for _, ch := range pending {
		if ch.Before == nil {
			// File was created during the turn; delete it.
			if err := root.Remove(ch.Path); err != nil && !os.IsNotExist(err) {
				return restored, err
			}
		} else {
			// File was modified or deleted; restore original content.
			if err := root.MkdirAll(filepath.Dir(ch.Path), 0o755); err != nil {
				return restored, err
			}
			mode := ch.Before.Mode.Perm()
			if mode == 0 {
				mode = 0o644
			}
			if err := root.WriteFile(ch.Path, ch.Before.Content, mode); err != nil {
				return restored, err
			}
			if err := root.Chmod(ch.Path, mode); err != nil {
				return restored, err
			}
		}
		restored++
	}
	return restored, nil
}
