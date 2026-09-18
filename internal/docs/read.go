package docs

import (
	"fmt"
	"strings"
)

// DefaultReadBytes bounds one reading of a page when the caller names no
// budget: a whole page of the documentation is up to 150 KB, which a model
// should take section by section rather than at once.
const DefaultReadBytes = 24 << 10

// ReadOptions selects the part of a page a reading returns.
type ReadOptions struct {
	// Anchor names a section; empty reads the page from its title.
	Anchor string
	// Offset is the line to start at, from 1, counted within the page or
	// the section; 0 starts at the top.
	Offset int
	// MaxBytes bounds the text; 0 means DefaultReadBytes, negative means
	// no bound. A reading always holds at least one line.
	MaxBytes int
}

// Reading is a run of lines of a page or of one of its sections.
type Reading struct {
	Page *Page
	// Heading is the section read, nil for the page.
	Heading *Heading
	Text    string
	// From and To are the first and last line returned and Total the
	// lines of the page or section, all from 1.
	From, To, Total int
	// Next is the offset that continues the reading, 0 when it is done.
	Next int
}

// Read returns a run of lines of the page or of one of its sections, cut
// at a line boundary under the byte budget.
func (p *Page) Read(opts ReadOptions) (Reading, error) {
	start, end := 0, len(p.lines)
	r := Reading{Page: p}
	if opts.Anchor != "" {
		h, ok := p.heading(opts.Anchor)
		if !ok {
			return Reading{}, fmt.Errorf("page %s has no section #%s; its sections: %s", p.Slug, opts.Anchor, strings.Join(p.anchors(), ", "))
		}
		r.Heading = &h
		start, end = p.sectionLines(h)
	} else {
		end = trimTrailingBlank(p.lines, 0, end)
	}
	lines := p.lines[start:end]
	r.Total = len(lines)
	from := opts.Offset
	if from < 1 {
		from = 1
	}
	if from > r.Total {
		return Reading{}, fmt.Errorf("offset %d is past the end: %s has %d lines", opts.Offset, Ref(p.Slug, opts.Anchor), r.Total)
	}
	budget := opts.MaxBytes
	if budget == 0 {
		budget = DefaultReadBytes
	}
	to := from - 1
	size := 0
	for to < r.Total {
		n := len(lines[to]) + 1
		if budget > 0 && size+n > budget && to >= from {
			break
		}
		size += n
		to++
	}
	r.From, r.To = from, to
	r.Text = strings.Join(lines[from-1:to], "\n")
	if to < r.Total {
		r.Next = to + 1
	}
	return r, nil
}

// Sections lists the headings under the page title, for a reader choosing
// what to read next.
func (p *Page) Sections() []Heading {
	var out []Heading
	for _, h := range p.Headings {
		if h.Level > 1 {
			out = append(out, h)
		}
	}
	return out
}

// Outline writes the sections of a page as an indented list of anchors
// and headings.
func (p *Page) Outline() string {
	var b strings.Builder
	for _, h := range p.Sections() {
		fmt.Fprintf(&b, "%s- #%s  %s\n", strings.Repeat("  ", h.Level-2), h.Anchor, h.Text)
	}
	return b.String()
}

// Contents writes the map of the documentation: every group with its pages,
// their slugs and one-line summaries.
func (l *Library) Contents() string {
	var b strings.Builder
	for i, g := range l.Groups {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "## %s\n", g.Title)
		for _, p := range g.Pages {
			fmt.Fprintf(&b, "- %s - %s: %s\n", p.Slug, p.Title, p.Summary)
		}
	}
	return b.String()
}

// FormatHits writes search results as a numbered list: the reference to
// read, the page and section titles and the snippet.
func FormatHits(hits []Hit) string {
	var b strings.Builder
	for i, h := range hits {
		title := h.Title
		if h.Heading != "" {
			title += " > " + h.Heading
		}
		fmt.Fprintf(&b, "%d. %s  (%s)\n", i+1, h.Ref(), title)
		if s := h.SnippetText(); s != "" {
			fmt.Fprintf(&b, "   %s\n", s)
		}
	}
	return b.String()
}
