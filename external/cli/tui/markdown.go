//go:build cli

package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MarkdownTheme styles rendered markdown elements (pi markdown theme shape).
type MarkdownTheme struct {
	Heading         func(string) string
	Bold            func(string) string
	Italic          func(string) string
	Strikethrough   func(string) string
	Code            func(string) string
	CodeBlock       func(string) string
	CodeBlockBorder func(string) string
	Quote           func(string) string
	QuoteBorder     func(string) string
	Hr              func(string) string
	Link            func(string) string
	LinkURL         func(string) string
	ListBullet      func(string) string
	Underline       func(string) string
	// HighlightCode optionally syntax-highlights fence bodies.
	HighlightCode func(code, lang string) []string
	// Hyperlink wraps text in an OSC 8 hyperlink when supported.
	Hyperlink func(text, url string) string
}

func mdIdent(s string) string { return s }

func (t *MarkdownTheme) fill() {
	if t.Heading == nil {
		t.Heading = mdIdent
	}
	if t.Bold == nil {
		t.Bold = func(s string) string { return "\x1b[1m" + s + "\x1b[22m" }
	}
	if t.Italic == nil {
		t.Italic = func(s string) string { return "\x1b[3m" + s + "\x1b[23m" }
	}
	if t.Strikethrough == nil {
		t.Strikethrough = func(s string) string { return "\x1b[9m" + s + "\x1b[29m" }
	}
	if t.Code == nil {
		t.Code = mdIdent
	}
	if t.CodeBlock == nil {
		t.CodeBlock = mdIdent
	}
	if t.CodeBlockBorder == nil {
		t.CodeBlockBorder = mdIdent
	}
	if t.Quote == nil {
		t.Quote = mdIdent
	}
	if t.QuoteBorder == nil {
		t.QuoteBorder = mdIdent
	}
	if t.Hr == nil {
		t.Hr = mdIdent
	}
	if t.Link == nil {
		t.Link = mdIdent
	}
	if t.LinkURL == nil {
		t.LinkURL = mdIdent
	}
	if t.ListBullet == nil {
		t.ListBullet = mdIdent
	}
	if t.Underline == nil {
		t.Underline = func(s string) string { return "\x1b[4m" + s + "\x1b[24m" }
	}
}

// Markdown renders markdown text with word wrapping (subset port of pi-tui
// Markdown: headings, emphasis, inline code, fences, quotes, hr, lists, task
// lists, tables, links).
type Markdown struct {
	text     string
	paddingX int
	paddingY int
	theme    MarkdownTheme

	cachedText  string
	cachedWidth int
	cachedLines []string
	cacheValid  bool
}

// NewMarkdown creates a Markdown block.
func NewMarkdown(text string, paddingX, paddingY int, theme MarkdownTheme) *Markdown {
	theme.fill()
	return &Markdown{text: text, paddingX: paddingX, paddingY: paddingY, theme: theme}
}

// SetText replaces the markdown source.
func (m *Markdown) SetText(text string) {
	m.text = text
	m.cacheValid = false
}

// Text returns the current source.
func (m *Markdown) Text() string { return m.text }

// Invalidate clears the render cache.
func (m *Markdown) Invalidate() { m.cacheValid = false }

// Render lexes and renders the markdown at the given width.
func (m *Markdown) Render(width int) []string {
	if m.cacheValid && m.cachedText == m.text && m.cachedWidth == width {
		return m.cachedLines
	}
	store := func(lines []string) []string {
		m.cachedText = m.text
		m.cachedWidth = width
		m.cachedLines = lines
		m.cacheValid = true
		return lines
	}
	if strings.TrimSpace(m.text) == "" {
		return store(nil)
	}
	contentWidth := max(1, width-m.paddingX*2)
	body := renderMarkdownLines(trimPartialClosingFence(m.text), contentWidth, m.theme)

	margin := strings.Repeat(" ", m.paddingX)
	var out []string
	for i := 0; i < m.paddingY; i++ {
		out = append(out, "")
	}
	for _, line := range body {
		out = append(out, margin+line)
	}
	for i := 0; i < m.paddingY; i++ {
		out = append(out, "")
	}
	return store(out)
}

