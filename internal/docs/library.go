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
	// the page's address everywhere and in every language:
	// coddy.dev/docs/<slug>, @coddy:<slug>, the web reader's #/docs/<slug>.
	Slug    string
	Title   string
	Summary string
	Group   *Group
	// Lang is the language the page's text is in: the library's, or English
	// for a page its translation lacks.
	Lang string
	// Markdown is the page with its links rewritten for a reader outside
	// the repository: a link to another page is coddy:<slug>#<anchor>, an
	// image or a repository file is an address on GitHub at the release.
	Markdown string
	Headings []Heading

	lines []string
	index int
}

// Library is the whole documentation of one binary in one language.
type Library struct {
	Version string
	// Lang is the language of the library (English, Russian).
	Lang   string
	Groups []*Group

	pages  []*Page
	bySlug map[string]*Page

	indexOnce sync.Once
	idx       *index
}

// Default is the English documentation embedded in this binary, loaded once.
func Default() (*Library, error) { return For(English) }

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

// Load reads the English documentation from fsys: LoadLang in English.
func Load(fsys fs.FS, ver string) (*Library, error) { return LoadLang(fsys, ver, English) }

// LoadLang reads nav.yaml and its pages from fsys in a language. The map of
// the source (nav.yaml) orders the library in every language; a translation
// takes its titles and summaries from <lang>/nav.yaml and each page from
// <lang>/<path>, the English page standing in for one it lacks. A page the
// map lists outside the documentation directory (../CONTRIBUTING.md) is
// skipped: the binary does not carry it. A page inside it that fsys lacks in
// English is an error, so a documentation group the embed pattern forgot
// fails the build's tests rather than a reader.
//
// A translated heading answers to the anchor of the English heading at the
// same position (Heading.Anchor) as well as to its own (Heading.Local), and
// the links of a translated page are rewritten to those shared anchors, so
// one address names a section in every language.
func LoadLang(fsys fs.FS, ver, lang string) (*Library, error) {
	data, err := fs.ReadFile(fsys, "nav.yaml")
	if err != nil {
		return nil, err
	}
	var nav navFile
	if err := yaml.Unmarshal(data, &nav); err != nil {
		return nil, fmt.Errorf("nav.yaml: %w", err)
	}
	var tr translatedNav
	if lang != English {
		if data, err := fs.ReadFile(fsys, lang+"/nav.yaml"); err == nil {
			if err := yaml.Unmarshal(data, &tr); err != nil {
				return nil, fmt.Errorf("%s/nav.yaml: %w", lang, err)
			}
		}
	}
	lib := &Library{Version: ver, Lang: lang, bySlug: map[string]*Page{}}
	ref := releaseRef(ver)

	// The first pass reads every page and its headings, so the second can
	// rewrite a link to the shared anchor of the section it points at.
	type raw struct {
		page *Page
		file string
		text string
	}
	var raws []raw
	for _, g := range nav.Groups {
		group := &Group{ID: g.ID, Title: g.Title, Summary: g.Summary}
		if t, ok := tr.Groups[g.ID]; ok {
			group.Title, group.Summary = orElse(t.Title, g.Title), orElse(t.Summary, g.Summary)
		}
		for _, p := range g.Pages {
			clean := path.Clean(p.Path)
			if strings.HasPrefix(clean, "../") || !strings.HasSuffix(clean, ".md") {
				continue
			}
			source, err := fs.ReadFile(fsys, clean)
			if err != nil {
				return nil, fmt.Errorf("page %s of nav.yaml: %w", p.Path, err)
			}
			slug := strings.TrimSuffix(clean, ".md")
			if _, dup := lib.bySlug[slug]; dup {
				return nil, fmt.Errorf("page %s listed twice in nav.yaml", p.Path)
			}
			page := &Page{Slug: slug, Title: p.Title, Summary: p.Summary, Group: group, Lang: English, index: len(lib.pages)}
			if t, ok := tr.Pages[clean]; ok {
				page.Title, page.Summary = orElse(t.Title, p.Title), orElse(t.Summary, p.Summary)
			}
			text, file := stripStamp(string(source)), "docs/"+clean
			page.Headings = parseHeadings(strings.Split(text, "\n"))
			if lang != English {
				if body, err := fs.ReadFile(fsys, lang+"/"+clean); err == nil {
					text, file, page.Lang = stripStamp(string(body)), "docs/"+lang+"/"+clean, lang
					local := parseHeadings(strings.Split(text, "\n"))
					shareAnchors(local, page.Headings)
					page.Headings = local
				}
			}
			group.Pages = append(group.Pages, page)
			lib.pages = append(lib.pages, page)
			lib.bySlug[slug] = page
			raws = append(raws, raw{page: page, file: file, text: text})
		}
		if len(group.Pages) > 0 {
			lib.Groups = append(lib.Groups, group)
		}
	}
	if len(lib.pages) == 0 {
		return nil, fmt.Errorf("nav.yaml lists no page")
	}
	for _, r := range raws {
		md := rewriteLinks(r.text, r.file, r.page.Slug, ref, lib.sharedAnchor)
		r.page.Markdown = md
		r.page.lines = strings.Split(md, "\n")
	}
	return lib, nil
}

