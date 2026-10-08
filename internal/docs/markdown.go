package docs

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Heading is one heading of a page, outside fenced code.
type Heading struct {
	Level int
	// Text is the heading with its inline markup removed, for display.
	Text string
	// Anchor is the fragment GitHub generates for the heading, numbered
	// from -1 when the same text repeats on the page. On a translated page
	// it is the anchor of the English heading at the same position, so one
	// address names the section in every language.
	Anchor string
	// Local is the fragment GitHub generates for the heading as written on
	// this page: the Anchor of an English page, the anchor of the translated
	// text on a translated one.
	Local string
	// Line is the heading's index in the page's lines, from 0.
	Line int
}

// Anchor converts a heading to the anchor GitHub generates for it: inline
// markup removed, lower-cased, punctuation dropped, spaces turned into
// hyphens. internal/docsgen checks the links of the tree with the same
// function, so an anchor the check accepts is one the reader finds.
func Anchor(heading string) string {
	r := strings.NewReplacer("`", "", "*", "", "[", "", "]", "", "(", "", ")", "")
	h := strings.ToLower(r.Replace(heading))
	var b strings.Builder
	for _, c := range h {
		switch {
		case unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_' || c == '-':
			b.WriteRune(c)
		case c == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// fence tracks fenced code blocks line by line. A fence opens at a line
// that starts (indentation aside) with three backticks or tildes, an info
// string allowed, and closes at the next line that holds nothing but such a
// run; three backticks in running prose are not a fence.
type fence struct{ open bool }

// step reports whether the line is code or a fence marker, which no reader
// of prose should look into, and advances the state.
func (f *fence) step(line string) bool {
	t := strings.TrimRight(strings.TrimLeft(line, " \t"), " \t\r")
	marker := isFenceMarker(t)
	switch {
	case f.open:
		if marker && strings.Trim(t, "`~") == "" {
			f.open = false
		}
		return true
	case marker:
		f.open = true
		return true
	}
	return false
}

func isFenceMarker(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// HeadingAnchors returns the anchor of every heading of a Markdown text, in
// order, numbered the way GitHub numbers a repeated heading.
func HeadingAnchors(markdown string) []string {
	var out []string
	for _, h := range ParseHeadings(markdown) {
		out = append(out, h.Anchor)
	}
	return out
}

// ParseHeadings returns every heading of a Markdown text outside fenced
// code, in order, with the anchor GitHub gives it (Anchor and Local alike).
func ParseHeadings(markdown string) []Heading {
	return parseHeadings(strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n"))
}

func parseHeadings(lines []string) []Heading {
	var out []Heading
	seen := map[string]int{}
	var f fence
	for i, line := range lines {
		if f.step(line) || !strings.HasPrefix(line, "#") {
			continue
		}
		level := len(line) - len(strings.TrimLeft(line, "#"))
		title := line[level:]
		if !strings.HasPrefix(title, " ") {
			continue
		}
		title = strings.TrimSpace(title)
		anchor := Anchor(title)
		if n := seen[anchor]; n > 0 {
			seen[anchor]++
			anchor = fmt.Sprintf("%s-%d", anchor, n)
		} else {
			seen[anchor] = 1
		}
		out = append(out, Heading{Level: level, Text: inlineText(title), Anchor: anchor, Local: anchor, Line: i})
	}
	return out
}

// heading finds the heading an anchor names: its shared anchor or its own,
// in any case, written or percent-encoded.
func (p *Page) heading(anchor string) (Heading, bool) {
	a := normalAnchor(anchor)
	for _, h := range p.Headings {
		if h.Anchor == a {
			return h, true
		}
	}
	for _, h := range p.Headings {
		if h.Local == a {
			return h, true
		}
	}
	return Heading{}, false
}

// normalAnchor is a fragment as anchors are written: percent-decoded and
// lower-cased.
func normalAnchor(anchor string) string {
	a := strings.TrimSpace(anchor)
	if strings.Contains(a, "%") {
		if u, err := url.PathUnescape(a); err == nil {
			a = u
		}
	}
	return strings.ToLower(a)
}

func (p *Page) anchors() []string {
	var out []string
	for _, h := range p.Headings {
		if h.Level > 1 {
			out = append(out, h.Anchor)
		}
	}
	return out
}

// Section returns the Markdown of the section an anchor names: its heading
// and everything under it up to the next heading of the same or a higher
// level, subsections included.
func (p *Page) Section(anchor string) (Heading, string, bool) {
	h, ok := p.heading(anchor)
	if !ok {
		return Heading{}, "", false
	}
	start, end := p.sectionLines(h)
	return h, strings.Join(p.lines[start:end], "\n"), true
}

// sectionLines is the half-open line range of a heading's section.
func (p *Page) sectionLines(h Heading) (int, int) {
	end := len(p.lines)
	for _, other := range p.Headings {
		if other.Line > h.Line && other.Level <= h.Level {
			end = other.Line
			break
		}
	}
	return h.Line, trimTrailingBlank(p.lines, h.Line, end)
}

func trimTrailingBlank(lines []string, start, end int) int {
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return end
}

// Lines is the number of lines of the page.
func (p *Page) Lines() int { return len(p.lines) }

var inlineMarkupRE = strings.NewReplacer("`", "", "**", "", "__", "")

// inlineText strips the inline markup of a heading or a line for display:
// code spans, emphasis, links reduced to their text.
func inlineText(s string) string {
	return strings.TrimSpace(inlineMarkupRE.Replace(linkText(s)))
}

// linkText reduces every [text](target) and ![alt](target) of a line to
// its text, the way mdLinkRE reads them, without a regular expression: the
// search index runs it over every line of the documentation.
func linkText(s string) string {
	if !strings.Contains(s, "](") {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		open := strings.IndexByte(s[i:], '[')
		if open < 0 {
			break
		}
		open += i
		mid := strings.IndexByte(s[open+1:], ']')
		if mid < 0 {
			break
		}
		mid += open + 1
		if mid+1 >= len(s) || s[mid+1] != '(' {
			b.WriteString(s[i : mid+1])
			i = mid + 1
			continue
		}
		end := strings.IndexByte(s[mid+2:], ')')
		if end < 0 {
			break
		}
		end += mid + 2
		start := open
		if start > i && s[start-1] == '!' {
			start--
		}
		b.WriteString(s[i:start])
		b.WriteString(s[open+1 : mid])
		i = end + 1
	}
	b.WriteString(s[i:])
	return b.String()
}

var (
	// linkTargetRE finds the target of an inline link or image, after its
	// "](" and up to a space, a closing parenthesis or a title. The link's
	// text may hold anything, a code span included.
	linkTargetRE = regexp.MustCompile(`\]\(([^)\s]+)`)
	// refDefRE is a reference-style definition "[name]: target".
	refDefRE = regexp.MustCompile(`^\[[^\]\n]+\]:[ \t]+(\S+)`)
	// htmlAttrRE is an HTML src= or href= attribute.
	htmlAttrRE = regexp.MustCompile(`(src|href)="([^"]+)`)
)

// rewriteLineTargets rewrites the link targets of one line outside its code
// spans: inline links and images, reference definitions, HTML src and href.
func rewriteLineTargets(line, file, slug, ref string, shared anchorFunc) string {
	spans := codeSpanRanges(line)
	inCode := func(i int) bool {
		for _, r := range spans {
			if i >= r[0] && i < r[1] {
				return true
			}
		}
		return false
	}
	type edit struct {
		start, end int
		image      bool
	}
	var edits []edit
	for _, m := range linkTargetRE.FindAllStringSubmatchIndex(line, -1) {
		if inCode(m[0]) {
			continue
		}
		// An image is the link whose text opens with "![".
		open := -1
		for k := m[0] - 1; k >= 0; k-- {
			if line[k] == '[' && !inCode(k) {
				open = k
				break
			}
		}
		edits = append(edits, edit{m[2], m[3], open > 0 && line[open-1] == '!'})
	}
	for _, m := range refDefRE.FindAllStringSubmatchIndex(line, -1) {
		edits = append(edits, edit{m[2], m[3], false})
	}
	for _, m := range htmlAttrRE.FindAllStringSubmatchIndex(line, -1) {
		if !inCode(m[0]) {
			edits = append(edits, edit{m[4], m[5], line[m[2]:m[3]] == "src"})
		}
	}
	sort.Slice(edits, func(a, b int) bool { return edits[a].start > edits[b].start })
	for _, e := range edits {
		line = line[:e.start] + rewriteTarget(line[e.start:e.end], file, slug, ref, e.image, shared) + line[e.end:]
	}
	return line
}

// codeSpanRanges returns the byte ranges of a line's inline code spans.
func codeSpanRanges(line string) [][2]int {
	var out [][2]int
	i := 0
	for i < len(line) {
		open := strings.IndexByte(line[i:], '`')
		if open < 0 {
			break
		}
		open += i
		n := 0
		for open+n < len(line) && line[open+n] == '`' {
			n++
		}
		closeAt := strings.Index(line[open+n:], line[open:open+n])
		if closeAt < 0 {
			break
		}
		end := open + n + closeAt + n
		out = append(out, [2]int{open, end})
		i = end
	}
	return out
}

// anchorFunc maps a fragment a link names on a page to the page's shared
// anchor for that section.
type anchorFunc func(slug, frag string) string

// rewriteLinks makes a page readable outside the repository. file is the
// page's path in the repository (docs/features/mcp.md, docs/ru/features/mcp.md),
// which relative targets resolve against. A link to another page of the
// documentation, in any language folder, becomes coddy:<slug>#<anchor>, so a
// reader opens it in place; a fragment of the page itself becomes a coddy:
// link too, because a hash-routed reader has no other fragment to give it.
// The anchor is the shared one shared returns, so a translated page's link
// names the section in every language. An image or a link to a file of the
// repository becomes its address on GitHub at the release the binary was
// built from. Absolute URLs and everything inside code are left alone.
func rewriteLinks(md, file, slug, ref string, shared anchorFunc) string {
	lines := strings.Split(md, "\n")
	var f fence
	for i, line := range lines {
		if f.step(line) {
			continue
		}
		if video, ok := attachmentVideo(lines, i, file, ref); ok {
			lines[i] = video
			continue
		}
		refDef := strings.HasPrefix(line, "[") && strings.Contains(line, "]:")
		if !strings.Contains(line, "](") && !strings.Contains(line, `src="`) && !strings.Contains(line, `href="`) && !refDef {
			continue
		}
		lines[i] = rewriteLineTargets(line, file, slug, ref, shared)
	}
	return strings.Join(lines, "\n")
}

var (
	// attachmentRE is a GitHub attachment on a line of its own: how a page
	// embeds a video on GitHub (docs/contributing/documentation.md, Videos).
	attachmentRE = regexp.MustCompile(`^https://github\.com/user-attachments/assets/[0-9a-fA-F-]+$`)
	// videoCopyRE is the link to the repository copy the caption under it
	// carries.
	videoCopyRE = regexp.MustCompile(`\]\(([^)\s]*assets/video/([^)/\s]+\.(?:mp4|webm|mov)))\)`)
)

// attachmentVideo turns a GitHub attachment line into an embedded video of
// the repository copy linked in the next few lines. The attachment plays only
// inside GitHub; the copy is fetched from GitHub at the release, like an
// image, and never enters the binary. A line with no copy under it stays.
func attachmentVideo(lines []string, i int, file, ref string) (string, bool) {
	if !attachmentRE.MatchString(strings.TrimSpace(lines[i])) {
		return "", false
	}
	for k := i + 1; k < len(lines) && k <= i+4; k++ {
		if m := videoCopyRE.FindStringSubmatch(lines[k]); m != nil {
			return "![Video: " + m[2] + "](" + rewriteTarget(m[1], file, "", ref, true, nil) + ")", true
		}
	}
	return "", false
}

func rewriteTarget(target, file, slug, ref string, image bool, shared anchorFunc) string {
	if shared == nil {
		shared = func(_, frag string) string { return frag }
	}
	if frag, ok := strings.CutPrefix(target, "#"); ok {
		return LinkScheme + Ref(slug, shared(slug, frag))
	}
	if u, err := url.Parse(target); err != nil || u.Scheme != "" || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "{{") {
		return target
	}
	rel, frag, _ := strings.Cut(target, "#")
	rel, _ = url.PathUnescape(rel)
	// The target is relative to the folder of the page's file.
	repoPath := path.Clean(path.Join(path.Dir(file), rel))
	if strings.HasPrefix(repoPath, "../") {
		return target
	}
	if !image && strings.HasPrefix(repoPath, "docs/") && strings.HasSuffix(repoPath, ".md") {
		page := trimLangFolder(strings.TrimSuffix(strings.TrimPrefix(repoPath, "docs/"), ".md"))
		if !strings.HasPrefix(page, "plans/") && page != "README" {
			if frag != "" {
				frag = shared(page, frag)
			}
			return LinkScheme + Ref(page, frag)
		}
	}
	base := githubBlob
	if image {
		base = githubRaw
	}
	out := base + ref + "/" + repoPath
	if frag != "" {
		out += "#" + frag
	}
	return out
}

var htmlCommentRE = regexp.MustCompile(`<!--.*?-->`)

// isTableRule reports the line under a table header, | --- | :-: |, and
// a thematic break, neither of which a reader reads.
func isTableRule(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.Contains(t, "---") {
		return false
	}
	return strings.Trim(t, "|-: \t") == ""
}

// trimListMark drops a list marker, "- ", "* ", "+ " or "12. ".
func trimListMark(line string) string {
	if len(line) >= 2 && strings.ContainsRune("-*+", rune(line[0])) && line[1] == ' ' {
		return line[2:]
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' ' {
		return line[i+2:]
	}
	return line
}

// plainText is the text of a run of Markdown lines as a reader sees it,
// for the search index and its snippets: headings, list and table markup,
// emphasis and link targets dropped, code kept, one space between words.
func plainText(lines []string) string {
	var b strings.Builder
	var f fence
	for _, line := range lines {
		code := f.step(line)
		if code && isFenceMarker(line) {
			continue
		}
		if !code {
			if strings.Contains(line, "<!--") {
				line = htmlCommentRE.ReplaceAllString(line, "")
			}
			if isTableRule(line) {
				continue
			}
			line = trimListMark(strings.TrimLeft(line, "#> \t"))
			line = linkText(line)
			line = strings.ReplaceAll(line, "|", " ")
			line = inlineMarkupRE.Replace(line)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(line)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
