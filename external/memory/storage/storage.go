//go:build memory

// Package memstorage reads and writes long-term memory files under global and project roots.
package memstorage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/docs"
)

// Hit is one ranked memory snippet for recall.
type Hit struct {
	Path    string  `json:"path"`
	Scope   string  `json:"scope"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet"`
}

// GuideName is the file at the root of a scope that guides the memory
// subagent. A search never returns it: the child already has it at the top of
// its task. Anywhere else it is an ordinary note.
const GuideName = "AGENTS.md"

// The fields of a note a search scores, in this order, with their weights
// (BM25F, see docs.Ranker): the words of its folders and file name, and its
// body. A word in the path says what a note is about, so it weighs three
// times a word in the body. The weights are constants, not settings.
const (
	pathWeight = 3.0
	bodyWeight = 1.0
)

// ErrProjectRootLink refuses a project root that is a link, or sits in a
// .coddy folder that is one.
var ErrProjectRootLink = errors.New("project memory: .coddy/memory and the .coddy folder holding it must be real folders, not links")

// Store reads and writes markdown-like memory files under global and project roots.
type Store struct {
	globalRoot  string
	projectRoot string
}

// NewStore resolves filesystem locations from config and paths.
func NewStore(m *config.MemoryConfig, p config.Paths, cwd string) (*Store, error) {
	home := strings.TrimSpace(p.Home)
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("memory store: home: %w", err)
		}
		home = filepath.Join(h, ".coddy")
	}
	g := strings.TrimSpace(m.Dir)
	if g == "" {
		g = filepath.Join(home, "memory")
	}
	return &Store{globalRoot: filepath.Clean(g), projectRoot: ProjectRootFor(cwd)}, nil
}

// ProjectRootFor is the project memory root of a session working in cwd:
// .coddy/memory of that folder, beside the project's other Coddy files
// (.coddy/mcp.json, .coddy/rules, .coddy/agents). An empty cwd stands for
// the current directory.
func ProjectRootFor(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "."
	}
	return filepath.Join(cwd, ".coddy", "memory")
}

// NewWithRoots builds a store from explicit filesystem roots (tests and narrow call sites).
func NewWithRoots(globalRoot, projectRoot string) *Store {
	return &Store{globalRoot: filepath.Clean(globalRoot), projectRoot: filepath.Clean(projectRoot)}
}

func (s *Store) GlobalRoot() string  { return s.globalRoot }
func (s *Store) ProjectRoot() string { return s.projectRoot }

func (s *Store) ensureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

func isMemoryFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".md" || ext == ".txt"
}

// clip cuts s to max characters (runes, not bytes), so a snippet of
// non-Latin text is as long as an ASCII one and never ends in half a
// character.
func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "\n..."
}

// Search ranks the notes of the scope against the query with the BM25F
// ranker of the documentation search (docs.Ranker): words are stemmed,
// a word few notes have counts more than one most have, a word in the path
// counts more than one in the body, and a word the query begins is found.
// The index is built from the files read for this call; memory changes
// between calls and is small, so there is nothing to keep. Equal scores are
// ordered by path, and the root AGENTS.md of a scope is not a note to find.
func (s *Store) Search(query string, scope string, maxHits int) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if maxHits <= 0 {
		maxHits = 8
	}
	var labels []string
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "global":
		labels = []string{"global"}
	case "project":
		labels = []string{"project"}
	default:
		labels = []string{"global", "project"}
	}
	type note struct {
		path  string
		scope string
		rel   string
		body  string
	}
	var notes []note
	for _, label := range labels {
		root, err := s.openRoot(label, false)
		if err != nil {
			// A root that does not exist yet holds no notes. A project root
			// that cannot be opened (a link, a file, no access) is an error
			// when it is the one root asked for; a search of both roots
			// answers from the other.
			if errors.Is(err, fs.ErrNotExist) || (len(labels) > 1 && label == "project") {
				continue
			}
			return nil, err
		}
		// The guide is the file the root answers for GuideName, whatever its
		// case on a case-insensitive filesystem.
		guide, _ := root.Stat(GuideName)
		for _, rel := range collectFiles(root.FS()) {
			if guide != nil && !strings.Contains(rel, "/") {
				if info, err := root.Stat(rel); err == nil && os.SameFile(guide, info) {
					continue
				}
			}
			b, err := root.ReadFile(rel)
			if err != nil {
				continue
			}
			notes = append(notes, note{path: label + ":" + rel, scope: label, rel: rel, body: string(b)})
		}
		_ = root.Close()
	}
	// The ranker orders equal scores by the order notes were added in, so
	// they go in by path.
	sort.Slice(notes, func(i, j int) bool { return notes[i].path < notes[j].path })
	ranker := docs.NewRanker(pathWeight, bodyWeight)
	for _, n := range notes {
		ranker.Add(docs.CountTerms(strings.TrimSuffix(n.rel, path.Ext(n.rel)), n.body))
	}
	ranked, _ := ranker.Rank(query)
	if len(ranked) > maxHits {
		ranked = ranked[:maxHits]
	}
	out := make([]Hit, 0, len(ranked))
	for _, r := range ranked {
		n := notes[r.Doc]
		out = append(out, Hit{Path: n.path, Scope: n.scope, Score: r.Score, Snippet: clip(n.body, 1200)})
	}
	return out, nil
}