// trimPartialClosingFence drops a trailing partial ``` so streaming output
// does not flicker between fenced and unfenced states.
func trimPartialClosingFence(text string) string {
	trimmed := strings.TrimRight(text, "\n")
	idx := strings.LastIndexByte(trimmed, '\n')
	last := trimmed[idx+1:]
	if last == "`" || last == "``" {
		return trimmed[:idx+1]
	}
	return text
}

var (
	headingRegex   = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	hrRegex        = regexp.MustCompile(`^\s{0,3}((-\s*){3,}|(\*\s*){3,}|(_\s*){3,})$`)
	bulletRegex    = regexp.MustCompile(`^(\s*)([-*+])\s+(.*)$`)
	orderedRegex   = regexp.MustCompile(`^(\s*)(\d+)\.\s+(.*)$`)
	taskRegex      = regexp.MustCompile(`^\[( |x|X)\]\s+(.*)$`)
	tableSepRegex  = regexp.MustCompile(`^\s*\|?[\s:|-]+\|?\s*$`)
	fenceOpenRegex = regexp.MustCompile("^\\s{0,3}```\\s*([A-Za-z0-9_+-]*)\\s*$")
)

func renderMarkdownLines(text string, width int, th MarkdownTheme) []string {
	src := strings.Split(strings.ReplaceAll(text, "\t", "   "), "\n")
	var out []string
	i := 0
	for i < len(src) {
		line := src[i]

		if line == "" {
			out = append(out, "")
			i++
			continue
		}

		if m := fenceOpenRegex.FindStringSubmatch(line); m != nil {
			lang := m[1]
			var body []string
			i++
			for i < len(src) && !strings.HasPrefix(strings.TrimSpace(src[i]), "```") {
				body = append(body, src[i])
				i++
			}
			if i < len(src) {
				i++ // closing fence
			}
			out = append(out, th.CodeBlockBorder("```"+lang))
			var rendered []string
			if th.HighlightCode != nil {
				rendered = th.HighlightCode(strings.Join(body, "\n"), lang)
			}
			if rendered == nil {
				for _, b := range body {
					rendered = append(rendered, th.CodeBlock(b))
				}
			}
			for _, b := range rendered {
				if VisibleWidth(b) > width-2 {
					out = append(out, prefixLines("  ", WrapTextWithANSI(b, max(1, width-2)))...)
					continue
				}
				out = append(out, "  "+b)
			}
			out = append(out, th.CodeBlockBorder("```"))
			out = append(out, "")
			continue
		}

		if m := headingRegex.FindStringSubmatch(line); m != nil {
			depth := len(m[1])
			content := renderInline(m[2], th, "")
			var rendered string
			switch depth {
			case 1:
				rendered = th.Heading(th.Bold(th.Underline(content)))
			case 2:
				rendered = th.Heading(th.Bold(content))
			default:
				rendered = th.Heading(m[1]+" ") + th.Heading(th.Bold(content))
			}
			out = append(out, WrapTextWithANSI(rendered, width)...)
			if i+1 < len(src) && src[i+1] != "" {
				out = append(out, "")
			}
			i++
			continue
		}

		if hrRegex.MatchString(line) {
			out = append(out, th.Hr(strings.Repeat("─", min(width, 80))))
			i++
			continue
		}

		if strings.HasPrefix(strings.TrimLeft(line, " "), "> ") || strings.TrimLeft(line, " ") == ">" {
			var quote []string
			for i < len(src) {
				t := strings.TrimLeft(src[i], " ")
				if strings.HasPrefix(t, "> ") {
					quote = append(quote, t[2:])
				} else if t == ">" {
					quote = append(quote, "")
				} else {
					break
				}
				i++
			}
			inner := renderMarkdownLines(strings.Join(quote, "\n"), max(1, width-2), th)
			for len(inner) > 0 && inner[len(inner)-1] == "" {
				inner = inner[:len(inner)-1]
			}
			for _, q := range inner {
				out = append(out, th.QuoteBorder("│ ")+th.Quote(th.Italic(q)))
			}
			continue
		}

		if bulletRegex.MatchString(line) || orderedRegex.MatchString(line) {
			var rendered []string
			i, rendered = renderList(src, i, width, th)
			out = append(out, rendered...)
			continue
		}

		if strings.Contains(line, "|") && i+1 < len(src) && tableSepRegex.MatchString(src[i+1]) && strings.Contains(src[i+1], "-") {
			var rendered []string
			i, rendered = renderTable(src, i, width, th)
			out = append(out, rendered...)
			continue
		}

		// Paragraph: gather until blank or block start.
		var para []string
		for i < len(src) && src[i] != "" &&
			!headingRegex.MatchString(src[i]) && !hrRegex.MatchString(src[i]) &&
			!bulletRegex.MatchString(src[i]) && !orderedRegex.MatchString(src[i]) &&
			!fenceOpenRegex.MatchString(src[i]) &&
			!strings.HasPrefix(strings.TrimLeft(src[i], " "), "> ") {
			para = append(para, src[i])
			i++
		}
		if len(para) == 0 {
			para = []string{line}
			i++
		}
		text := renderInline(strings.Join(para, "\n"), th, "")
		out = append(out, WrapTextWithANSI(text, width)...)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func renderList(src []string, i, width int, th MarkdownTheme) (int, []string) {
	var out []string
	for i < len(src) {
		line := src[i]
		bm := bulletRegex.FindStringSubmatch(line)
		om := orderedRegex.FindStringSubmatch(line)
		if bm == nil && om == nil {
			break
		}
		var indentStr, marker, content string
		if bm != nil {
			indentStr, marker, content = bm[1], "- ", bm[3]
		} else {
			indentStr, marker, content = om[1], om[2]+". ", om[3]
		}
		depth := len(indentStr) / 2
		indent := strings.Repeat("    ", depth)
		if tm := taskRegex.FindStringSubmatch(content); tm != nil {
			box := "[ ] "
			if tm[1] != " " {
				box = "[x] "
			}
			marker += box
			content = tm[2]
		}
		firstPrefix := indent + th.ListBullet(marker)
		contPrefix := indent + strings.Repeat(" ", VisibleWidth(marker))
		wrapped := WrapTextWithANSI(renderInline(content, th, ""), max(1, width-VisibleWidth(indent+marker)))
		for wi, w := range wrapped {
			if wi == 0 {
				out = append(out, firstPrefix+w)
			} else {
				out = append(out, contPrefix+w)
			}
		}
		i++
	}
	return i, out
}

func renderTable(src []string, i, width int, th MarkdownTheme) (int, []string) {
	parseRow := func(line string) []string {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "|")
		trimmed = strings.TrimSuffix(trimmed, "|")
		cells := strings.Split(trimmed, "|")
		for ci := range cells {
			cells[ci] = strings.TrimSpace(cells[ci])
		}
		return cells
	}
	header := parseRow(src[i])
	i += 2 // skip separator
	var rows [][]string
	for i < len(src) && strings.Contains(src[i], "|") {
		rows = append(rows, parseRow(src[i]))
		i++
	}
	numCols := len(header)
	colWidths := make([]int, numCols)
	for ci, h := range header {
		colWidths[ci] = VisibleWidth(h)
	}
	for _, row := range rows {
		for ci := 0; ci < numCols && ci < len(row); ci++ {
			colWidths[ci] = max(colWidths[ci], min(30, VisibleWidth(row[ci])))
		}
	}
	// Shrink to available width if needed.
	available := width - (3*numCols + 1)
	total := 0
	for _, w := range colWidths {
		total += w
	}
	if available < numCols {
		// Too narrow: fall back to raw wrapped text.
		var out []string
		out = append(out, WrapTextWithANSI(strings.Join(header, " | "), width)...)
		for _, row := range rows {
			out = append(out, WrapTextWithANSI(strings.Join(row, " | "), width)...)
		}
		return i, out
	}
	for total > available {
		widest := 0
		for ci := 1; ci < numCols; ci++ {
			if colWidths[ci] > colWidths[widest] {
				widest = ci
			}
		}
		if colWidths[widest] <= 3 {
			break
		}
		colWidths[widest]--
		total--
	}
	rule := func(l, mid, r string) string {
		parts := make([]string, numCols)
		for ci, w := range colWidths {
			parts[ci] = strings.Repeat("─", w+2)
		}
		return l + strings.Join(parts, mid) + r
	}
	renderCells := func(cells []string, bold bool) string {
		parts := make([]string, numCols)
		for ci := 0; ci < numCols; ci++ {
			cell := ""
			if ci < len(cells) {
				cell = cells[ci]
			}
			txt := TruncateToWidthPad(renderInline(cell, th, ""), colWidths[ci], "...")
			if bold {
				txt = th.Bold(txt)
			}
			parts[ci] = " " + txt + " "
		}
		return "│" + strings.Join(parts, "│") + "│"
	}
	var out []string
	out = append(out, rule("┌", "┬", "┐"))
	out = append(out, renderCells(header, true))
	out = append(out, rule("├", "┼", "┤"))
	for ri, row := range rows {
		out = append(out, renderCells(row, false))
		if ri < len(rows)-1 {
			out = append(out, rule("├", "┼", "┤"))
		}
	}
	out = append(out, rule("└", "┴", "┘"))
	return i, out
}

var linkRegex = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)

