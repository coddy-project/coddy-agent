//go:build cli

package cli

import (
	"regexp"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/docs"
)

// docsModal is the console's help: Coddy's own documentation, built into the
// binary (internal/docs), in the place of the editor. F1 or /docs opens it on
// a search box over the contents; typing searches the sections with BM25,
// enter opens the page at the section found, and the page reads like a book:
// scroll it, jump from section to section, turn to the next page or the one
// before. Nothing is fetched, so it works offline and under --remote alike.
type docsModal struct {
	tui.Container

	theme         *tui.Theme
	md            tui.MarkdownTheme
	requestRender func()
	// rows is the height of the terminal, which bounds the overlay.
	rows func() int
	lib  *docs.Library

	// The search view: the query and what it found, or the contents when
	// the query is empty.
	query    string
	entries  []docsEntry
	selected int

	// The page view, when page is set.
	page      *docs.Page
	offset    int
	jumpTo    string
	rendered  []string
	sectionAt []docsSectionLine
	width     int

	OnClose func()
}

// docsEntry is one row of the search view: a page, or a section of one.
type docsEntry struct {
	slug, anchor   string
	title, heading string
	group, snippet string
}

// docsSectionLine is where a section starts among the rendered lines.
type docsSectionLine struct {
	anchor string
	line   int
}

const (
	// docsChromeRows is what the overlay draws besides its body (borders,
	// title, help line) plus the footer below it, which the overlay must
	// leave on screen so opening and closing it redraw in place.
	docsChromeRows = 8
	docsMinBody    = 5
	docsSearchMax  = 30
)

func newDocsModal(lib *docs.Library, theme *tui.Theme, md tui.MarkdownTheme, rows func() int, requestRender func()) *docsModal {
	m := &docsModal{theme: theme, md: md, rows: rows, lib: lib, requestRender: requestRender}
	m.search()
	m.rebuild()
	return m
}

func (m *docsModal) bodyRows() int {
	rows := 24
	if m.rows != nil && m.rows() > 0 {
		rows = m.rows()
	}
	return max(docsMinBody, rows-docsChromeRows)
}

// SetQuery searches for text, as /docs <words> does.
func (m *docsModal) SetQuery(text string) {
	m.query = tui.SanitizeText(text)
	m.search()
	m.rebuild()
}

// InsertPaste adds a pasted text to the query of the search view.
func (m *docsModal) InsertPaste(text string) {
	if m.page != nil {
		return
	}
	m.SetQuery(m.query + strings.ReplaceAll(strings.TrimSpace(text), "\n", " "))
}

// OpenPage shows a page, scrolled to a section when anchor names one.
func (m *docsModal) OpenPage(page *docs.Page, anchor string) {
	m.page, m.offset, m.jumpTo, m.rendered, m.width = page, 0, anchor, nil, 0
	m.rebuild()
}

// PageTitle is the title of the page on screen, "" in the search view.
func (m *docsModal) PageTitle() string {
	if m.page == nil {
		return ""
	}
	return m.page.Title
}

func (m *docsModal) search() {
	m.entries, m.selected = nil, 0
	if strings.TrimSpace(m.query) == "" {
		for _, p := range m.lib.Pages() {
			m.entries = append(m.entries, docsEntry{slug: p.Slug, title: p.Title, group: p.Group.Title, snippet: p.Summary})
		}
		return
	}
	for _, h := range m.lib.Search(m.query, docsSearchMax) {
		m.entries = append(m.entries, docsEntry{slug: h.Slug, anchor: h.Anchor, title: h.Title, heading: h.Heading, group: h.Group, snippet: h.SnippetText()})
	}
}

// HandleInput edits the query and picks an entry in the search view, and
// scrolls, jumps and turns pages in the page view. Escape leaves a page
// first, then the overlay; F1 closes it from anywhere.
func (m *docsModal) HandleInput(data []byte) {
	defer func() {
		if m.requestRender != nil {
			m.requestRender()
		}
	}()
	key, isKey := tui.ParseKey(data)
	if isKey && (key.String() == "f1" || key.String() == "ctrl+c") {
		m.close()
		return
	}
	if m.page != nil {
		m.pageInput(data, key, isKey)
		return
	}
	if isKey {
		switch key.String() {
		case "escape":
			m.close()
			return
		case "up":
			if m.selected > 0 {
				m.selected--
				m.rebuild()
			}
			return
		case "down":
			if m.selected < len(m.entries)-1 {
				m.selected++
				m.rebuild()
			}
			return
		case "enter":
			if m.selected < len(m.entries) {
				e := m.entries[m.selected]
				if p, ok := m.lib.Page(e.slug); ok {
					m.OpenPage(p, e.anchor)
				}
			}
			return
		case "backspace":
			if m.query != "" {
				m.SetQuery(tui.TrimLastGrapheme(m.query))
			}
			return
		}
	}
	s := string(data)
	if len(s) > 0 && !strings.ContainsRune(s, 0x1b) && s[0] >= 0x20 && s[0] != 0x7f {
		m.SetQuery(m.query + s)
	}
}

