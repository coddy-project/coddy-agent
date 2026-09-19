// Package docs is Coddy's documentation as the binary carries it: the pages
// of docs/nav.yaml, embedded at build time (package docs at the repository
// root), split into sections by heading, searchable with BM25 and readable by
// page or by section. Every surface reads it from here - the agent's
// coddy_docs_search and coddy_docs_read tools, the @coddy: mention, the
// console's F1 help, the web UI's reader, coddy docs on the command line - so
// what the user reads and what the agent reads is the documentation of the
// very binary that runs, with no request to a site.
package docs

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	docsfs "github.com/EvilFreelancer/coddy-agent/docs"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// Repository addresses for what the binary does not carry: images and files
// of the repository a page links to. They are pinned to the release the
// binary was built from, so an image matches the text around it.
const (
	githubBlob = "https://github.com/coddy-project/coddy-agent/blob/"
	githubRaw  = "https://raw.githubusercontent.com/coddy-project/coddy-agent/"

	// SiteBase is the public address of a page, coddy.dev/docs/<slug>.
	SiteBase = "https://coddy.dev/docs/"

	// LinkScheme prefixes a link from one page to another once the page is
	// read out of the binary: coddy:features/mentions#what-the-model-receives.
	// Every surface resolves it to its own reader, and the agent passes it to
	// coddy_docs_read as it stands.
	LinkScheme = "coddy:"
)

// Group is one section of the map with the pages the binary carries.
type Group struct {
	ID      string
	Title   string
	Summary string
	Pages   []*Page
}

// Page is one page of the map.
type Page struct {
	// Slug is the path under docs/ without ".md": features/mentions. It is
	// the page's address everywhere: coddy.dev/docs/<slug>, @coddy:<slug>,
	// the web reader's #/docs/<slug>.
	Slug    string
	Title   string
	Summary string
	Group   *Group
	// Markdown is the page with its links rewritten for a reader outside
	// the repository: a link to another page is coddy:<slug>#<anchor>, an
	// image or a repository file is an address on GitHub at the release.
	Markdown string
	Headings []Heading

	lines []string
	index int
}

// Library is the whole documentation of one binary.
type Library struct {
	Version string
	Groups  []*Group

	pages  []*Page
	bySlug map[string]*Page

	indexOnce sync.Once
	idx       *index
}

var (
	defaultOnce sync.Once
	defaultLib  *Library
	defaultErr  error
)

// Default is the documentation embedded in this binary, loaded once.
func Default() (*Library, error) {
	defaultOnce.Do(func() {
		defaultLib, defaultErr = Load(docsfs.FS, version.Get())
	})
	return defaultLib, defaultErr
}

type navFile struct {
	Groups []struct {
		ID      string `yaml:"id"`
		Title   string `yaml:"title"`
		Summary string `yaml:"summary"`
		Pages   []struct {
			Path    string `yaml:"path"`
			Title   string `yaml:"title"`
			Summary string `yaml:"summary"`
		} `yaml:"pages"`
	} `yaml:"groups"`
}

// Load reads nav.yaml and its pages from fsys. A page the map lists outside
// the documentation directory (../CONTRIBUTING.md) is skipped: the binary
// does not carry it. A page inside it that fsys lacks is an error, so a
// documentation group the embed pattern forgot fails the build's tests
// rather than a reader.
func Load(fsys fs.FS, ver string) (*Library, error) {
	data, err := fs.ReadFile(fsys, "nav.yaml")
	if err != nil {
		return nil, err
	}
	var nav navFile
	if err := yaml.Unmarshal(data, &nav); err != nil {
		return nil, fmt.Errorf("nav.yaml: %w", err)
	}
	lib := &Library{Version: ver, bySlug: map[string]*Page{}}
	ref := releaseRef(ver)
	for _, g := range nav.Groups {
		group := &Group{ID: g.ID, Title: g.Title, Summary: g.Summary}
		for _, p := range g.Pages {
			clean := path.Clean(p.Path)
			if strings.HasPrefix(clean, "../") || !strings.HasSuffix(clean, ".md") {
				continue
			}
			body, err := fs.ReadFile(fsys, clean)
			if err != nil {
				return nil, fmt.Errorf("page %s of nav.yaml: %w", p.Path, err)
			}
			slug := strings.TrimSuffix(clean, ".md")
			if _, dup := lib.bySlug[slug]; dup {
				return nil, fmt.Errorf("page %s listed twice in nav.yaml", p.Path)
			}
			md := rewriteLinks(string(body), slug, ref)
			page := &Page{
				Slug:     slug,
				Title:    p.Title,
				Summary:  p.Summary,
				Group:    group,
				Markdown: md,
				lines:    strings.Split(md, "\n"),
				index:    len(lib.pages),
			}
			page.Headings = parseHeadings(page.lines)
			group.Pages = append(group.Pages, page)
			lib.pages = append(lib.pages, page)
			lib.bySlug[slug] = page
		}
		if len(group.Pages) > 0 {
			lib.Groups = append(lib.Groups, group)
		}
	}
	if len(lib.pages) == 0 {
		return nil, fmt.Errorf("nav.yaml lists no page")
	}
	return lib, nil
}