// renderInline styles bold/italic/strike/inline-code/links inside one block.
// stylePrefix is re-applied after every styled span (pi getStylePrefix).
func renderInline(text string, th MarkdownTheme, stylePrefix string) string {
	var b strings.Builder
	i := 0
	for i < len(text) {
		// Inline code span.
		if text[i] == '`' {
			if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
				b.WriteString(th.Code(text[i+1 : i+1+end]))
				b.WriteString(stylePrefix)
				i += end + 2
				continue
			}
		}
		// Bold: ** or __
		if strings.HasPrefix(text[i:], "**") || strings.HasPrefix(text[i:], "__") {
			delim := text[i : i+2]
			if end := strings.Index(text[i+2:], delim); end > 0 {
				inner := renderInline(text[i+2:i+2+end], th, stylePrefix)
				b.WriteString(th.Bold(inner))
				b.WriteString(stylePrefix)
				i += end + 4
				continue
			}
		}
		// Strikethrough: ~~
		if strings.HasPrefix(text[i:], "~~") {
			if end := strings.Index(text[i+2:], "~~"); end > 0 {
				inner := text[i+2 : i+2+end]
				if !strings.HasPrefix(inner, " ") && !strings.HasSuffix(inner, " ") {
					b.WriteString(th.Strikethrough(renderInline(inner, th, stylePrefix)))
					b.WriteString(stylePrefix)
					i += end + 4
					continue
				}
			}
		}
		// Italic: * or _
		if text[i] == '*' || text[i] == '_' {
			delim := text[i]
			if end := strings.IndexByte(text[i+1:], delim); end > 0 {
				inner := text[i+1 : i+1+end]
				if inner != "" && !strings.HasPrefix(inner, " ") && !strings.HasSuffix(inner, " ") {
					b.WriteString(th.Italic(renderInline(inner, th, stylePrefix)))
					b.WriteString(stylePrefix)
					i += end + 2
					continue
				}
			}
		}
		// Link: [text](url)
		if text[i] == '[' {
			if m := linkRegex.FindStringSubmatchIndex(text[i:]); m != nil && m[0] == 0 {
				label := text[i+m[2] : i+m[3]]
				url := text[i+m[4] : i+m[5]]
				styled := th.Link(th.Underline(label))
				if th.Hyperlink != nil {
					b.WriteString(th.Hyperlink(styled, url))
				} else {
					b.WriteString(styled)
					display := strings.TrimPrefix(url, "mailto:")
					if label != url && label != display {
						b.WriteString(th.LinkURL(" (" + url + ")"))
					}
				}
				b.WriteString(stylePrefix)
				i += m[1]
				continue
			}
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

func prefixLines(prefix string, lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, prefix+l)
	}
	return out
}

// FormatTokenCount renders 1234 as "1.2k" (pi footer formatTokens).
func FormatTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1_000_000)) + "M"
	case n >= 1_000:
		return trimZero(fmt.Sprintf("%.1f", float64(n)/1_000)) + "k"
	default:
		return strconv.Itoa(n)
	}
}

func trimZero(s string) string {
	s = strings.TrimSuffix(s, ".0")
	return s
}
