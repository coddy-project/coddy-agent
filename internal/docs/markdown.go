package docs

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
)

// Heading is one heading of a page, outside fenced code.
type Heading struct {
	Level int
	// Text is the heading with its inline markup removed, for display.
	Text string
	// Anchor is the fragment GitHub generates for the heading, numbered
	// from -1 when the same text repeats on the page.
	Anchor string
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
	for _, h := range parseHeadings(strings.Split(markdown, "\n")) {
		out = append(out, h.Anchor)
	}
	return out
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
		out = append(out, Heading{Level: level, Text: inlineText(title), Anchor: anchor, Line: i})
	}
	return out
}

func (p *Page) heading(anchor string) (Heading, bool) {
	for _, h := range p.Headings {
		if h.Anchor == anchor {
			return h, true
		}
	}
	return Heading{}, false
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
	// linkTargetRE finds an inline link or image: the prefix up to "(" and
	// the target up to a space, a closing parenthesis or a title.
	linkTargetRE = regexp.MustCompile(`(!?\[[^\]]*\]\()([^)\s]+)`)
	// refDefRE is a reference-style definition "[name]: target".
	refDefRE = regexp.MustCompile(`^(\[[^\]\n]+\]:[ \t]+)(\S+)`)
	// htmlAttrRE is an HTML src= or href= attribute.
	htmlAttrRE = regexp.MustCompile(`((?:src|href)=")([^"]+)`)
)

// rewriteLinks makes a page readable outside the repository. A link to
// another page of the documentation becomes coddy:<slug>#<anchor>, so a
// reader opens it in place; a fragment of the page itself becomes a
// coddy: link too, because a hash-routed reader has no other fragment to
// give it. An image or a link to a file of the repository becomes its
// address on GitHub at the release the binary was built from. Absolute
// URLs and everything inside code are left alone.
func rewriteLinks(md, slug, ref string) string {
	lines := strings.Split(md, "\n")
	var f fence
	for i, line := range lines {
		if f.step(line) {
			continue
		}
		if video, ok := attachmentVideo(lines, i, slug, ref); ok {
			lines[i] = video
			continue
		}
		inline := strings.Contains(line, "](")
		refDef := strings.HasPrefix(line, "[") && strings.Contains(line, "]:")
		attr := strings.Contains(line, `src="`) || strings.Contains(line, `href="`)
		if !inline && !refDef && !attr {
			continue
		}
		lines[i] = outsideCode(line, func(text string) string {
			if inline {
				text = linkTargetRE.ReplaceAllStringFunc(text, func(m string) string {
					sub := linkTargetRE.FindStringSubmatch(m)
					return sub[1] + rewriteTarget(sub[2], slug, ref, strings.HasPrefix(sub[1], "!"))
				})
			}
			if refDef {
				text = refDefRE.ReplaceAllStringFunc(text, func(m string) string {
					sub := refDefRE.FindStringSubmatch(m)
					return sub[1] + rewriteTarget(sub[2], slug, ref, false)
				})
			}
			if attr {
				text = htmlAttrRE.ReplaceAllStringFunc(text, func(m string) string {
					sub := htmlAttrRE.FindStringSubmatch(m)
					return sub[1] + rewriteTarget(sub[2], slug, ref, strings.HasPrefix(sub[1], "src"))
				})
			}
			return text
		})
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
func attachmentVideo(lines []string, i int, slug, ref string) (string, bool) {
	if !attachmentRE.MatchString(strings.TrimSpace(lines[i])) {
		return "", false
	}
	for k := i + 1; k < len(lines) && k <= i+4; k++ {
		if m := videoCopyRE.FindStringSubmatch(lines[k]); m != nil {
			return "![Video: " + m[2] + "](" + rewriteTarget(m[1], slug, ref, true) + ")", true
		}
	}
	return "", false
}

func rewriteTarget(target, slug, ref string, image bool) string {
	if strings.HasPrefix(target, "#") {
		return LinkScheme + slug + target
	}
	if u, err := url.Parse(target); err != nil || u.Scheme != "" || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "{{") {
		return target
	}
	file, frag, _ := strings.Cut(target, "#")
	file, _ = url.PathUnescape(file)
	// The page lives at docs/<slug>.md; the target is relative to its folder.
	repoPath := path.Clean(path.Join("docs", path.Dir(slug), file))
	if strings.HasPrefix(repoPath, "../") {
		return target
	}
	if !image && strings.HasPrefix(repoPath, "docs/") && strings.HasSuffix(repoPath, ".md") {
		page := strings.TrimSuffix(strings.TrimPrefix(repoPath, "docs/"), ".md")
		if !strings.HasPrefix(page, "plans/") && page != "README" {
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

// outsideCode applies fn to the parts of a line outside inline code spans.
func outsideCode(line string, fn func(string) string) string {
	if !strings.Contains(line, "`") {
		return fn(line)
	}
	var b strings.Builder
	rest := line
	for {
		open := strings.Index(rest, "`")
		if open < 0 {
			b.WriteString(fn(rest))
			return b.String()
		}
		n := 0
		for open+n < len(rest) && rest[open+n] == '`' {
			n++
		}
		fence := rest[open : open+n]
		closeAt := strings.Index(rest[open+n:], fence)
		if closeAt < 0 {
			b.WriteString(fn(rest))
			return b.String()
		}
		end := open + n + closeAt + n
		b.WriteString(fn(rest[:open]))
		b.WriteString(rest[open:end])
		rest = rest[end:]
	}
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
