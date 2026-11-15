package preview

// The artifact root of the published-preview channel: where a publish
// materialises a copy of finished work, under what name, and for how long.
//
// A published artifact is a copy, not a view of the project. The preview
// server answers from the project's own files, which is what makes an edit
// show on reload; an artifact behind somebody's reverse proxy must not change
// under the people reading it because the agent rebuilt the branch, and must
// not reach anything the publish did not copy. So a publish walks the source
// once, writes into a staging directory, and only then renames that directory
// into place: until the rename nothing is reachable, and after it everything
// is. A failed publish leaves the previous artifact exactly as it was.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/webfs"
)

const (
	// manifestName records what an artifact is, inside the artifact itself, so
	// the publish that creates it is one rename and nothing is ever half
	// described. It is dot-named, so the origin never serves it.
	manifestName = ".coddy-preview.json"
	// stagingDir is where a publish builds the artifact before the rename, and
	// trashDir is where the artifact it replaces waits for its removal. Both
	// are dot-named for the same reason the manifest is.
	stagingDir = ".staging"
	trashDir   = ".trash"
	// maxArtifactFiles bounds one artifact's file count. The byte budget is
	// the operator's (tools.preview_publish.max_bytes); this one is against a
	// source tree that is wide rather than large.
	maxArtifactFiles = 20000
	// slugLimit caps the readable half of a named artifact's id.
	slugLimit = 40
	// leftoverGrace is how long a staging or trash directory of a publish that
	// died mid-way is left alone before a sweep removes it.
	leftoverGrace = time.Hour
)

// publishMu serializes the publishes of one process: a publish is a
// read-modify-write of one directory name, and two calls naming the same
// artifact would otherwise race between the rename of the old one and the
// rename of the new.
var publishMu sync.Mutex

