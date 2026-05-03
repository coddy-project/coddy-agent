package memory

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

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

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n..."
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
			sc := scoreOverlap(query, filepath.Base(p)+" "+body)
			if sc <= 0 {
				continue
			}
			rel, err := filepath.Rel(r.root, p)
			if err != nil {
				rel = p
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
	root := ""
	switch parts[0] {
	case "global":
		root = s.globalRoot
	case "project":
		root = s.projectRoot
	default:
		return "", fmt.Errorf("unknown scope %q", parts[0])
	}
	rest := filepath.FromSlash(parts[1])
	rest = filepath.Clean(rest)
	if strings.HasPrefix(rest, "..") {
		return "", fmt.Errorf("invalid relative path")
	}
	abs = filepath.Join(root, rest)
	abs = filepath.Clean(abs)
	rootClean := filepath.Clean(root)
	rel2, err := filepath.Rel(rootClean, abs)
	if err != nil || strings.HasPrefix(rel2, "..") {
		return "", fmt.Errorf("path escapes root")
	}
	return abs, nil
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

func slugify(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
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
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// Write saves markdown body under the given scope with a filename derived from title.
func (s *Store) Write(scope, title, body string) (writtenPath string, err error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	var root string
	switch scope {
	case "global":
		root = s.globalRoot
	case "project":
		root = s.projectRoot
	default:
		return "", fmt.Errorf("scope must be global or project")
	}
	if err := s.ensureDir(root); err != nil {
		return "", err
	}
	name := slugify(title) + ".md"
	abs := filepath.Join(root, name)
	if err := os.WriteFile(abs, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return scope + ":" + name, nil
	}
	return scope + ":" + filepath.ToSlash(rel), nil
}

// Delete removes a memory file by scope:relative path.
func (s *Store) Delete(rel string) error {
	abs, err := s.resolveReadable(rel)
	if err != nil {
		return err
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
