// Package mention is the one grammar of "@" references in a prompt and the
// pieces every surface shares around it: where a path points (paths.go), how a
// workspace is indexed for completion (index.go), how candidates are ranked
// (fuzzy.go) and how a resolved reference is written into the user message
// (attachment.go).
//
// A mention is resolved once, when its message enters the conversation, and
// what it resolved to is persisted with that message. Nothing here is read
// again for a later turn, so a file that changes afterwards never rewrites a
// message the provider has already cached.
package mention

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Scheme names a meta reference: "@session:<id>", "@rule:<name>",
// "@agent:<name>", "@coddy:<page>#<section>". A token without a scheme is a
// path.
type Scheme string

// The meta schemes the grammar recognises. Anything else before a colon is
// read as a path, so "@C:\x" stays a Windows path and "@f.go:21-31" a range.
const (
	SchemeSession Scheme = "session"
	SchemeRule    Scheme = "rule"
	SchemeAgent   Scheme = "agent"
	// SchemeCoddy names a page of Coddy's own documentation, built into the
	// binary: "@coddy:features/mentions#completion".
	SchemeCoddy Scheme = "coddy"
)

// Schemes lists the meta schemes in the order a picker offers them.
var Schemes = []Scheme{SchemeSession, SchemeRule, SchemeAgent, SchemeCoddy}

// ParseScheme reports whether s names a meta scheme.
func ParseScheme(s string) (Scheme, bool) {
	for _, sc := range Schemes {
		if s == string(sc) {
			return sc, true
		}
	}
	return "", false
}

// Range is a 1-based inclusive line range. The zero value is the whole file.
type Range struct {
	Start int
	End   int
}

// IsZero reports whether the range selects the whole file.
func (r Range) IsZero() bool { return r.Start == 0 && r.End == 0 }

// PathReading is one way to read a path token. A token typed in prose is
// ambiguous where it ends: "@README.md." may be a file named "README.md." or
// "README.md" at the end of a sentence, and "@notes draft.md" a name with a
// space or a file followed by a word. The grammar lists every reading, longest
// first, and the resolver takes the first one that exists on disk.
type PathReading struct {
	Path  string
	Range Range
	// End is the byte offset just past this reading in the parsed text.
	End int
}

// Token is one "@" reference in a text.
type Token struct {
	// Start is the offset of the "@"; End is just past the longest reading
	// (or the scheme reference), which is the span a surface highlights.
	Start int
	End   int
	// Scheme and Ref are set for a meta reference ("@session:sess_1").
	Scheme Scheme
	Ref    string
	// URL is set for a web page: "@https://example.com/page".
	URL string
	// Quoted marks a path written as @"a path with spaces".
	Quoted bool
	// Readings are the path readings of a path token, longest first.
	Readings []PathReading
}

// IsPath reports whether the token names a filesystem path.
func (t Token) IsPath() bool { return t.Scheme == "" && t.URL == "" }