func orElse(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// stampRE is the line docsgen writes at the end of a translated page: the
// digest of the English page it translates. It is bookkeeping, not text.
var stampRE = regexp.MustCompile(`(?m)^<!-- docsgen:source [^>]*-->[ \t]*\n?`)

func stripStamp(s string) string {
	if !strings.Contains(s, "<!-- docsgen:source") {
		return s
	}
	return strings.TrimRight(stampRE.ReplaceAllString(s, ""), "\n") + "\n"
}

// shareAnchors gives each translated heading the anchor of the source heading
// at the same position, for as long as the two outlines agree on the levels;
// a heading past the first disagreement keeps its own anchor.
func shareAnchors(local, source []Heading) {
	for i := range local {
		if i >= len(source) || local[i].Level != source[i].Level {
			return
		}
		local[i].Anchor = source[i].Anchor
	}
}

// sharedAnchor is the anchor a link's fragment names on a page of the
// library: the shared anchor of the heading whose own or shared anchor it is,
// or the fragment as written when no heading has it.
func (l *Library) sharedAnchor(slug, frag string) string {
	if p := l.bySlug[slug]; p != nil {
		if h, ok := p.heading(frag); ok {
			return h.Anchor
		}
	}
	return frag
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

// SiteURL is the public address of a page in the language of its text:
// coddy.dev/docs/<slug>, coddy.dev/ru/docs/<slug>.
func (p *Page) SiteURL() string { return siteBase(p.Lang) + p.Slug }

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
// likely to write: the slug, the file path with or without docs/, a language
// folder and .md, the coddy: link and the @coddy: mention, the coddy.dev
// address in any language, a page's file name when only one page has it, and
// a page's title. A section is named by its shared anchor or by the anchor of
// its heading in the library's language, written or percent-encoded, and the
// shared anchor is returned. An anchor that names no heading of the page is
// an error listing the ones that exist.
func (l *Library) Resolve(ref string) (*Page, string, error) {
	raw := strings.TrimSpace(ref)
	s := raw
	s = strings.TrimPrefix(s, "@")
	s = strings.TrimPrefix(s, LinkScheme)
	for _, prefix := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimPrefix(s, "www.")
	if rest, ok := strings.CutPrefix(s, "coddy.dev/"); ok {
		s = trimLangFolder(rest)
		s = strings.TrimPrefix(s, "docs/")
	}
	s, anchor, _ := strings.Cut(s, "#")
	s = strings.Trim(strings.TrimSpace(s), "/")
	s = strings.TrimPrefix(s, "./")
	s = strings.TrimPrefix(s, "docs/")
	s = trimLangFolder(s)
	s = strings.TrimSuffix(s, ".md")
	anchor = strings.TrimSpace(anchor)
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
		h, ok := page.heading(anchor)
		if !ok {
			return nil, "", fmt.Errorf("page %s has no section #%s; its sections: %s", page.Slug, strings.ToLower(anchor), strings.Join(page.anchors(), ", "))
		}
		anchor = h.Anchor
	}
	return page, anchor, nil
}

// trimLangFolder drops the folder of a translation ("ru/") from a path.
func trimLangFolder(s string) string {
	for _, l := range Languages[1:] {
		if rest, ok := strings.CutPrefix(s, l+"/"); ok {
			return rest
		}
	}
	return s
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
