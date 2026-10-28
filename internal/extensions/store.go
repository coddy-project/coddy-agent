// Package extensions stores operator-uploaded UI bundles. It never executes code.
package extensions

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

const MaxArchive = 8 << 20
const maxExpanded = 16 << 20

var ErrConflict = errors.New("extension already installed; remove it before installing another version")
var ErrInvalid = errors.New("invalid extension")
var ErrUnavailable = errors.New("extension unavailable or digest changed")
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
var filePattern = regexp.MustCompile(`^[a-zA-Z0-9_./-]+$`)
var locks sync.Map

type Manifest struct {
	API         int    `json:"api"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	UI          struct {
		Entry  string   `json:"entry"`
		Styles []string `json:"styles,omitempty"`
	} `json:"ui"`
}

type Entry struct {
	Manifest
	SHA256      string `json:"sha256"`
	EntrySHA256 string `json:"entry_sha256"`
	Enabled     bool   `json:"enabled"`
}

type record struct {
	Archive []byte `json:"archive"`
	SHA256  string `json:"sha256"`
	Enabled bool   `json:"enabled"`
}

type bundle struct {
	record
	entry Entry
	files map[string][]byte
}

type Store struct{ dir string }

func NewStore(home string) *Store { return &Store{dir: filepath.Join(home, "extensions")} }

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func validID(id string) bool    { return len(id) <= 32 && idPattern.MatchString(id) }
func validPath(name string) bool {
	return name != "." && name != ".." && !strings.HasPrefix(name, "/") && path.Clean(name) == name && !strings.HasPrefix(name, "../") && filePattern.MatchString(name)
}

func parse(data []byte) (*bundle, error) {
	bad := func(msg string) (*bundle, error) { return nil, fmt.Errorf("%w: %s", ErrInvalid, msg) }
	if len(data) > MaxArchive {
		return bad("ZIP exceeds 8 MiB")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return bad("expected a ZIP archive")
	}
	if len(z.File) > 128 {
		return bad("too many files")
	}
	b := &bundle{record: record{Archive: data, SHA256: digest(data)}, files: map[string][]byte{}}
	seen := map[string]bool{}
	total := 0
	for _, f := range z.File {
		name := f.Name
		if f.FileInfo().IsDir() {
			name = strings.TrimSuffix(name, "/")
		}
		if !validPath(name) || seen[strings.ToLower(name)] || f.Mode()&os.ModeSymlink != 0 {
			return bad("unsafe or duplicate file path")
		}
		seen[strings.ToLower(name)] = true
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() || f.UncompressedSize64 > maxExpanded {
			return bad("unsupported file or file too large")
		}
		r, err := f.Open()
		if err != nil {
			return bad("cannot read ZIP entry")
		}
		body, readErr := io.ReadAll(io.LimitReader(r, int64(maxExpanded-total+1)))
		closeErr := r.Close()
		if readErr != nil || closeErr != nil {
			return bad("damaged ZIP entry")
		}
		total += len(body)
		if total > maxExpanded {
			return bad("expanded ZIP exceeds 16 MiB")
		}
		b.files[name] = body
	}
	var m Manifest
	if err := json.Unmarshal(b.files["manifest.json"], &m); err != nil {
		return bad("manifest.json is missing or invalid")
	}
	if m.API != 1 || !validID(m.ID) || strings.TrimSpace(m.Name) == "" || len(m.Name) > 120 || strings.TrimSpace(m.Version) == "" || len(m.Version) > 64 || len(m.Description) > 2000 {
		return bad("expected api 1, an id (up to 32 lowercase letters, digits and hyphens), name and version")
	}
	if !validPath(m.UI.Entry) || !strings.HasSuffix(m.UI.Entry, ".js") || len(b.files[m.UI.Entry]) == 0 {
		return bad("ui.entry must name a nonempty .js file")
	}
	for _, style := range m.UI.Styles {
		if !validPath(style) || !strings.HasSuffix(style, ".css") || b.files[style] == nil {
			return bad("ui.styles must name CSS files in the archive")
		}
	}
	b.entry = Entry{Manifest: m, SHA256: b.SHA256, EntrySHA256: digest(b.files[m.UI.Entry])}
	return b, nil
}

// withRoot serializes mutations across requests and processes. Rooted file
// operations keep archive data and state inside the operator's extension folder.
func (s *Store) withRoot(fn func(*os.Root) error) error {
	v, _ := locks.LoadOrStore(s.dir, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	unlock, err := platform.LockFile(filepath.Join(s.dir, ".lock"))
	if err != nil {
		return err
	}
	defer unlock()
	root, err := os.OpenRoot(s.dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return fn(root)
}

func read(root *os.Root, id string) (*bundle, error) {
	if !validID(id) {
		return nil, ErrInvalid
	}
	f, err := root.Open(id + ".json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 2*MaxArchive {
		return nil, ErrInvalid
	}
	var r record
	if err := json.NewDecoder(io.LimitReader(f, 2*MaxArchive+1)).Decode(&r); err != nil {
		return nil, err
	}
	b, err := parse(r.Archive)
	if err != nil {
		return nil, err
	}
	if b.entry.ID != id || b.SHA256 != r.SHA256 {
		return nil, ErrUnavailable
	}
	b.Enabled = r.Enabled
	b.entry.Enabled = r.Enabled
	return b, nil
}

func write(root *os.Root, id string, r record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	// The shared lock protects the staging name. A partial write never replaces
	// the installed bundle, including on Windows where Rename replaces a file.
	temp := id + ".tmp"
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		if err = root.Remove(temp); err != nil {
			return err
		}
		f, err = root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(temp, id+".json")
}

func (s *Store) List() ([]Entry, error) {
	out := []Entry{}
	err := s.withRoot(func(root *os.Root) error {
		f, err := root.Open(".")
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		names, err := f.Readdirnames(-1)
		if err != nil {
			return err
		}
		sort.Strings(names)
		for _, name := range names {
			if strings.HasSuffix(name, ".json") {
				b, err := read(root, strings.TrimSuffix(name, ".json"))
				if err != nil {
					return fmt.Errorf("read %s: %w", name, err)
				}
				out = append(out, b.entry)
			}
		}
		return nil
	})
	return out, err
}

func (s *Store) Install(data []byte) (Entry, error) {
	b, err := parse(data)
	if err != nil {
		return Entry{}, err
	}
	err = s.withRoot(func(root *os.Root) error {
		_, err := root.Lstat(b.entry.ID + ".json")
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return write(root, b.entry.ID, b.record)
	})
	return b.entry, err
}

func (s *Store) SetEnabled(id, sha string, enabled bool) (Entry, error) {
	var out Entry
	err := s.withRoot(func(root *os.Root) error {
		b, err := read(root, id)
		if err != nil {
			return err
		}
		if b.SHA256 != sha {
			return ErrUnavailable
		}
		b.Enabled = enabled
		b.entry.Enabled = enabled
		out = b.entry
		return write(root, id, b.record)
	})
	return out, err
}

func (s *Store) Remove(id string) error {
	if !validID(id) {
		return ErrInvalid
	}
	return s.withRoot(func(root *os.Root) error { return root.Remove(id + ".json") })
}

// CheckEnabled authorizes a server-side operation for one approved bundle.
func (s *Store) CheckEnabled(id, sha string) error {
	return s.withRoot(func(root *os.Root) error {
		b, err := read(root, id)
		if err != nil {
			return err
		}
		if !b.Enabled || sha != b.SHA256 {
			return ErrUnavailable
		}
		return nil
	})
}

// Asset returns bytes from the exact approved bundle, without extracting it.
func (s *Store) Asset(id, sha, name string) ([]byte, error) {
	var out []byte
	err := s.withRoot(func(root *os.Root) error {
		b, err := read(root, id)
		if err != nil {
			return err
		}
		if !b.Enabled || sha != b.SHA256 {
			return ErrUnavailable
		}
		data, ok := b.files[name]
		if !ok || !validPath(name) {
			return os.ErrNotExist
		}
		out = data
		return nil
	})
	return out, err
}
