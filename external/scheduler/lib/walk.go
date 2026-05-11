//go:build scheduler

package scheduler

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListFlatJobMarkdownFiles returns *.md job files immediately under each scheduler root (non-recursive, no subfolders).
func ListFlatJobMarkdownFiles(roots []string) ([]string, error) {
	var out []string
	seen := map[string]struct{}{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		de, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, ent := range de {
			if ent.IsDir() {
				continue
			}
			name := ent.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if !strings.HasSuffix(strings.ToLower(name), ".md") {
				continue
			}
			path := filepath.Join(root, name)
			ap, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			if _, ok := seen[ap]; ok {
				continue
			}
			seen[ap] = struct{}{}
			out = append(out, ap)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Deprecated: recursive walk kept for tooling that still discovers nested drafts; daemon and REST use ListFlatJobMarkdownFiles only.
// ListJobMarkdownFiles collects *.md files under scheduler roots (excluding dotfiles).
func ListJobMarkdownFiles(roots []string) ([]string, error) {
	return ListFlatJobMarkdownFiles(roots)
}
