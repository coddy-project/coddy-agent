//go:build cli

package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Width and wrapping utilities ported from pi-tui (packages/tui/src/utils.ts).
// Terminal cell conventions: tab occupies 3 columns, regional indicators are 2
// columns even in isolation, ANSI/OSC/APC sequences are invisible.

const tabWidth = 3

// AnsiCode is one extracted escape sequence starting at a byte offset.
type AnsiCode struct {
	Code   string
	Length int
}

// ExtractAnsiCode extracts the escape sequence starting at pos, or nil.
// Recognized forms mirror pi-tui: CSI terminated by one of m G K H J,
// OSC (ESC ]) and APC (ESC _) terminated by BEL or ST.
func ExtractAnsiCode(s string, pos int) *AnsiCode {
	if pos >= len(s) || s[pos] != 0x1b {
		return nil
	}
	if pos+1 >= len(s) {
		return nil
	}
	switch s[pos+1] {
	case '[':
		j := pos + 2
		for j < len(s) && !isCSIFinal(s[j]) {
			j++
		}
		if j < len(s) {
			return &AnsiCode{Code: s[pos : j+1], Length: j + 1 - pos}
		}
		return nil
	case ']', '_':
		j := pos + 2
		for j < len(s) {
			if s[j] == 0x07 {
				return &AnsiCode{Code: s[pos : j+1], Length: j + 1 - pos}
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return &AnsiCode{Code: s[pos : j+2], Length: j + 2 - pos}
			}
			j++
		}
		return nil
	}
	return nil
}

func isCSIFinal(b byte) bool {
	switch b {
	case 'm', 'G', 'K', 'H', 'J':
		return true
	}
	return false
}

func isPrintableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// graphemeWidth returns the cell width of one grapheme cluster.
func graphemeWidth(g string) int {
	if g == "\t" {
		return tabWidth
	}
	r, size := firstRune(g)
	if r >= 0x1f1e6 && r <= 0x1f1ff {
		// Regional indicators render full-width even in isolation.
		return 2
	}
	// Multi-rune clusters: emoji presentation selector or ZWJ sequences are 2.
	if size < len(g) {
		if strings.ContainsRune(g, 0xfe0f) || strings.ContainsRune(g, 0x200d) {
			return 2
		}
	}
	w := uniseg.StringWidth(g)
	if w < 0 {
		return 0
	}
	// go-runewidth backstop for single wide runes uniseg may size differently.
	if size == len(g) {
		if rw := runewidth.RuneWidth(r); rw > w {
			return rw
		}
	}
	return w
}

func firstRune(s string) (rune, int) {
	for i, r := range s {
		_ = i
		return r, len(string(r))
	}
	return 0, 0
}

// VisibleWidth returns the display width of s ignoring ANSI/OSC/APC sequences.
// Tabs count as 3 columns.
func VisibleWidth(s string) int {
	if s == "" {
		return 0
	}
	if isPrintableASCII(s) {
		return len(s)
	}
	clean := StripTerminalSequences(s)
	width := 0
	g := uniseg.NewGraphemes(clean)
	for g.Next() {
		width += graphemeWidth(g.Str())
	}
	return width
}

