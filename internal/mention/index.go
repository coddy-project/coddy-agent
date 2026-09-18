package mention

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxIndexEntries caps a workspace index: a checkout larger than that is
// still searchable, only its tail is not offered.
const MaxIndexEntries = 50000

// Entry is one indexed workspace path, relative to the root with forward
// slashes. A directory ends with "/".
type Entry struct {
	Path string
	Dir  bool
}

// Index sources.
const (
	SourceGit  = "git"
	SourceWalk = "walk"
)

// skippedDirs are never descended into by the fallback walk. Inside a git
// checkout the ignore files decide instead.
var skippedDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, ".coddy": true,
	"__pycache__": true, ".venv": true, ".tox": true, ".mypy_cache": true,
	".pytest_cache": true, ".next": true, ".gradle": true,
}

// SkippedDir reports whether a folder of this name is left out of an index
// built by walking (inside a git checkout the ignore files decide instead).
func SkippedDir(name string) bool { return skippedDirs[name] }

// BuildIndex lists root for completion. Inside a git checkout it asks git,
// which honours .gitignore, the info/exclude file and the global excludes and
// still offers dotfiles such as .github/workflows/ci.yml; elsewhere it walks
// the tree, skipping hidden folders, version-control folders and dependency
// caches. Folders
// are listed too, derived from the files they hold. At most limit entries are
// returned; truncated says the tree held more.
func BuildIndex(ctx context.Context, root string, limit int) (entries []Entry, source string, truncated bool, err error) {
	if limit <= 0 {
		limit = MaxIndexEntries
	}
	if files, trunc, gitErr := gitListFiles(ctx, root, limit); gitErr == nil {
		return withDirs(files, limit), SourceGit, trunc, nil
	}
	entries, truncated, err = walkListFiles(ctx, root, limit)
	return entries, SourceWalk, truncated, err
}

// ListGitFolder lists dir as git offers it at this moment: tracked files and
// untracked ones that are not ignored, deleted ones left out, with the folders
// they imply, relative to dir. ok is false outside a git checkout or without
// git, where a caller walks the folder itself. A mention reads a folder with
// it rather than from an IndexCache, whose snapshot may predate the folder.
func ListGitFolder(ctx context.Context, dir string, limit int) (entries []Entry, truncated, ok bool) {
	if limit <= 0 {
		limit = MaxIndexEntries
	}
	files, truncated, err := gitListFiles(ctx, dir, limit)
	if err != nil {
		return nil, false, false
	}
	return withDirs(files, limit), truncated, true
}

var errNoGit = errors.New("not a git checkout")

// gitListFiles returns the tracked and untracked-but-not-ignored files of
// root, minus the tracked ones already deleted from disk.
func gitListFiles(ctx context.Context, root string, limit int) ([]string, bool, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, false, errNoGit
	}
	files, truncated, err := runGitZ(ctx, gitPath, root, limit, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, false, err
	}
	deleted, _, err := runGitZ(ctx, gitPath, root, 0, "ls-files", "-z", "--deleted")
	if err == nil && len(deleted) > 0 {
		gone := make(map[string]bool, len(deleted))
		for _, d := range deleted {
			gone[d] = true
		}
		kept := files[:0]
		for _, f := range files {
			if !gone[f] {
				kept = append(kept, f)
			}
		}
		files = kept
	}
	// An unmerged file is listed once per stage.
	sort.Strings(files)
	uniq := files[:0]
	for i, f := range files {
		if i == 0 || f != files[i-1] {
			uniq = append(uniq, f)
		}
	}
	return uniq, truncated, nil
}