func (m *docsModal) pageInput(data []byte, key tui.Key, isKey bool) {
	body := m.bodyRows()
	// Letters are commands here: the page view has no text to type into.
	switch string(data) {
	case " ":
		m.offset += body - 1
		m.rebuild()
		return
	case "n", "N":
		if next := m.lib.Next(m.page); next != nil {
			m.OpenPage(next, "")
		}
		return
	case "p", "P":
		if prev := m.lib.Prev(m.page); prev != nil {
			m.OpenPage(prev, "")
		}
		return
	case "/":
		m.page, m.rendered = nil, nil
		m.rebuild()
		return
	}
	if !isKey {
		return
	}
	switch key.String() {
	case "escape":
		m.page, m.rendered = nil, nil
	case "up":
		m.offset--
	case "down", "enter":
		m.offset++
	case "pageup":
		m.offset -= body - 1
	case "pagedown":
		m.offset += body - 1
	case "home":
		m.offset = 0
	case "end":
		m.offset = len(m.rendered)
	case "tab":
		m.jumpSection(1)
	case "shift+tab":
		m.jumpSection(-1)
	default:
		return
	}
	m.rebuild()
}

// jumpSection scrolls to the next (dir 1) or the previous (dir -1) section
// heading of the page on screen.
func (m *docsModal) jumpSection(dir int) {
	if dir > 0 {
		for _, s := range m.sectionAt {
			if s.line > m.offset {
				m.offset = s.line
				return
			}
		}
		return
	}
	target := 0
	for _, s := range m.sectionAt {
		if s.line >= m.offset {
			break
		}
		target = s.line
	}
	m.offset = target
}

func (m *docsModal) close() {
	if m.OnClose != nil {
		m.OnClose()
	}
}

func (m *docsModal) rebuild() {
	m.Clear()
	th := m.theme
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	if m.page != nil {
		m.buildPage()
	} else {
		m.buildSearch()
	}
	m.AddChild(tui.NewDynamicBorder(th.FgFn(roleBorderAccent)))
	m.Invalidate()
}

func (m *docsModal) buildSearch() {
	th := m.theme
	title := th.Fg(roleAccent, th.Bold("Coddy docs")) + th.Fg(roleMuted, "  "+m.lib.Version+" · built into this binary · "+itoa(len(m.lib.Pages()))+" pages")
	m.AddChild(tui.NewText(title, 1, 0, nil))
	m.AddChild(tui.NewText(th.Fg(roleAccent, "› ")+m.query+th.Fg(roleDim, "▏"), 1, 0, nil))
	m.AddChild(&docsResults{modal: m})
	m.AddChild(tui.NewText(th.Fg(roleDim, "type to search · ↑↓ select · enter read · esc close"), 1, 0, nil))
}

func (m *docsModal) buildPage() {
	th := m.theme
	pos := 0
	for i, p := range m.lib.Pages() {
		if p == m.page {
			pos = i + 1
		}
	}
	title := th.Fg(roleAccent, th.Bold("Coddy docs › "+m.page.Title)) +
		th.Fg(roleMuted, "  "+m.page.Group.Title+" · page "+itoa(pos)+" of "+itoa(len(m.lib.Pages())))
	m.AddChild(tui.NewText(title, 1, 0, nil))
	m.AddChild(&docsPageView{modal: m})
	m.AddChild(tui.NewText(th.Fg(roleDim, "↑↓ pgup/pgdn scroll · tab section · n/p next/previous page · / search · esc back"), 1, 0, nil))
}

// docsResults draws the entries of the search view in a window around the
// cursor, with the snippet of the selected one under it.
type docsResults struct{ modal *docsModal }

func (r *docsResults) Invalidate() {}