// Parse returns the "@" references of text in document order. A reference
// starts at an "@" that opens the text or follows whitespace or an opening
// bracket or quote; one inside a fenced code block, an inline code span or a
// blockquote line is prose. Parse never touches the filesystem: which reading
// of a path token is meant is the resolver's call.
//
// Mirrored for the composer's highlighting by external/ui draftAt.ts
// (listAtPathSpans); the two test suites share their literals.
func Parse(text string) []Token {
	var out []Token
	inFence := false
	for lineStart := 0; lineStart <= len(text); {
		lineEnd := strings.IndexByte(text[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(text)
		} else {
			lineEnd += lineStart
		}
		lead := strings.TrimLeft(text[lineStart:lineEnd], " \t")
		switch {
		case strings.HasPrefix(lead, "```"):
			// A fence line opens or closes a code block and holds no reference.
			inFence = !inFence
		case inFence, strings.HasPrefix(lead, ">"):
			// Code and quoted text are prose.
		default:
			out = append(out, parseLine(text, lineStart, lineEnd)...)
		}
		lineStart = lineEnd + 1
	}
	return out
}

// parseLine reads the references of one line. No form of reference crosses a
// line break, so a line is parsed on its own.
func parseLine(text string, lineStart, lineEnd int) []Token {
	var out []Token
	for i := lineStart; i < lineEnd; {
		j := strings.IndexByte(text[i:lineEnd], '@')
		if j < 0 {
			break
		}
		j += i
		i = j + 1
		if !mentionContextOK(text, lineStart, j) {
			continue
		}
		if tok, ok := parseAt(text, j); ok {
			out = append(out, tok)
			if tok.End > i {
				i = tok.End
			}
		}
	}
	return out
}

// mentionContextOK reports whether the "@" at j may open a reference: it
// starts the line or follows whitespace, an opening bracket or a quote, and no
// inline code span is open before it ("`@Override`" is code).
func mentionContextOK(text string, lineStart, j int) bool {
	if j > 0 {
		prev, _ := utf8.DecodeLastRuneInString(text[:j])
		if !unicode.IsSpace(prev) && !strings.ContainsRune(`([{"'`, prev) {
			return false
		}
	}
	return strings.Count(text[lineStart:j], "`")%2 == 0
}

func parseAt(text string, at int) (Token, bool) {
	rest := text[at+1:]
	if strings.HasPrefix(rest, `"`) {
		return parseQuoted(text, at)
	}
	if strings.HasPrefix(rest, "https://") || strings.HasPrefix(rest, "http://") {
		return parseURL(text, at)
	}
	if tok, ok := parseScheme(text, at); ok {
		return tok, true
	}
	return parsePathRun(text, at)
}

// parseQuoted reads @"a path" with an optional range suffix after the quote.
func parseQuoted(text string, at int) (Token, bool) {
	start := at + 2
	end := strings.IndexAny(text[start:], "\"\n")
	if end < 0 || text[start+end] != '"' {
		return Token{}, false
	}
	path := text[start : start+end]
	if strings.TrimSpace(path) == "" {
		return Token{}, false
	}
	after := start + end + 1
	reading := PathReading{Path: path, End: after}
	if r, next, ok := parseRangeSuffix(text, after); ok {
		reading.Range, reading.End = r, next
	}
	return Token{Start: at, End: reading.End, Quoted: true, Readings: []PathReading{reading}}, true
}

// parseURL reads "@https://..." up to the next whitespace. Punctuation that
// closes a sentence is not part of the address, nor is a closing bracket the
// address never opened: "(see @https://x.dev/a)" names https://x.dev/a.
func parseURL(text string, at int) (Token, bool) {
	start := at + 1
	end := start
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if unicode.IsSpace(r) || r == '"' || r == '<' || r == '>' || r == '`' {
			break
		}
		end += size
	}
	for end > start {
		c := text[end-1]
		trim := strings.IndexByte(".,;:!?'", c) >= 0
		if (c == ')' && !strings.Contains(text[start:end-1], "(")) ||
			(c == ']' && !strings.Contains(text[start:end-1], "[")) ||
			(c == '}' && !strings.Contains(text[start:end-1], "{")) {
			trim = true
		}
		if !trim {
			break
		}
		end--
	}
	u := text[start:end]
	host := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if host == "" || strings.HasPrefix(host, "/") {
		return Token{}, false
	}
	return Token{Start: at, End: end, URL: u}, true
}

// parseScheme reads "@session:<ref>", "@rule:<ref>", "@agent:<ref>" and
// "@coddy:<ref>". A reference is a run of letters, digits, "_", "-" and ".";
// a documentation page also takes "/" and "#" (features/mentions#completion).
// A trailing ".", and for a page a trailing "/" or "#", is the end of a
// sentence or of an unfinished reference, not part of the name.
func parseScheme(text string, at int) (Token, bool) {
	rest := text[at+1:]
	colon := strings.IndexByte(rest, ':')
	if colon <= 0 {
		return Token{}, false
	}
	scheme, ok := ParseScheme(rest[:colon])
	if !ok {
		return Token{}, false
	}
	refStart := at + 1 + colon + 1
	page := scheme == SchemeCoddy
	k := refStart
	for k < len(text) {
		c := text[k]
		if c == '_' || c == '-' || c == '.' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(page && (c == '/' || c == '#')) {
			k++
			continue
		}
		break
	}
	trim := "."
	if page {
		trim = "./#"
	}
	ref := strings.TrimRight(text[refStart:k], trim)
	if ref == "" {
		return Token{}, false
	}
	return Token{Start: at, End: refStart + len(ref), Scheme: scheme, Ref: ref}, true
}

