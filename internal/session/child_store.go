package session

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ChildSessionsDirName is the folder inside a session bundle that holds the
// sessions it spawned. A subagent run is a session of the conversation that
// started it, so its bundle lives at
// <root>/<parent>/subagents/<child>, and a child of that child one level
// deeper again. Nothing about the id says so: every session is minted by
// NewSessionID whoever started it, and where the bundle sits is what says
// whose work it was.
const ChildSessionsDirName = "subagents"

// maxChildNesting bounds the walk that indexes child bundles. Nothing in a
// directory tree can loop - a symlink is not a directory to os.ReadDir - so
// this is a guard against a hand-made tree, not a limit on how deep spawning
// may nest. It sits far above any subagents.max_depth an operator would set,
// because a bundle past it would resolve to the sessions root instead: the
// two numbers are related, and this one has to stay the larger.
const maxChildNesting = 128

// childRescanInterval is the shortest gap between two full walks of the
// sessions root. The index is kept up to date by EnsureChildLayout for every
// child this process creates, so a walk is only needed for a bundle written
// by somebody else - another process, or a previous run. Bounding its rate
// keeps a stream of lookups for ids that do not exist from re-walking the
// tree once per request.
const childRescanInterval = 2 * time.Second

// lookupChildDir returns the indexed directory of a child session, and only
// while a bundle is still there. An entry the index kept after the bundle was
// removed - by a delete this process raced, or by another process entirely -
// is dropped rather than answered with, so a path never outlives what it
// pointed at.
func (f *FileStore) lookupChildDir(sessionID string) (string, bool) {
	if f == nil {
		return "", false
	}
	f.childMu.RLock()
	dir, ok := f.childDirs[sessionID]
	f.childMu.RUnlock()
	if !ok {
		return "", false
	}
	if isBundleDir(dir) {
		return dir, true
	}
	f.ForgetChildDir(sessionID)
	return "", false
}

// rememberChildDir records where a child bundle lives, so SessionPath resolves
// it without walking the tree. A name that could not be a session id never
// enters the index: the walk reads whatever the filesystem holds, and a
// hand-made folder must not become a path anything answers with.
func (f *FileStore) rememberChildDir(sessionID, dir string) {
	if f == nil || sessionID == "" || dir == "" {
		return
	}
	if err := ValidateFolderSessionID(sessionID); err != nil {
		return
	}
	f.childMu.Lock()
	defer f.childMu.Unlock()
	if f.childDirs == nil {
		f.childDirs = map[string]string{}
	}
	f.childDirs[sessionID] = dir
}

// ForgetChildDir drops a child session from the index. A caller that removed
// the bundle uses it so a later id collision cannot be answered from the path
// the deleted session had.
func (f *FileStore) ForgetChildDir(sessionID string) {
	if f == nil || sessionID == "" {
		return
	}
	f.childMu.Lock()
	defer f.childMu.Unlock()
	delete(f.childDirs, sessionID)
}

// resolveChildDir answers where a child bundle lives, walking the sessions
// root at most once per childRescanInterval when the index does not know it.
//
// The walk is serialized: one caller does it and the others wait on the same
// lock, then find the interval fresh and return. Without that, a burst of
// requests naming ids nobody has ever stored - which anyone who can reach the
// HTTP surface can send - would each start a full walk of the tree.
func (f *FileStore) resolveChildDir(sessionID string, forceScan bool) (string, bool) {
	if dir, ok := f.lookupChildDir(sessionID); ok {
		return dir, true
	}
	if f == nil || f.Root == "" {
		return "", false
	}

	f.childScanMu.Lock()
	defer f.childScanMu.Unlock()

	// Somebody else may have walked while this call waited for the lock.
	if dir, ok := f.lookupChildDir(sessionID); ok {
		return dir, true
	}
	if !forceScan {
		f.childMu.RLock()
		fresh := !f.childScanned.IsZero() && time.Since(f.childScanned) < childRescanInterval
		f.childMu.RUnlock()
		if fresh {
			return "", false
		}
	}

	found := f.scanChildDirs()

	f.childMu.Lock()
	if f.childDirs == nil {
		f.childDirs = map[string]string{}
	}
	for id, dir := range found {
		f.childDirs[id] = dir
	}
	f.childScanned = time.Now()
	f.childMu.Unlock()

	// The walk is what the index now says; a bundle removed while it ran is
	// dropped again by the check in lookupChildDir.
	return f.lookupChildDir(sessionID)
}

// scanChildDirs walks every bundle under Root and returns the directory of
// each session nested inside another one.
func (f *FileStore) scanChildDirs() map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(f.Root)
	if err != nil {
		return out
	}
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		collectChildDirs(filepath.Join(f.Root, ent.Name()), out, 0)
	}
	return out
}

// collectChildDirs adds every session nested under dir to out, depth first.
func collectChildDirs(dir string, out map[string]string, depth int) {
	if depth >= maxChildNesting {
		return
	}
	kids := filepath.Join(dir, ChildSessionsDirName)
	entries, err := os.ReadDir(kids)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		child := filepath.Join(kids, ent.Name())
		if ValidateFolderSessionID(ent.Name()) == nil {
			out[ent.Name()] = child
		}
		collectChildDirs(child, out, depth+1)
	}
}

// childBundleDirs returns the directory of every session nested directly
// inside dir, in lexical order, and records each one in the index.
func (f *FileStore) childBundleDirs(dir string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, ChildSessionsDirName))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		child := filepath.Join(dir, ChildSessionsDirName, ent.Name())
		f.rememberChildDir(ent.Name(), child)
		out = append(out, child)
	}
	return out
}

// EnsureChildLayout builds the bundle of a session spawned by another one,
// inside the parent's own bundle, and returns its directory. It is the only
// way a nested bundle is created: the index learns the path here, so every
// later SessionPath for that id answers without a walk.
func (f *FileStore) EnsureChildLayout(parentSessionID, childSessionID string) (string, error) {
	if err := ValidateFolderSessionID(parentSessionID); err != nil {
		return "", err
	}
	if err := ValidateFolderSessionID(childSessionID); err != nil {
		return "", err
	}
	dir := filepath.Join(f.SessionPath(parentSessionID), ChildSessionsDirName, childSessionID)
	if err := f.ensureLayoutAt(childSessionID, dir); err != nil {
		return "", err
	}
	f.rememberChildDir(childSessionID, dir)
	return dir, nil
}

// childBundleParent names the session a bundle at dir was spawned by, read off
// the path: a child lives at <parent>/<ChildSessionsDirName>/<child>.
//
// A bundle sitting directly in the sessions root is a session somebody started,
// whatever that root is called: an operator who points sessions.dir at a folder
// named "subagents" would otherwise have every session of it read as a
// delegated run. Above that, the folder holding the children must be inside a
// bundle of its own.
func (f *FileStore) childBundleParent(dir string) (string, bool) {
	children := filepath.Dir(dir)
	if f != nil && f.Root != "" && children == filepath.Clean(f.Root) {
		return "", false
	}
	if filepath.Base(children) != ChildSessionsDirName {
		return "", false
	}
	parentDir := filepath.Dir(children)
	parent := filepath.Base(parentDir)
	if parent == "" || parent == "." || parent == string(filepath.Separator) {
		return "", false
	}
	if !isBundleDir(parentDir) {
		return "", false
	}
	return parent, true
}