var releaseRE = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

// releaseRef is the git reference the repository links of a page point at:
// the release tag for a release build, main for anything else.
func releaseRef(ver string) string {
	if releaseRE.MatchString(ver) {
		return strings.TrimPrefix(ver, "v")
	}
	return "main"
}

// Pages returns every page in map order.
func (l *Library) Pages() []*Page { return l.pages }

// Page returns the page with this exact slug.
func (l *Library) Page(slug string) (*Page, bool) {
	p, ok := l.bySlug[slug]
	return p, ok
}

// Prev and Next are the neighbours of a page in map order, for reading the
// documentation as a book; nil at either end.
func (l *Library) Prev(p *Page) *Page {
	if p == nil || p.index == 0 {
		return nil
	}
	return l.pages[p.index-1]
}

func (l *Library) Next(p *Page) *Page {
	if p == nil || p.index+1 >= len(l.pages) {
		return nil
	}
	return l.pages[p.index+1]
}

// SiteURL is the public address of a page.
func (p *Page) SiteURL() string { return SiteBase + p.Slug }

// Ref is how a surface names a page and, optionally, one of its sections:
// features/mentions or features/mentions#what-the-model-receives.
func Ref(slug, anchor string) string {
	if anchor == "" {
		return slug
	}
	return slug + "#" + anchor
}

// Resolve finds the page (and the section, when the reference carries an
// anchor) a reference names. It takes every spelling a person or a model is
// likely to write: the slug, the file path with or without docs/ and .md,
// the coddy: link and the @coddy: mention, the coddy.dev address, a page's
// file name when only one page has it, and a page's title. An anchor that
// names no heading of the page is an error listing the ones that exist.
func (l *Library) Resolve(ref string) (*Page, string, error) {
	raw := strings.TrimSpace(ref)
	s := raw
	s = strings.TrimPrefix(s, "@")
	s = strings.TrimPrefix(s, LinkScheme)
	for _, prefix := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimPrefix(s, "coddy.dev/docs/")
	s = strings.TrimPrefix(s, "www.coddy.dev/docs/")
	s, anchor, _ := strings.Cut(s, "#")
	s = strings.Trim(strings.TrimSpace(s), "/")
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimPrefix(s, "docs/")
	s = strings.TrimSuffix(s, ".md")
	anchor = strings.ToLower(strings.TrimSpace(anchor))
	if s == "" {
		return nil, "", fmt.Errorf("no page named")
	}
	page := l.bySlug[s]
	if page == nil {
		page = l.bySlug[strings.ToLower(s)]
	}
	if page == nil {
		page = l.uniqueMatch(func(p *Page) bool { return path.Base(p.Slug) == strings.ToLower(s) })
	}
	if page == nil {
		page = l.uniqueMatch(func(p *Page) bool { return strings.EqualFold(p.Title, s) })
	}
	if page == nil {
		msg := fmt.Sprintf("no documentation page %q", raw)
		if hits := l.Search(strings.ReplaceAll(s, "/", " "), 3); len(hits) > 0 {
			var names []string
			for _, h := range hits {
				names = append(names, h.Slug)
			}
			msg += "; closest: " + strings.Join(dedupe(names), ", ")
		}
		return nil, "", fmt.Errorf("%s", msg)
	}
	if anchor != "" {
		if _, ok := page.heading(anchor); !ok {
			return nil, "", fmt.Errorf("page %s has no section #%s; its sections: %s", page.Slug, anchor, strings.Join(page.anchors(), ", "))
		}
	}
	return page, anchor, nil
}

func (l *Library) uniqueMatch(match func(*Page) bool) *Page {
	var found *Page
	for _, p := range l.pages {
		if match(p) {
			if found != nil {
				return nil
			}
			found = p
		}
	}
	return found
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