// isPathRune reports whether r continues a path run. prev is the rune before
// it in the run (utf8.RuneError at the start), and runLen the bytes read so far.
func isPathRune(r, prev rune, runLen int, next rune) bool {
	switch r {
	case '.', '/', '\\', '_', '-', '~', '+':
		return true
	case '@':
		// A scoped package folder: node_modules/@types/node.
		return prev == '/' || prev == '\\'
	case ':':
		// A drive letter: C:\Users or C:/Users. Anywhere else a colon ends the
		// path, so a ":21-31" suffix stays a line range.
		return runLen == 1 && isASCIILetter(prev) && (next == '/' || next == '\\')
	}
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func isASCIILetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

// parsePathRun reads an unquoted path, with the space-continued and
// punctuation-trimmed readings it may also stand for.
func parsePathRun(text string, at int) (Token, bool) {
	n := len(text)
	k := at + 1
	prev := utf8.RuneError
	// cuts are the offsets where a space continuation began: every one of them
	// is also the end of a shorter reading.
	var cuts []int
	for k < n {
		r, size := utf8.DecodeRuneInString(text[k:])
		if r == utf8.RuneError && size <= 1 {
			break
		}
		next := utf8.RuneError
		if k+size < n {
			next, _ = utf8.DecodeRuneInString(text[k+size:])
		}
		if isPathRune(r, prev, k-(at+1), next) {
			prev = r
			k += size
			continue
		}
		if (r == ' ' || r == '\t') && k > at+1 && continuesPath(text[k+size:]) {
			cuts = append(cuts, k)
			prev = r
			k += size
			continue
		}
		break
	}
	if k == at+1 {
		return Token{}, false
	}
	var readings []PathReading
	seen := map[string]bool{}
	add := func(path string, rng Range, end int) {
		if !validPathReading(path) {
			return
		}
		key := path
		if !rng.IsZero() {
			key += "#" + rangeKey(rng)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		readings = append(readings, PathReading{Path: path, Range: rng, End: end})
	}
	full := text[at+1 : k]
	fullEnd := k
	ranged := false
	if r, next, ok := parseRangeSuffix(text, k); ok {
		add(full, r, next)
		fullEnd = next
		ranged = true
	}
	ends := append([]int{k}, reverseInts(cuts)...)
	for idx, end := range ends {
		if idx == 0 && ranged {
			// The ranged reading is the only one of the full run: a range the
			// file cannot honour must never widen into the whole file.
			continue
		}
		p := text[at+1 : end]
		add(p, Range{}, end)
		if trimmed := strings.TrimRight(p, "."); trimmed != p {
			add(trimmed, Range{}, at+1+len(trimmed))
		}
	}
	if len(readings) == 0 {
		return Token{}, false
	}
	return Token{Start: at, End: max(fullEnd, readings[0].End), Readings: readings}, true
}

// continuesPath reports whether the word after a space still looks like part
// of a path ("notes draft.md"): a word that carries a "/" or a ".". It is one
// reading among others, never the only one, so a wrong guess costs nothing.
func continuesPath(after string) bool {
	end := 0
	for end < len(after) {
		r, size := utf8.DecodeRuneInString(after[end:])
		if r == utf8.RuneError && size <= 1 {
			break
		}
		if end == 0 && !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' {
			return false
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' || r == '.' || r == '/' {
			end += size
			continue
		}
		break
	}
	word := strings.TrimRight(after[:end], ".")
	return word != "" && strings.ContainsAny(word, "/.")
}

// validPathReading drops readings that name nothing a user means: an empty
// token, the bare root, a lone "~", or dots alone ("@." and "@.." in prose).
func validPathReading(p string) bool {
	switch strings.TrimSpace(p) {
	case "", "/", `\`, "~", ".", "..":
		return false
	}
	return strings.Trim(p, ".") != ""
}

func reverseInts(v []int) []int {
	out := make([]int, len(v))
	for i := range v {
		out[len(v)-1-i] = v[i]
	}
	return out
}

// parseRangeSuffix reads a line range right after a path: ":<start>-<end>",
// "#L<start>-<end>", "#L<start>-L<end>", "#L<line>", "#<start>-<end>" or
// "#<line>". A range counts only
// when 1 <= start <= end and the token ends there - the next rune is not a
// letter, a digit or "-" - so ":21-31x" and ":21" stay prose. Mirrors
// external/ui draftAt.parseAtLineRangeSuffix.
func parseRangeSuffix(text string, k int) (Range, int, bool) {
	n := len(text)
	if k >= n {
		return Range{}, k, false
	}
	p := k
	switch {
	case text[p] == ':':
		p++
		first, q, ok := digits(text, p)
		if !ok || q >= n || text[q] != '-' {
			return Range{}, k, false
		}
		second, q2, ok := digits(text, q+1)
		if !ok {
			return Range{}, k, false
		}
		return finishRange(text, first, second, q2, k)
	case strings.HasPrefix(text[p:], "#"):
		// "#L10-20", "#L10-L20" and "#L10" (GitHub, Claude Code), or "#10-20"
		// and "#10" (OpenCode).
		p++
		if p < n && text[p] == 'L' {
			p++
		}
		first, q, ok := digits(text, p)
		if !ok {
			return Range{}, k, false
		}
		second := first
		if q < n && text[q] == '-' {
			q++
			if q < n && text[q] == 'L' {
				q++
			}
			var ok2 bool
			second, q, ok2 = digits(text, q)
			if !ok2 {
				return Range{}, k, false
			}
		}
		return finishRange(text, first, second, q, k)
	}
	return Range{}, k, false
}

func finishRange(text string, start, end, next, k int) (Range, int, bool) {
	if next < len(text) {
		r, _ := utf8.DecodeRuneInString(text[next:])
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '-' {
			return Range{}, k, false
		}
	}
	if start < 1 || end < start {
		return Range{}, k, false
	}
	return Range{Start: start, End: end}, next, true
}

// digits reads 1 to 9 ASCII digits at p; a longer run is not a line number.
func digits(text string, p int) (int, int, bool) {
	q := p
	for q < len(text) && text[q] >= '0' && text[q] <= '9' {
		q++
	}
	if q == p || q-p > 9 {
		return 0, p, false
	}
	v := 0
	for _, c := range text[p:q] {
		v = v*10 + int(c-'0')
	}
	return v, q, true
}

func rangeKey(r Range) string {
	return itoa(r.Start) + "-" + itoa(r.End)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