// StripTerminalSequences removes ANSI, OSC, and APC sequences keeping visible text.
func StripTerminalSequences(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if ansi := ExtractAnsiCode(s, i); ansi != nil {
			i += ansi.Length
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// ansiTracker accumulates active SGR attributes and the open OSC 8 hyperlink so
// wrapped continuation lines can re-apply them (port of pi AnsiCodeTracker).
type ansiTracker struct {
	bold, dim, italic, underline, blink, inverse, hidden, strike bool

	fgColor string
	bgColor string

	linkParams, linkURL, linkTerm string
	linkOpen                      bool
}

func (t *ansiTracker) reset() {
	t.bold, t.dim, t.italic, t.underline, t.blink, t.inverse, t.hidden, t.strike = false, false, false, false, false, false, false, false
	t.fgColor, t.bgColor = "", ""
}

func (t *ansiTracker) process(code string) {
	if params, url, term, ok := parseOsc8(code); ok {
		if url == "" {
			t.linkOpen = false
		} else {
			t.linkParams, t.linkURL, t.linkTerm = params, url, term
			t.linkOpen = true
		}
		return
	}
	if !strings.HasSuffix(code, "m") || !strings.HasPrefix(code, "\x1b[") {
		return
	}
	body := code[2 : len(code)-1]
	if body == "" || body == "0" {
		t.reset()
		return
	}
	parts := strings.Split(body, ";")
	for i := 0; i < len(parts); i++ {
		switch parts[i] {
		case "38", "48":
			if i+2 < len(parts) && parts[i+1] == "5" {
				col := parts[i] + ";" + parts[i+1] + ";" + parts[i+2]
				if parts[i] == "38" {
					t.fgColor = col
				} else {
					t.bgColor = col
				}
				i += 2
				continue
			}
			if i+4 < len(parts) && parts[i+1] == "2" {
				col := parts[i] + ";" + parts[i+1] + ";" + parts[i+2] + ";" + parts[i+3] + ";" + parts[i+4]
				if parts[i] == "38" {
					t.fgColor = col
				} else {
					t.bgColor = col
				}
				i += 4
				continue
			}
		}
		switch parts[i] {
		case "0":
			t.reset()
		case "1":
			t.bold = true
		case "2":
			t.dim = true
		case "3":
			t.italic = true
		case "4":
			t.underline = true
		case "5":
			t.blink = true
		case "7":
			t.inverse = true
		case "8":
			t.hidden = true
		case "9":
			t.strike = true
		case "21", "22":
			t.bold, t.dim = false, false
		case "23":
			t.italic = false
		case "24":
			t.underline = false
		case "25":
			t.blink = false
		case "27":
			t.inverse = false
		case "28":
			t.hidden = false
		case "29":
			t.strike = false
		case "39":
			t.fgColor = ""
		case "49":
			t.bgColor = ""
		default:
			if n, ok := atoiSafe(parts[i]); ok {
				if (n >= 30 && n <= 37) || (n >= 90 && n <= 97) {
					t.fgColor = parts[i]
				} else if (n >= 40 && n <= 47) || (n >= 100 && n <= 107) {
					t.bgColor = parts[i]
				}
			}
		}
	}
}

func atoiSafe(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

func (t *ansiTracker) activeCodes() string {
	var codes []string
	if t.bold {
		codes = append(codes, "1")
	}
	if t.dim {
		codes = append(codes, "2")
	}
	if t.italic {
		codes = append(codes, "3")
	}
	if t.underline {
		codes = append(codes, "4")
	}
	if t.blink {
		codes = append(codes, "5")
	}
	if t.inverse {
		codes = append(codes, "7")
	}
	if t.hidden {
		codes = append(codes, "8")
	}
	if t.strike {
		codes = append(codes, "9")
	}
	if t.fgColor != "" {
		codes = append(codes, t.fgColor)
	}
	if t.bgColor != "" {
		codes = append(codes, t.bgColor)
	}
	out := ""
	if len(codes) > 0 {
		out = "\x1b[" + strings.Join(codes, ";") + "m"
	}
	if t.linkOpen {
		out += "\x1b]8;" + t.linkParams + ";" + t.linkURL + t.linkTerm
	}
	return out
}

// lineEndReset closes attributes that must not bleed into padding: underline
// and the open hyperlink (re-opened at the next line start via activeCodes).
func (t *ansiTracker) lineEndReset() string {
	out := ""
	if t.underline {
		out += "\x1b[24m"
	}
	if t.linkOpen {
		out += "\x1b]8;;" + t.linkTerm
	}
	return out
}

func (t *ansiTracker) processText(text string) {
	i := 0
	for i < len(text) {
		if ansi := ExtractAnsiCode(text, i); ansi != nil {
			t.process(ansi.Code)
			i += ansi.Length
			continue
		}
		i++
	}
}

// parseOsc8 splits an OSC 8 hyperlink sequence into params, url, terminator.
func parseOsc8(code string) (params, url, term string, ok bool) {
	if !strings.HasPrefix(code, "\x1b]8;") {
		return "", "", "", false
	}
	if strings.HasSuffix(code, "\x07") {
		term = "\x07"
		code = code[4 : len(code)-1]
	} else if strings.HasSuffix(code, "\x1b\\") {
		term = "\x1b\\"
		code = code[4 : len(code)-2]
	} else {
		return "", "", "", false
	}
	sep := strings.IndexByte(code, ';')
	if sep < 0 {
		return "", "", "", false
	}
	return code[:sep], code[sep+1:], term, true
}

func isCJKBreak(g string) bool {
	r, _ := firstRune(g)
	return (r >= 0x2e80 && r <= 0x9fff) || // CJK radicals..unified
		(r >= 0x3040 && r <= 0x30ff) || // hiragana, katakana
		(r >= 0xac00 && r <= 0xd7af) || // hangul syllables
		(r >= 0x1100 && r <= 0x11ff) || // hangul jamo
		(r >= 0xf900 && r <= 0xfaff) || // CJK compat ideographs
		(r >= 0x20000 && r <= 0x2fa1f) // extension planes
}

// WrapTextWithANSI word-wraps text to width, preserving ANSI styling across
// breaks and trimming trailing whitespace on produced lines. Newlines split
// input; active styles carry over onto following lines.
func WrapTextWithANSI(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	inputLines := strings.Split(normalized, "\n")
	var result []string
	tracker := &ansiTracker{}
	for _, inputLine := range inputLines {
		prefix := ""
		if len(result) > 0 {
			prefix = tracker.activeCodes()
		}
		result = append(result, wrapSingleLine(prefix+inputLine, width)...)
		tracker.processText(inputLine)
	}
	if len(result) == 0 {
		return []string{""}
	}
	return result
}

func wrapSingleLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	if VisibleWidth(line) <= width {
		return []string{line}
	}
	var wrapped []string
	tracker := &ansiTracker{}
	tokens := splitIntoTokensWithANSI(line)

	currentLine := ""
	currentWidth := 0
	for _, token := range tokens {
		tokenWidth := VisibleWidth(token)
		isWhitespace := strings.TrimSpace(StripTerminalSequences(token)) == ""

		if tokenWidth > width && !isWhitespace {
			if currentLine != "" {
				wrapped = append(wrapped, currentLine+tracker.lineEndReset())
			}
			broken := breakLongWord(token, width, tracker)
			wrapped = append(wrapped, broken[:len(broken)-1]...)
			currentLine = broken[len(broken)-1]
			currentWidth = VisibleWidth(currentLine)
			continue
		}

		if currentWidth+tokenWidth > width && currentWidth > 0 {
			lineToWrap := strings.TrimRight(currentLine, " ")
			wrapped = append(wrapped, lineToWrap+tracker.lineEndReset())
			if isWhitespace {
				currentLine = tracker.activeCodes()
				currentWidth = 0
			} else {
				currentLine = tracker.activeCodes() + token
				currentWidth = tokenWidth
			}
		} else {
			currentLine += token
			currentWidth += tokenWidth
		}
		tracker.processText(token)
	}
	if currentLine != "" {
		wrapped = append(wrapped, currentLine)
	}
	if len(wrapped) == 0 {
		return []string{""}
	}
	for i := range wrapped {
		wrapped[i] = strings.TrimRight(wrapped[i], " ")
	}
	return wrapped
}

// splitIntoTokensWithANSI splits into word/space runs; CJK graphemes become
// standalone tokens so lines can break between them. ANSI codes attach to the
// next visible content.
func splitIntoTokensWithANSI(text string) []string {
	var tokens []string
	current := ""
	pendingAnsi := ""
	currentKind := 0 // 0 none, 1 space, 2 word

	flush := func() {
		if current != "" {
			tokens = append(tokens, current)
			current = ""
			currentKind = 0
		}
	}

	i := 0
	for i < len(text) {
		if ansi := ExtractAnsiCode(text, i); ansi != nil {
			pendingAnsi += ansi.Code
			i += ansi.Length
			continue
		}
		end := i
		for end < len(text) && ExtractAnsiCode(text, end) == nil {
			end++
		}
		g := uniseg.NewGraphemes(text[i:end])
		for g.Next() {
			seg := g.Str()
			if seg != " " && isCJKBreak(seg) {
				flush()
				tokens = append(tokens, pendingAnsi+seg)
				pendingAnsi = ""
				continue
			}
			kind := 2
			if seg == " " {
				kind = 1
			}
			if current != "" && currentKind != kind {
				flush()
			}
			if pendingAnsi != "" {
				current += pendingAnsi
				pendingAnsi = ""
			}
			currentKind = kind
			current += seg
		}
		i = end
	}
	if pendingAnsi != "" {
		if current != "" {
			current += pendingAnsi
		} else if len(tokens) > 0 {
			tokens[len(tokens)-1] += pendingAnsi
		} else {
			current = pendingAnsi
		}
	}
	if current != "" {
		tokens = append(tokens, current)
	}
	return tokens
}

func breakLongWord(word string, width int, tracker *ansiTracker) []string {
	var lines []string
	currentLine := tracker.activeCodes()
	currentWidth := 0

	i := 0
	for i < len(word) {
		if ansi := ExtractAnsiCode(word, i); ansi != nil {
			currentLine += ansi.Code
			tracker.process(ansi.Code)
			i += ansi.Length
			continue
		}
		end := i
		for end < len(word) && ExtractAnsiCode(word, end) == nil {
			end++
		}
		g := uniseg.NewGraphemes(word[i:end])
		for g.Next() {
			seg := g.Str()
			w := graphemeWidth(seg)
			if currentWidth+w > width {
				lines = append(lines, currentLine+tracker.lineEndReset())
				currentLine = tracker.activeCodes()
				currentWidth = 0
			}
			currentLine += seg
			currentWidth += w
		}
		i = end
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// ApplyBackgroundToLine pads line to width with spaces and wraps it in bgFn.
func ApplyBackgroundToLine(line string, width int, bgFn func(string) string) string {
	padding := width - VisibleWidth(line)
	if padding < 0 {
		padding = 0
	}
	return bgFn(line + strings.Repeat(" ", padding))
}

// TruncateToWidth truncates to maxWidth visible columns appending ellipsis when
// content is cut. ANSI codes are preserved; an SGR reset and OSC 8 close are
// emitted before the ellipsis so styling does not bleed into it.
func TruncateToWidth(text string, maxWidth int, ellipsis string) string {
	return truncateToWidthOpt(text, maxWidth, ellipsis, false)
}

// TruncateToWidthPad is TruncateToWidth with space padding to exactly maxWidth.
func TruncateToWidthPad(text string, maxWidth int, ellipsis string) string {
	return truncateToWidthOpt(text, maxWidth, ellipsis, true)
}

func truncateToWidthOpt(text string, maxWidth int, ellipsis string, pad bool) string {
	if maxWidth <= 0 {
		return ""
	}
	if text == "" {
		if pad {
			return strings.Repeat(" ", maxWidth)
		}
		return ""
	}
	ellipsisWidth := VisibleWidth(ellipsis)
	textWidth := VisibleWidth(text)
	if textWidth <= maxWidth {
		if pad {
			return text + strings.Repeat(" ", maxWidth-textWidth)
		}
		return text
	}
	if ellipsisWidth >= maxWidth {
		clipped := clipToWidth(ellipsis, maxWidth)
		if clipped == "" {
			if pad {
				return strings.Repeat(" ", maxWidth)
			}
			return ""
		}
		return finalizeTruncated("", 0, clipped, VisibleWidth(clipped), maxWidth, pad)
	}
	targetWidth := maxWidth - ellipsisWidth
	prefix, prefixWidth := clipWithWidth(text, targetWidth)
	return finalizeTruncated(prefix, prefixWidth, ellipsis, ellipsisWidth, maxWidth, pad)
}

func finalizeTruncated(prefix string, prefixWidth int, ellipsis string, ellipsisWidth, maxWidth int, pad bool) string {
	const reset = "\x1b[0m"
	linkClose := activeOsc8Close(prefix)
	visible := prefixWidth + ellipsisWidth
	var result string
	if ellipsis != "" {
		result = prefix + linkClose + reset + ellipsis + reset
	} else {
		result = prefix + linkClose + reset
	}
	if pad && maxWidth > visible {
		result += strings.Repeat(" ", maxWidth-visible)
	}
	return result
}

func activeOsc8Close(prefix string) string {
	if !strings.Contains(prefix, "\x1b]8;") {
		return ""
	}
	term := ""
	open := false
	i := 0
	for i < len(prefix) {
		if ansi := ExtractAnsiCode(prefix, i); ansi != nil {
			if params, url, t, ok := parseOsc8(ansi.Code); ok {
				_ = params
				open = url != ""
				term = t
			}
			i += ansi.Length
			continue
		}
		i++
	}
	if open {
		return "\x1b]8;;" + term
	}
	return ""
}

// clipToWidth keeps the longest prefix whose visible width fits maxWidth,
// preserving ANSI codes that precede kept content.
func clipToWidth(text string, maxWidth int) string {
	s, _ := clipWithWidth(text, maxWidth)
	return s
}

func clipWithWidth(text string, maxWidth int) (string, int) {
	if maxWidth <= 0 || text == "" {
		return "", 0
	}
	var b strings.Builder
	width := 0
	pendingAnsi := ""
	i := 0
	for i < len(text) {
		if ansi := ExtractAnsiCode(text, i); ansi != nil {
			pendingAnsi += ansi.Code
			i += ansi.Length
			continue
		}
		end := i
		for end < len(text) && ExtractAnsiCode(text, end) == nil {
			end++
		}
		g := uniseg.NewGraphemes(text[i:end])
		for g.Next() {
			seg := g.Str()
			w := graphemeWidth(seg)
			if width+w > maxWidth {
				return b.String(), width
			}
			if pendingAnsi != "" {
				b.WriteString(pendingAnsi)
				pendingAnsi = ""
			}
			b.WriteString(seg)
			width += w
		}
		i = end
	}
	return b.String(), width
}

// SliceByColumn extracts [startCol, startCol+length) visible columns keeping
// ANSI codes; wide characters straddling the start boundary are skipped.
func SliceByColumn(line string, startCol, length int) string {
	if length <= 0 {
		return ""
	}
	endCol := startCol + length
	var b strings.Builder
	currentCol := 0
	pendingAnsi := ""
	i := 0
	for i < len(line) {
		if ansi := ExtractAnsiCode(line, i); ansi != nil {
			if currentCol >= startCol && currentCol < endCol {
				b.WriteString(ansi.Code)
			} else if currentCol < startCol {
				pendingAnsi += ansi.Code
			}
			i += ansi.Length
			continue
		}
		end := i
		for end < len(line) && ExtractAnsiCode(line, end) == nil {
			end++
		}
		g := uniseg.NewGraphemes(line[i:end])
		for g.Next() {
			seg := g.Str()
			w := graphemeWidth(seg)
			if currentCol >= startCol && currentCol < endCol {
				if pendingAnsi != "" {
					b.WriteString(pendingAnsi)
					pendingAnsi = ""
				}
				b.WriteString(seg)
			}
			currentCol += w
			if currentCol >= endCol {
				return b.String()
			}
		}
		i = end
	}
	return b.String()
}

// TrimLastGrapheme removes the final grapheme cluster from s.
func TrimLastGrapheme(s string) string {
	if s == "" {
		return s
	}
	last := 0
	g := uniseg.NewGraphemes(s)
	offset := 0
	for g.Next() {
		last = offset
		offset += len(g.Str())
	}
	return s[:last]
}