func scopeLabelFromPrefix(prefix string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(prefix)) {
	case "global":
		return "global", nil
	case "project":
		return "project", nil
	default:
		return "", fmt.Errorf("unknown scope %q", prefix)
	}
}

// resolveReadable splits a scope:relative path into its scope label and its
// name inside that root.
func (s *Store) resolveReadable(rel string) (label, name string, err error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || strings.Contains(rel, "..") {
		return "", "", fmt.Errorf("invalid path")
	}
	parts := strings.SplitN(rel, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("path must be scope:relative form, got %q", rel)
	}
	if label, err = scopeLabelFromPrefix(parts[0]); err != nil {
		return "", "", err
	}
	name, err = cleanRel(parts[1])
	return label, name, err
}

// openScopeDir resolves a scope label and a folder inside it, and opens the
// root.
func (s *Store) openScopeDir(scopeLabel, inner string, create bool) (rootFS, string, error) {
	label, err := scopeLabelFromPrefix(scopeLabel)
	if err != nil {
		return nil, "", err
	}
	name, err := cleanRel(inner)
	if err != nil {
		return nil, "", err
	}
	root, err := s.openRoot(label, create)
	if err != nil {
		return nil, "", err
	}
	return root, name, nil
}

// ListEntry is one row in a flat directory listing.
type ListEntry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
}

