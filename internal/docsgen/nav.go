// Package docsgen generates the parts of the documentation that follow the code
// or the navigation map, and checks that the tree, the map and the generated
// files agree. It is driven by cmd/docsgen (make docs, make docs-check).
package docsgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// NavFile is the navigation map, relative to the repository root.
const NavFile = "docs/nav.yaml"

// Page is one entry of the navigation map. Path is relative to docs/ and may
// climb to the repository root ("../CONTRIBUTING.md").
type Page struct {
	Path    string `yaml:"path"`
	Title   string `yaml:"title"`
	Summary string `yaml:"summary"`
}

// Group is one section of the navigation map.
type Group struct {
	ID      string `yaml:"id"`
	Title   string `yaml:"title"`
	Summary string `yaml:"summary"`
	Pages   []Page `yaml:"pages"`
}

// Nav is the whole navigation map.
type Nav struct {
	Groups []Group `yaml:"groups"`
}

// LoadNav reads and validates docs/nav.yaml under root.
func LoadNav(root string) (*Nav, error) {
	data, err := os.ReadFile(filepath.Join(root, NavFile))
	if err != nil {
		return nil, err
	}
	var nav Nav
	if err := yaml.Unmarshal(data, &nav); err != nil {
		return nil, fmt.Errorf("%s: %w", NavFile, err)
	}
	if err := nav.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", NavFile, err)
	}
	return &nav, nil
}

func (n *Nav) validate() error {
	if len(n.Groups) == 0 {
		return fmt.Errorf("no groups")
	}
	seenGroup := map[string]bool{}
	seenPath := map[string]bool{}
	for _, g := range n.Groups {
		if strings.TrimSpace(g.ID) == "" || strings.TrimSpace(g.Title) == "" {
			return fmt.Errorf("group %q needs an id and a title", g.ID)
		}
		if seenGroup[g.ID] {
			return fmt.Errorf("group %q listed twice", g.ID)
		}
		seenGroup[g.ID] = true
		if len(g.Pages) == 0 {
			return fmt.Errorf("group %q has no pages", g.ID)
		}
		for _, p := range g.Pages {
			if strings.TrimSpace(p.Path) == "" || strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Summary) == "" {
				return fmt.Errorf("group %q: page %q needs path, title and summary", g.ID, p.Path)
			}
			if strings.Contains(p.Summary, "\n") {
				return fmt.Errorf("group %q: page %q: the summary is one line", g.ID, p.Path)
			}
			if seenPath[p.Path] {
				return fmt.Errorf("page %q listed twice", p.Path)
			}
			seenPath[p.Path] = true
		}
	}
	return nil
}

// Pages returns every page in map order.
func (n *Nav) Pages() []Page {
	var out []Page
	for _, g := range n.Groups {
		out = append(out, g.Pages...)
	}
	return out
}

// RepoPath turns a nav path (relative to docs/) into a path relative to the
// repository root.
func RepoPath(navPath string) string {
	return filepath.ToSlash(filepath.Clean(filepath.Join("docs", navPath)))
}
