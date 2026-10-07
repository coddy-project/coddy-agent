//go:build memory

// Package memstorage reads and writes long-term memory files under global and project roots.
package memstorage

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Hit is one ranked memory snippet for recall.
type Hit struct {
	Path    string `json:"path"`
	Scope   string `json:"scope"`
	Score   int    `json:"score"`
	Snippet string `json:"snippet"`
}

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
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "."
	}
	proj := filepath.Join(cwd, "memory")
	return &Store{globalRoot: filepath.Clean(g), projectRoot: filepath.Clean(proj)}, nil
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

func collectFiles(root string) ([]string, error) {
	root = filepath.Clean(root)
	fi, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !fi.IsDir() {
		return nil, nil
	}
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !isMemoryFile(d.Name()) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, nil
}

func tokenize(s string) map[string]int {
	s = strings.ToLower(s)
	var cur strings.Builder
	tokens := make(map[string]int)
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		w := cur.String()
		cur.Reset()
		if len(w) < 2 {
			return
		}
		tokens[w]++
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func scoreOverlap(query, body string) int {
	q := tokenize(query)
	if len(q) == 0 {
		return 0
	}
	b := tokenize(body)
	score := 0
	for w, n := range q {
		if m, ok := b[w]; ok {
			if n < m {
				score += n
			} else {
				score += m
			}
		}
	}
	return score
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

// Search ranks memory files by token overlap with query.
func (s *Store) Search(query string, scope string, maxHits int) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if maxHits <= 0 {
		maxHits = 8
	}
	var roots []struct {
		root  string
		label string
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "global":
		roots = append(roots, struct {
			root  string
			label string
		}{s.globalRoot, "global"})
	case "project":
		roots = append(roots, struct {
			root  string
			label string
		}{s.projectRoot, "project"})
	default:
		roots = append(roots, struct {
			root  string
			label string
		}{s.globalRoot, "global"})
		roots = append(roots, struct {
			root  string
			label string
		}{s.projectRoot, "project"})
	}
	type cand struct {
		path  string
		scope string
		score int
		body  string
	}
	var all []cand
	for _, r := range roots {
		paths, err := collectFiles(r.root)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			body := string(b)
			rel, err := filepath.Rel(r.root, p)
			if err != nil {
				rel = p
			}
			relSlash := filepath.ToSlash(rel)
			pathContext := strings.ReplaceAll(relSlash, "/", " ") + " " + filepath.Base(p)
			sc := scoreOverlap(query, pathContext+" "+body)
			if sc <= 0 {
				continue
			}
			all = append(all, cand{path: r.label + ":" + filepath.ToSlash(rel), scope: r.label, score: sc, body: body})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score == all[j].score {
			return all[i].path < all[j].path
		}
		return all[i].score > all[j].score
	})
	if len(all) > maxHits {
		all = all[:maxHits]
	}
	out := make([]Hit, 0, len(all))
	for _, c := range all {
		out = append(out, Hit{Path: c.path, Scope: c.scope, Score: c.score, Snippet: clip(c.body, 1200)})
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

func (s *Store) resolveScopeRoot(scopeOrPrefix string) (label string, root string, err error) {
	scopeOrPrefix = strings.TrimSpace(scopeOrPrefix)
	if scopeOrPrefix == "" {
		return "", "", fmt.Errorf("missing scope")
	}
	lab, err := scopeLabelFromPrefix(scopeOrPrefix)
	if err != nil {
		return "", "", err
	}
	switch lab {
	case "global":
		return lab, s.globalRoot, nil
	case "project":
		return lab, s.projectRoot, nil
	default:
		return "", "", fmt.Errorf("unknown scope %q", lab)
	}
}

// joinUnderRoot validates rest stays under root (no ".." escapes) and returns absolute path.
func joinUnderRoot(root, rest string) (abs string, err error) {
	root = filepath.Clean(root)
	restPath := filepath.Clean(filepath.FromSlash(strings.TrimSpace(rest)))
	if strings.HasPrefix(restPath, "..") {
		return "", fmt.Errorf("invalid relative path")
	}
	if restPath == "." {
		return root, nil
	}
	abs = filepath.Join(root, restPath)
	abs = filepath.Clean(abs)
	relCheck, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(relCheck, "..") {
		return "", fmt.Errorf("path escapes root")
	}
	return abs, nil
}

func (s *Store) resolveReadable(rel string) (abs string, err error) {
	rel = strings.TrimSpace(rel)
	rel = filepath.ToSlash(rel)
	if rel == "" || strings.Contains(rel, "..") {
		return "", fmt.Errorf("invalid path")
	}
	parts := strings.SplitN(rel, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("path must be scope:relative form, got %q", rel)
	}
	_, root, err := s.resolveScopeRoot(parts[0])
	if err != nil {
		return "", err
	}
	return joinUnderRoot(root, parts[1])
}

// resolveDirAbs returns absolute directory under a memory scope root. inner empty means scope root directory.
func (s *Store) resolveDirAbs(scopeLabel, inner string) (abs string, err error) {
	_, root, err := s.resolveScopeRoot(scopeLabel)
	if err != nil {
		return "", err
	}
	return joinUnderRoot(root, inner)
}

// ListEntry is one row in a flat directory listing.
type ListEntry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
}

// ListOneLevel lists immediate children under scope-relative directory inner (empty string = scope root).
func (s *Store) ListOneLevel(scopeLabel, inner string) ([]ListEntry, error) {
	dirAbs, err := s.resolveDirAbs(scopeLabel, inner)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		return nil, err
	}
	fi, err := os.Stat(dirAbs)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("not a directory")
	}
	de, err := os.ReadDir(dirAbs)
	if err != nil {
		return nil, err
	}
	var nodes []ListEntry
	for _, e := range de {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.IsDir() {
			nodes = append(nodes, ListEntry{Name: name, Kind: "dir", Modified: info.ModTime().UTC().Format(time.RFC3339)})
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".txt" {
			continue
		}
		nodes = append(nodes, ListEntry{Name: name, Kind: "file", Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339)})
	}
	return nodes, nil
}