// ListOneLevel lists immediate children under scope-relative directory inner
// (empty string = scope root), creating that folder when it is missing.
func (s *Store) ListOneLevel(scopeLabel, inner string) ([]ListEntry, error) {
	root, name, err := s.openScopeDir(scopeLabel, inner, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(name, 0o755); err != nil {
		return nil, err
	}
	return listDir(root, name)
}

// List lists the immediate children of an existing folder inner of a scope:
// fs.ErrNotExist when it is missing, ErrNotDir when it is a file.
func (s *Store) List(scopeLabel, inner string) ([]ListEntry, error) {
	root, name, err := s.openScopeDir(scopeLabel, inner, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return listDir(root, name)
}

func listDir(root rootFS, name string) ([]ListEntry, error) {
	fi, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, ErrNotDir
	}
	de, err := fs.ReadDir(root.FS(), name)
	if err != nil {
		return nil, err
	}
	var nodes []ListEntry
	for _, e := range de {
		entry := e.Name()
		if strings.HasPrefix(entry, ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.IsDir() {
			nodes = append(nodes, ListEntry{Name: entry, Kind: "dir", Modified: info.ModTime().UTC().Format(time.RFC3339)})
			continue
		}
		if !isMemoryFile(entry) {
			continue
		}
		nodes = append(nodes, ListEntry{Name: entry, Kind: "file", Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339)})
	}
	return nodes, nil
}

// Mkdir creates directories under scope (idempotent via MkdirAll).
func (s *Store) Mkdir(scopeLabel, innerDir string) error {
	root, name, err := s.openScopeDir(scopeLabel, innerDir, true)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.MkdirAll(name, 0o755)
}

func validateMemoryLeafPath(innerFile string) error {
	innerFile = filepath.ToSlash(strings.TrimSpace(innerFile))
	if innerFile == "" || innerFile == "." {
		return fmt.Errorf("missing file path")
	}
	if strings.Contains(innerFile, "..") {
		return fmt.Errorf("invalid path")
	}
	base := filepath.Base(innerFile)
	ext := strings.ToLower(filepath.Ext(base))
	if ext != ".md" && ext != ".txt" {
		return fmt.Errorf("memory file must end with .md or .txt")
	}
	return nil
}

// Read returns file contents for a scope:relative path.
func (s *Store) Read(rel string) (string, error) {
	label, name, err := s.resolveReadable(rel)
	if err != nil {
		return "", err
	}
	root, err := s.openRoot(label, false)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	b, err := root.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ReadNote reads one note for the notes browser: a .md or .txt file, never a
// folder. A note that cannot be reached, whatever the reason, is
// fs.ErrNotExist.
func (s *Store) ReadNote(scopeLabel, inner string) ([]byte, error) {
	root, name, err := s.openScopeDir(scopeLabel, inner, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if name == "." {
		return nil, fs.ErrNotExist
	}
	if !isMemoryFile(name) {
		return nil, ErrNotNote
	}
	info, err := root.Stat(name)
	if err != nil {
		if errors.Is(err, ErrOutsideRoot) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", fs.ErrNotExist, err)
	}
	if info.IsDir() {
		return nil, fs.ErrNotExist
	}
	return root.ReadFile(name)
}

// PutFile writes content as the note inner of a scope, as it is, replacing
// what was there through a temporary file and a rename.
func (s *Store) PutFile(scopeLabel, inner string, content []byte) error {
	root, name, err := s.openScopeDir(scopeLabel, inner, true)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if name == "." {
		return ErrInvalidPath
	}
	if !isMemoryFile(name) {
		return ErrNotNote
	}
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := name + ".tmp"
	if err := root.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	if err := root.Rename(tmp, name); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// slugify turns a title into a file name stem. Letters and digits of any
// script are kept, so two notes titled in Cyrillic do not both land on
// "note.md" and overwrite each other; the stem is cut at 80 characters.
func slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		case r == ' ', r == '-', r == '_':
			if b.Len() > 0 && !dash {
				b.WriteRune('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "note"
	}
	if r := []rune(s); len(r) > 80 {
		s = strings.TrimRight(string(r[:80]), "-")
	}
	return s
}

// Write saves markdown body under the given scope with a filename derived from title (flat layout).
func (s *Store) Write(scope, title, body string) (writtenPath string, err error) {
	return s.WriteFlexible(scope, title, "", body)
}

// WriteFlexible writes body under scope. When relativeInner is empty, filename is slugify(title)+".md" at scope root.
// Otherwise relativeInner is path under scope root, e.g. design/auth-flow.md (.md or .txt only).
func (s *Store) WriteFlexible(scope, title, relativeInner, body string) (writtenPath string, err error) {
	lab, err := scopeLabelFromPrefix(scope)
	if err != nil {
		return "", err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("empty body")
	}
	content := []byte(body + "\n")

	name := slugify(title) + ".md"
	if relInner := strings.TrimSpace(filepath.ToSlash(relativeInner)); relInner != "" {
		if err := validateMemoryLeafPath(relInner); err != nil {
			return "", err
		}
		if name, err = cleanRel(relInner); err != nil {
			return "", fmt.Errorf("invalid relative_path: %w", err)
		}
	}
	root, err := s.openRoot(lab, true)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	if err := root.WriteFile(name, content, 0o644); err != nil {
		return "", err
	}
	return lab + ":" + name, nil
}

// Delete removes a memory file or an entire directory tree under the scope root by scope:relative path.
// Deleting the scope root itself (for example global: or global:.) is not allowed. A link is removed as
// the link, never what it points at.
func (s *Store) Delete(rel string) error {
	if parts := strings.SplitN(filepath.ToSlash(strings.TrimSpace(rel)), ":", 2); len(parts) == 2 {
		if inner := strings.TrimSpace(parts[1]); inner == "" || inner == "." {
			return ErrScopeRoot
		}
	}
	label, name, err := s.resolveReadable(rel)
	if err != nil {
		return err
	}
	if name == "." {
		return ErrScopeRoot
	}
	root, err := s.openRoot(label, false)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(name)
	if err != nil {
		if errors.Is(err, ErrOutsideRoot) {
			return err
		}
		return fmt.Errorf("%w: %w", fs.ErrNotExist, err)
	}
	if info.Mode()&fs.ModeSymlink == 0 && info.IsDir() {
		return root.RemoveAll(name)
	}
	return root.Remove(name)
}

// HasAnyFiles returns true if either root contains at least one memory file.
func (s *Store) HasAnyFiles() bool {
	for _, label := range []string{"global", "project"} {
		root, err := s.openRoot(label, false)
		if err != nil {
			continue
		}
		n := len(collectFiles(root.FS()))
		_ = root.Close()
		if n > 0 {
			return true
		}
	}
	return false
}