// runGitZ runs git in root and splits its NUL-separated output, stopping
// after limit records when limit is positive.
func runGitZ(ctx context.Context, gitPath, root string, limit int, args ...string) ([]string, bool, error) {
	cmd := exec.CommandContext(ctx, gitPath, append([]string{"-C", root, "-c", "core.quotepath=off"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	var out []string
	truncated := false
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	sc.Split(splitNUL)
	for sc.Scan() {
		if limit > 0 && len(out) >= limit {
			truncated = true
			break
		}
		if rec := sc.Text(); rec != "" {
			out = append(out, rec)
		}
	}
	if truncated {
		_ = cmd.Process.Kill()
		_, _ = io.Copy(io.Discard, stdout)
		_ = cmd.Wait()
		return out, true, nil
	}
	if err := cmd.Wait(); err != nil {
		return nil, false, errNoGit
	}
	return out, false, nil
}

func splitNUL(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// withDirs adds the folders the files live in, so "internal/age" offers
// "internal/agent/" too.
func withDirs(files []string, limit int) []Entry {
	out := make([]Entry, 0, len(files)+len(files)/8)
	seen := make(map[string]bool)
	for _, f := range files {
		f = filepath.ToSlash(f)
		for i := 0; i < len(f); i++ {
			if f[i] != '/' {
				continue
			}
			dir := f[:i+1]
			if !seen[dir] {
				seen[dir] = true
				out = append(out, Entry{Path: dir, Dir: true})
			}
		}
		out = append(out, Entry{Path: f})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// walkListFiles walks root breadth-first enough to be fair: WalkDir's lexical
// order, capped by entry count, with the skipped folders pruned.
func walkListFiles(ctx context.Context, root string, limit int) ([]Entry, bool, error) {
	var out []Entry
	truncated := false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p == root {
			return nil
		}
		if len(out) >= limit {
			truncated = true
			return filepath.SkipAll
		}
		name := d.Name()
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// Without git to say what is ignored, a hidden folder (.cache,
			// .local, .idea) is somebody's state, not the project.
			if skippedDirs[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			out = append(out, Entry{Path: rel + "/", Dir: true})
			return nil
		}
		out = append(out, Entry{Path: rel})
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return nil, false, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, truncated, nil
}

// Snapshot is what an index cache holds for one root.
type Snapshot struct {
	Entries   []Entry
	Source    string
	Truncated bool
	BuiltAt   time.Time
	// Building says a rebuild is running: the entries are the previous
	// build's, or empty when the first one has not finished yet.
	Building bool
}

// IndexCache keeps one index per workspace root. A read never waits for a
// rebuild once the root was indexed: it gets the last build and a fresh one
// starts in the background when the last is older than its time to live
// (at least MinIndexTTL, longer for a tree that took long to list). Refresh
// forces that rebuild - what a surface does when a mention starts, so a file
// written a moment ago is offered - and Subscribe says when a build landed.
type IndexCache struct {
	mu    sync.Mutex
	roots map[string]*rootIndex
	subs  map[int]func(root string)
	subID int
	// Build and Now are replaceable in tests.
	Build func(ctx context.Context, root string) ([]Entry, string, bool, error)
	Now   func() time.Time
}

// MinIndexTTL is how long a build is served without a rebuild.
const MinIndexTTL = 2 * time.Second

// indexIdleEvict drops the index of a root nobody asked about for this long.
const indexIdleEvict = 10 * time.Minute

type rootIndex struct {
	snap     Snapshot
	ttl      time.Duration
	built    bool
	building bool
	done     chan struct{}
	lastUsed time.Time
}

// NewIndexCache returns an empty cache over BuildIndex.
func NewIndexCache() *IndexCache {
	return &IndexCache{roots: map[string]*rootIndex{}, subs: map[int]func(string){}}
}

var defaultIndexes = NewIndexCache()

// DefaultIndexes is the process-wide cache every surface shares.
func DefaultIndexes() *IndexCache { return defaultIndexes }

func (c *IndexCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get returns the index of root. The first call for a root waits up to wait
// for the first build and returns whatever is there by then; every later call
// returns at once.
func (c *IndexCache) Get(root string, wait time.Duration) Snapshot {
	return c.get(root, wait, false)
}

// Refresh is Get that starts a rebuild even when the last build is fresh.
func (c *IndexCache) Refresh(root string, wait time.Duration) Snapshot {
	return c.get(root, wait, true)
}

func (c *IndexCache) get(root string, wait time.Duration, force bool) Snapshot {
	root = filepath.Clean(root)
	c.mu.Lock()
	now := c.now()
	c.evictIdleLocked(now)
	ri := c.roots[root]
	if ri == nil {
		ri = &rootIndex{}
		c.roots[root] = ri
	}
	ri.lastUsed = now
	stale := !ri.built || force || now.Sub(ri.snap.BuiltAt) >= ri.ttl
	if stale && !ri.building {
		c.startBuildLocked(root, ri)
	}
	snap := ri.snap
	snap.Building = ri.building
	done := ri.done
	built := ri.built
	c.mu.Unlock()
	if built || wait <= 0 || done == nil {
		return snap
	}
	select {
	case <-done:
	case <-time.After(wait):
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	snap = ri.snap
	snap.Building = ri.building
	return snap
}

func (c *IndexCache) startBuildLocked(root string, ri *rootIndex) {
	ri.building = true
	ri.done = make(chan struct{})
	done := ri.done
	build := c.Build
	if build == nil {
		build = func(ctx context.Context, root string) ([]Entry, string, bool, error) {
			return BuildIndex(ctx, root, MaxIndexEntries)
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		started := c.now()
		entries, source, truncated, err := build(ctx, root)
		took := c.now().Sub(started)
		c.mu.Lock()
		ri.building = false
		if err == nil {
			ri.snap = Snapshot{Entries: entries, Source: source, Truncated: truncated, BuiltAt: c.now()}
			ri.built = true
			// A tree that takes long to list is listed less often.
			ri.ttl = max(MinIndexTTL, min(5*took, time.Minute))
		} else if !ri.built {
			// Nothing to serve: remember the failure as an empty index for a
			// while rather than rebuilding on every keystroke.
			ri.snap = Snapshot{BuiltAt: c.now()}
			ri.built = true
			ri.ttl = MinIndexTTL
		}
		subs := make([]func(string), 0, len(c.subs))
		for _, fn := range c.subs {
			subs = append(subs, fn)
		}
		close(done)
		c.mu.Unlock()
		for _, fn := range subs {
			fn(root)
		}
	}()
}

func (c *IndexCache) evictIdleLocked(now time.Time) {
	for root, ri := range c.roots {
		if !ri.building && now.Sub(ri.lastUsed) > indexIdleEvict {
			delete(c.roots, root)
		}
	}
}

// Subscribe calls fn with the root after every finished build. The returned
// function removes the subscription.
func (c *IndexCache) Subscribe(fn func(root string)) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subID++
	id := c.subID
	c.subs[id] = fn
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.subs, id)
	}
}

// Under returns the entries of snap inside dir (a root-relative folder with
// a trailing "/", or "" for the root), relative to root as they are.
func (s Snapshot) Under(dir string) []Entry {
	if dir == "" || dir == "./" {
		return s.Entries
	}
	lo := sort.Search(len(s.Entries), func(i int) bool { return s.Entries[i].Path >= dir })
	var out []Entry
	for i := lo; i < len(s.Entries) && strings.HasPrefix(s.Entries[i].Path, dir); i++ {
		if s.Entries[i].Path != dir {
			out = append(out, s.Entries[i])
		}
	}
	return out
}