func (r *docsResults) Render(width int) []string {
	m := r.modal
	th := m.theme
	if len(m.entries) == 0 {
		return []string{" " + th.Fg(roleDim, "Nothing in the documentation matches; try other or fewer words")}
	}
	// Two rows go to the snippet under the cursor and to the counter.
	room := max(1, m.bodyRows()-3)
	first := 0
	if len(m.entries) > room {
		first = min(max(0, m.selected-room/2), len(m.entries)-room)
	}
	last := min(len(m.entries), first+room)
	lines := make([]string, 0, room+2)
	for i := first; i < last; i++ {
		e := m.entries[i]
		name := tui.SanitizeText(e.title)
		if e.heading != "" {
			name += " › " + tui.SanitizeText(e.heading)
		}
		// The group on the right is short; the reference to read is on the
		// selected row's second line, so a long anchor never eats the title.
		group := tui.SanitizeText(e.group)
		space := width - 1 - 2 - tui.VisibleWidth(group) - 2
		name = tui.TruncateToWidthPad(name, max(space, 8), "…")
		cursor := "  "
		if i == m.selected {
			cursor = th.Fg(roleAccent, "→ ")
			name = th.Bold(name)
		}
		line := " " + cursor + name + "  " + th.Fg(roleMuted, group)
		lines = append(lines, tui.TruncateToWidth(line, width, ""))
		if i == m.selected {
			detail := th.Fg(roleAccent, docs.Ref(e.slug, e.anchor))
			if s := strings.TrimSpace(e.snippet); s != "" {
				detail += th.Fg(roleDim, "  "+tui.SanitizeText(s))
			}
			lines = append(lines, tui.TruncateToWidth("     "+detail, width, "…"))
		}
	}
	if len(m.entries) > room {
		lines = append(lines, th.Fg(roleDim, "   "+itoa(m.selected+1)+" of "+itoa(len(m.entries))))
	}
	return lines
}

// docsPageView draws a window of the rendered page.
type docsPageView struct{ modal *docsModal }

func (v *docsPageView) Invalidate() {}

func (v *docsPageView) Render(width int) []string {
	m := v.modal
	if m.rendered == nil || m.width != width {
		m.render(width)
	}
	if m.jumpTo != "" {
		for _, s := range m.sectionAt {
			if s.anchor == m.jumpTo {
				m.offset = s.line
			}
		}
		m.jumpTo = ""
	}
	body := m.bodyRows()
	m.offset = max(0, min(m.offset, len(m.rendered)-body))
	end := min(len(m.rendered), m.offset+body)
	lines := append([]string(nil), m.rendered[m.offset:end]...)
	// The last line says where the reader is, as a pager does.
	if len(m.rendered) > body {
		percent := min(100, end*100/len(m.rendered))
		lines = append(lines, m.theme.Fg(roleDim, "   lines "+itoa(m.offset+1)+"-"+itoa(end)+" of "+itoa(len(m.rendered))+" · "+itoa(percent)+"%"))
	}
	return lines
}

var docsImageRE = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)

// render lays the page out at a width, section by section, so the line
// every section starts at is known: tab jumps there, and a search hit opens
// the page scrolled to it. Sections split at headings, which are block
// boundaries, so the pieces render as the whole page would.
func (m *docsModal) render(width int) {
	m.width = width
	m.rendered, m.sectionAt = nil, nil
	lines := strings.Split(m.page.Markdown, "\n")
	type chunk struct {
		anchor     string
		start, end int
	}
	var chunks []chunk
	start := 0
	anchor := ""
	for _, h := range m.page.Headings {
		if h.Level < 2 {
			continue
		}
		chunks = append(chunks, chunk{anchor, start, h.Line})
		start, anchor = h.Line, h.Anchor
	}
	chunks = append(chunks, chunk{anchor, start, len(lines)})
	for _, c := range chunks {
		text := strings.Join(lines[c.start:c.end], "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		// The console shows no images: an image is its caption.
		text = docsImageRE.ReplaceAllString(text, "[image: $1]")
		if n := len(m.rendered); n > 0 && strings.TrimSpace(tui.StripTerminalSequences(m.rendered[n-1])) != "" {
			m.rendered = append(m.rendered, "")
		}
		if c.anchor != "" {
			m.sectionAt = append(m.sectionAt, docsSectionLine{c.anchor, len(m.rendered)})
		}
		m.rendered = append(m.rendered, tui.NewMarkdown(tui.SanitizeText(text), 1, 0, m.md).Render(width)...)
	}
}

// openDocsOverlay is F1 and /docs: the built-in documentation in the place
// of the editor. A reference to a page ("features/mentions#completion")
// opens that page; any other text is a search.
func (a *App) openDocsOverlay(arg string) {
	lib, err := docs.Default()
	if err != nil {
		a.appendStatus(roleError, "The built-in documentation does not load: "+err.Error())
		return
	}
	rows := func() int {
		if a.term == nil {
			return 0
		}
		return a.term.Rows()
	}
	m := newDocsModal(lib, a.theme, a.mdTheme, rows, a.screen.RequestRender)
	m.OnClose = a.closeModal
	arg = strings.TrimSpace(arg)
	if arg != "" {
		if page, anchor, err := lib.Resolve(arg); err == nil && (strings.ContainsAny(arg, "/#:") || strings.EqualFold(arg, page.Title)) {
			m.OpenPage(page, anchor)
		} else {
			m.SetQuery(arg)
		}
	}
	a.openModal(m)
}
