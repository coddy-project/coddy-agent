package docsgen

import (
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
)

// A translator copies the links of the English page as they are, and
// localizeLinks makes them right for the translation's folder: a file outside
// the translation (an asset, a design record, a file of the repository) gets
// the extra "../" the deeper folder needs, and an anchor that names an English
// heading of a translated page becomes the anchor GitHub gives the translated
// heading at the same position. A link already written for the translation is
// left as it is, so make docs can run it over every translated page.

// localizeLinks rewrites the links of the translated page rel (docs/<lang>/...)
// whose text is given; read returns a file of the tree as this run sees it
// and exists reports one.
func localizeLinks(rel, text string, read func(string) (string, error), exists func(string) bool) string {
	_, source := sourceOf(rel)
	if source == "" {
		return text
	}
	headings := map[string][]docs.Heading{}
	parse := func(file string) []docs.Heading {
		if h, ok := headings[file]; ok {
			return h
		}
		s, err := read(file)
		if err != nil {
			headings[file] = nil
			return nil
		}
		h := docs.ParseHeadings(s)
		headings[file] = h
		return h
	}
	// anchor maps a fragment naming a heading of the English source of a
	// translated page to the translated heading's own anchor.
	anchor := func(translated, frag string) string {
		_, src := sourceOf(translated)
		if src == "" {
			return frag
		}
		f := normalFragment(frag)
		local := parse(translated)
		for _, h := range local {
			if h.Local == f {
				return frag
			}
		}
		shared := parse(src)
		for i, h := range shared {
			if h.Anchor == f && i < len(local) && local[i].Level == h.Level {
				return local[i].Local
			}
		}
		return frag
	}
	target := func(t string) string {
		if strings.HasPrefix(t, "{{") || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "/") {
			return t
		}
		if u, err := url.Parse(t); err != nil || u.Scheme != "" {
			return t
		}
		p, frag, hasFrag := strings.Cut(t, "#")
		if p == "" {
			if !hasFrag {
				return t
			}
			return "#" + anchor(rel, frag)
		}
		unescaped, err := url.PathUnescape(p)
		if err != nil {
			unescaped = p
		}
		// What the link means on the English page decides: a page of the
		// documentation is its translation, anything else the same file. A
		// link that names nothing from the English folder is taken as already
		// written for the translation's.
		var file string
		if there := path.Clean(path.Join(path.Dir(source), unescaped)); !strings.HasPrefix(there, "../") && exists(there) {
			file = there
			if tr := translationOf(rel, there); tr != "" && exists(tr) {
				file = tr
			}
			p = relativePath(path.Dir(rel), file)
		} else if here := path.Clean(path.Join(path.Dir(rel), unescaped)); !strings.HasPrefix(here, "../") && exists(here) {
			file = here
		} else {
			return t // CheckLinks reports it
		}
		if hasFrag && strings.HasSuffix(file, ".md") {
			frag = anchor(file, frag)
		}
		if hasFrag {
			return p + "#" + frag
		}
		return p
	}
	lines := strings.Split(text, "\n")
	fence := false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
			continue
		}
		if fence || (!strings.Contains(line, "](") && !strings.Contains(line, `="`) && !strings.HasPrefix(line, "[")) {
			continue
		}
		lines[i] = replaceTargets(line, target)
	}
	return strings.Join(lines, "\n")
}

var (
	targetInlineRE = regexp.MustCompile(`\]\(([^)\s]+)`)
	targetAttrRE   = regexp.MustCompile(`(?:src|href)="([^"]+)`)
	targetRefDefRE = regexp.MustCompile(`^\[[^\]\n]+\]:[ \t]+(\S+)`)
)

// replaceTargets rewrites every link target of a line outside its code
// spans: an inline link or image (whatever its text holds, code included),
// an HTML src or href, a reference definition.
func replaceTargets(line string, fn func(string) string) string {
	code := codeSpans(line)
	inCode := func(i int) bool {
		for _, r := range code {
			if i >= r[0] && i < r[1] {
				return true
			}
		}
		return false
	}
	type edit struct{ start, end int }
	var edits []edit
	for _, re := range []*regexp.Regexp{targetInlineRE, targetAttrRE, targetRefDefRE} {
		for _, m := range re.FindAllStringSubmatchIndex(line, -1) {
			if !inCode(m[0]) {
				edits = append(edits, edit{m[2], m[3]})
			}
		}
	}
	if len(edits) == 0 {
		return line
	}
	sort.Slice(edits, func(a, b int) bool { return edits[a].start > edits[b].start })
	for _, e := range edits {
		line = line[:e.start] + fn(line[e.start:e.end]) + line[e.end:]
	}
	return line
}

// codeSpans returns the byte ranges of a line's inline code spans.
func codeSpans(line string) [][2]int {
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

// translationOf is the translated counterpart of an English documentation
// file in the language of the translated page rel: docs/<lang>/<path> for a
// page under docs/ (the hub included), "" for anything else.
func translationOf(rel, file string) string {
	lang, _ := sourceOf(rel)
	rest, ok := strings.CutPrefix(file, "docs/")
	if !ok || lang == "" || !strings.HasSuffix(rest, ".md") || strings.HasPrefix(rest, "plans/") || strings.HasPrefix(rest, "assets/") {
		return ""
	}
	for _, l := range Translations {
		if strings.HasPrefix(rest, l+"/") {
			return ""
		}
	}
	return TranslationDir(lang) + rest
}

// relativePath is the path of file (from the repository root) as a link from
// the folder dir writes it.
func relativePath(dir, file string) string {
	from := strings.Split(path.Clean(dir), "/")
	to := strings.Split(path.Clean(file), "/")
	i := 0
	for i < len(from) && i < len(to)-1 && from[i] == to[i] {
		i++
	}
	parts := make([]string, 0, len(from)-i+len(to)-i)
	for range from[i:] {
		parts = append(parts, "..")
	}
	return strings.Join(append(parts, to[i:]...), "/")
}