// artifact is one published preview, as its manifest records it.
type artifact struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	SessionID string `json:"session_id"`
	// Source is where the copy was taken from, for the operator reading a
	// manifest and for the tool result.
	Source string `json:"source"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	// Skipped counts what the copy left out: a link that leaves the source or
	// names a directory or a dot-file, a socket, the artifact root itself.
	Skipped     int       `json:"skipped,omitempty"`
	PublishedAt time.Time `json:"published_at"`
	// ExpiresAt is when the origin stops answering for this artifact and a
	// sweep removes it. Zero means it stays until somebody unpublishes it.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// expired reports whether the artifact's lifetime has run out at now.
func (a artifact) expired(now time.Time) bool {
	return !a.ExpiresAt.IsZero() && !now.Before(a.ExpiresAt)
}

// store is one artifact root.
type store struct{ root string }

// artifactPath is where an artifact of this id lives.
func (s store) artifactPath(id string) string { return filepath.Join(s.root, id) }

// slug is the readable half of a named artifact's id: what the operator sees
// in the address. Anything that is not a letter, a digit or a dash becomes a
// dash, so a name the model chose cannot reach outside one path element.
func slug(name string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() < slugLimit {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= slugLimit {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// artifactID is the path element an artifact is addressed under. A named one
// is the slug of its name plus a digest of the session and that name, so the
// same session publishing the same name twice replaces its own artifact while
// another session publishing that name gets one of its own and can neither
// read nor overwrite it. An unnamed one is random: nothing about it is
// guessable from what the session published.
func artifactID(sessionID, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", fmt.Errorf("preview id: %w", err)
		}
		return hex.EncodeToString(raw[:]), nil
	}
	s := slug(name)
	if s == "" {
		return "", fmt.Errorf("name %q has no letters or digits to make an address out of", name)
	}
	sum := sha256.Sum256([]byte(sessionID + "\x00" + s))
	return s + "-" + hex.EncodeToString(sum[:6]), nil
}

// readManifest reads one artifact's manifest.
func (s store) readManifest(id string) (artifact, error) {
	raw, err := os.ReadFile(filepath.Join(s.artifactPath(id), manifestName))
	if err != nil {
		return artifact{}, err
	}
	var a artifact
	if err := json.Unmarshal(raw, &a); err != nil {
		return artifact{}, fmt.Errorf("preview %s: unreadable manifest: %w", id, err)
	}
	return a, nil
}

// publishRequest is one call to the publishing tool, resolved.
type publishRequest struct {
	// Source is the directory to copy, already checked to be the session's own.
	Source string
	// SessionID owns the artifact: only it may replace or remove it.
	SessionID string
	// Name makes the address stable across publishes. Empty mints a new one.
	Name string
	// TTL is how long the artifact lives. Zero keeps it until it is unpublished.
	TTL time.Duration
	// MaxBytes bounds the copy. A source over it is refused whole.
	MaxBytes int64
	// Now is the clock, so a test does not have to wait out a lifetime.
	Now time.Time
}

// publish materialises req.Source as an artifact and returns it. Nothing is
// reachable under the returned id until every byte is written.
func (s store) publish(req publishRequest) (artifact, error) {
	id, err := artifactID(req.SessionID, req.Name)
	if err != nil {
		return artifact{}, err
	}
	if err := s.sourceIsOutsideRoot(req.Source); err != nil {
		return artifact{}, err
	}

	publishMu.Lock()
	defer publishMu.Unlock()

	if err := os.MkdirAll(filepath.Join(s.root, stagingDir), 0o700); err != nil {
		return artifact{}, fmt.Errorf("preview root %s: %w", s.root, err)
	}
	staging, err := os.MkdirTemp(filepath.Join(s.root, stagingDir), "pub-")
	if err != nil {
		return artifact{}, fmt.Errorf("preview staging: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	files, bytes, skipped, err := copyTree(req.Source, staging, s.root, req.MaxBytes)
	if err != nil {
		return artifact{}, err
	}

	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	a := artifact{
		ID: id, Name: strings.TrimSpace(req.Name), SessionID: req.SessionID,
		Source: req.Source, Files: files, Bytes: bytes, Skipped: skipped, PublishedAt: now.UTC(),
	}
	if req.TTL > 0 {
		a.ExpiresAt = now.Add(req.TTL).UTC()
	}
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return artifact{}, err
	}
	if err := os.WriteFile(filepath.Join(staging, manifestName), raw, 0o600); err != nil {
		return artifact{}, fmt.Errorf("preview manifest: %w", err)
	}

	// An artifact of another session under this id cannot happen - the id
	// carries the session - but an artifact root is a directory on a disk, and
	// one that arrived some other way is not ours to overwrite.
	target := s.artifactPath(id)
	if prev, err := s.readManifest(id); err == nil && prev.SessionID != req.SessionID {
		return artifact{}, fmt.Errorf("preview %s belongs to another session", id)
	}
	replaced := ""
	if _, err := os.Lstat(target); err == nil {
		if err := os.MkdirAll(filepath.Join(s.root, trashDir), 0o700); err != nil {
			return artifact{}, err
		}
		replaced, err = os.MkdirTemp(filepath.Join(s.root, trashDir), "old-")
		if err != nil {
			return artifact{}, err
		}
		// MkdirTemp made the directory; the rename needs the name free.
		if err := os.Remove(replaced); err != nil {
			return artifact{}, err
		}
		if err := os.Rename(target, replaced); err != nil {
			return artifact{}, fmt.Errorf("preview %s: replacing: %w", id, err)
		}
	}
	if err := os.Rename(staging, target); err != nil {
		// Put the previous artifact back rather than leave the address empty.
		if replaced != "" {
			_ = os.Rename(replaced, target)
		}
		return artifact{}, fmt.Errorf("preview %s: publishing: %w", id, err)
	}
	if replaced != "" {
		_ = os.RemoveAll(replaced)
	}
	return a, nil
}

// sourceIsOutsideRoot refuses a publish of the artifact root itself or of
// anything inside it: an artifact that holds the root would copy itself.
func (s store) sourceIsOutsideRoot(source string) error {
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		// No root yet: the first publish creates it, and nothing can be inside it.
		return nil
	}
	rel, err := filepath.Rel(root, source)
	if err != nil {
		return nil
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("%s is inside the artifact root %s; publish the work, not the published copies", source, s.root)
	}
	return nil
}

// validArtifactID reports whether id is a path element this package could
// have minted. The id of a call reaches os.RemoveAll, and preview_unpublish
// takes one without a permission prompt, so nothing else may pass: not a
// traversal, not a nested path, not a dot-name of the root's own bookkeeping.
func validArtifactID(id string) bool {
	if id == "" || id != filepath.Base(id) || len(id) > slugLimit+16 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return false
	}
	return !strings.HasPrefix(id, "-") && !strings.HasSuffix(id, "-")
}

// remove takes an artifact down. Only the session that published it may.
func (s store) remove(id, sessionID string) error {
	if !validArtifactID(id) {
		return fmt.Errorf("%q is not the id of a published preview", id)
	}
	a, err := s.readManifest(id)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no published preview %s", id)
		}
		return err
	}
	if a.SessionID != sessionID {
		return fmt.Errorf("preview %s was published by another session", id)
	}
	publishMu.Lock()
	defer publishMu.Unlock()
	return os.RemoveAll(s.artifactPath(id))
}

// list reads every artifact of the root, newest first.
func (s store) list() []artifact {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	out := make([]artifact, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if a, err := s.readManifest(e.Name()); err == nil {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[j].PublishedAt.Before(out[i].PublishedAt) })
	return out
}

// sweep removes the artifacts whose lifetime has run out, and the staging and
// trash directories of a publish that died before it could clean up after
// itself. It returns how many artifacts it removed.
func (s store) sweep(now time.Time) int {
	publishMu.Lock()
	defer publishMu.Unlock()
	removed := 0
	for _, a := range s.list() {
		if a.expired(now) {
			if os.RemoveAll(s.artifactPath(a.ID)) == nil {
				removed++
			}
		}
	}
	for _, dir := range []string{stagingDir, trashDir} {
		entries, err := os.ReadDir(filepath.Join(s.root, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil || now.Sub(info.ModTime()) < leftoverGrace {
				continue
			}
			_ = os.RemoveAll(filepath.Join(s.root, dir, e.Name()))
		}
	}
	return removed
}

// copyTree copies src into dst, and reports how many files and bytes it wrote
// and how many entries it left out. What it leaves behind is as much a part of
// the contract as what it copies:
//
//   - a dot-named path is never materialised, and neither is a symlink that
//     resolves to one, so `.env` and `.git` cannot be published by accident
//     under a name that does not look like theirs;
//   - a symlink is followed only while it stays inside the source and names a
//     regular file, so a link to /etc/passwd publishes nothing and a link to a
//     directory is left out rather than walked, which keeps a cycle out of the
//     copy;
//   - anything that is not a regular file or a directory is skipped, because an
//     artifact is bytes a static server reads, not a device or a socket;
//   - skip is the artifact root itself when it happens to lie inside the
//     source: an artifact that holds the published copies of other sessions
//     would hand them to whoever has this one's address.
//
// Everything it leaves out is counted, and the tool says so in its answer: a
// page whose assets quietly did not come is worse than one that did not
// publish.
func copyTree(src, dst, artifactRoot string, maxBytes int64) (files int, written int64, skipped int, err error) {
	root, err := filepath.EvalSymlinks(src)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("cannot publish %s: %w", src, err)
	}
	keepOut := ""
	if resolved, rerr := filepath.EvalSymlinks(artifactRoot); rerr == nil && insideRoot(root, resolved) {
		keepOut = resolved
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if keepOut != "" && path == keepOut {
			skipped++
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				skipped++
				return nil
			}
			if !insideRoot(root, resolved) || hiddenInside(root, resolved) {
				skipped++
				return nil
			}
			if info, err = os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
				skipped++
				return nil
			}
			path = resolved
		}
		if !info.Mode().IsRegular() {
			skipped++
			return nil
		}
		if files+1 > maxArtifactFiles {
			return fmt.Errorf("%s holds more than %d files; publish a built directory rather than a whole tree", src, maxArtifactFiles)
		}
		if maxBytes > 0 && written+info.Size() > maxBytes {
			return fmt.Errorf("%s is larger than the %d bytes one published preview may hold (tools.preview_publish.max_bytes)", src, maxBytes)
		}
		n, err := copyFile(path, target)
		if err != nil {
			return err
		}
		files++
		written += n
		return nil
	})
	if err != nil {
		return 0, 0, 0, err
	}
	return files, written, skipped, nil
}

// hiddenInside reports whether path, resolved and inside root, lies under a
// dot-named element of it. A symlink is the one way a request for a name that
// looks ordinary reaches a file the dot rule exists to keep out.
func hiddenInside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	return webfs.HiddenPathElement(filepath.ToSlash(rel))
}

// insideRoot reports whether path is root or below it, both already resolved.
func insideRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyFile writes one file of the artifact. The mode is the artifact's, not
// the source's: everything below the root is read by a static server and by
// nobody else.
func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}