// Mkdir creates directories under scope (idempotent via MkdirAll).
func (s *Store) Mkdir(scopeLabel, innerDir string) error {
	dirAbs, err := s.resolveDirAbs(scopeLabel, innerDir)
	if err != nil {
		return err
	}
	return s.ensureDir(dirAbs)
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
	abs, err := s.resolveReadable(rel)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(b), nil
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
	scope = strings.ToLower(strings.TrimSpace(scope))
	lab, root, err := s.resolveScopeRoot(scope)
	if err != nil {
		return "", err
	}
	if err := s.ensureDir(root); err != nil {
		return "", err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", fmt.Errorf("empty body")
	}
	content := []byte(body + "\n")

	relInner := strings.TrimSpace(filepath.ToSlash(relativeInner))
	rootClean := filepath.Clean(root)

	var abs string
	if relInner == "" {
		name := slugify(title) + ".md"
		abs = filepath.Join(root, name)
	} else {
		if err := validateMemoryLeafPath(relInner); err != nil {
			return "", err
		}
		relInnerClean := filepath.Clean(filepath.FromSlash(relInner))
		if strings.HasPrefix(relInnerClean, "..") || strings.Contains(filepath.ToSlash(relInnerClean), "/../") {
			return "", fmt.Errorf("invalid relative_path")
		}
		parent := filepath.Dir(relInnerClean)
		if parent != "." {
			if err := s.ensureDir(filepath.Join(root, parent)); err != nil {
				return "", err
			}
		}
		candidate := filepath.Join(root, relInnerClean)
		candidate = filepath.Clean(candidate)
		relProbe, err := filepath.Rel(rootClean, candidate)
		if err != nil || strings.HasPrefix(relProbe, "..") {
			return "", fmt.Errorf("path escapes root")
		}
		abs = candidate
	}
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		return "", err
	}
	toShow, err := filepath.Rel(rootClean, abs)
	if err != nil {
		return lab + ":" + filepath.Base(abs), nil
	}
	return lab + ":" + filepath.ToSlash(toShow), nil
}

// Delete removes a memory file or an entire directory tree under the scope root by scope:relative path.
// Deleting the scope root itself (for example global: or global:.) is not allowed.
func (s *Store) Delete(rel string) error {
	rel = strings.TrimSpace(rel)
	rel = filepath.ToSlash(rel)
	if rel == "" || strings.Contains(rel, "..") {
		return fmt.Errorf("invalid path")
	}
	parts := strings.SplitN(rel, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("path must be scope:relative form, got %q", rel)
	}
	inner := strings.TrimSpace(parts[1])
	if inner == "" || inner == "." {
		return fmt.Errorf("cannot delete scope root")
	}
	abs, err := s.resolveReadable(rel)
	if err != nil {
		return err
	}
	_, root, err := s.resolveScopeRoot(parts[0])
	if err != nil {
		return err
	}
	if filepath.Clean(abs) == filepath.Clean(root) {
		return fmt.Errorf("cannot delete scope root")
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return os.RemoveAll(abs)
	}
	return os.Remove(abs)
}

// HasAnyFiles returns true if either root contains at least one memory file.
func (s *Store) HasAnyFiles() bool {
	for _, root := range []string{s.globalRoot, s.projectRoot} {
		paths, _ := collectFiles(root)
		if len(paths) > 0 {
			return true
		}
	}
	return false
}
