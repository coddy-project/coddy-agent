package platform

import "strings"

// escape is the byte that opens every ANSI escape sequence.
const escape = 0x1b

// SanitizeOutput turns captured terminal output into something readable as a
// transcript. Escape sequences carry no meaning once the bytes leave a terminal,
// and a progress bar redrawn with carriage returns arrives as dozens of
// near-identical lines: a webpack build spends most of its output on redraws, and
// the model has to read past all of it to find the line that matters.
//
// Text that holds neither an escape byte nor a carriage return is returned
// untouched, so ordinary command output keeps round-tripping verbatim.
func SanitizeOutput(s string) string {
	if !strings.ContainsAny(s, "\x1b\r") {
		return s
	}
	s = StripANSI(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = CollapseCarriageReturns(s)
	return collapseBlankRuns(s)
}

// StripANSI removes ANSI escape sequences. Every byte it matches is ASCII and
// every UTF-8 continuation byte is >= 0x80, so multi-byte characters pass
// through untouched.
func StripANSI(s string) string {
	if !strings.ContainsRune(s, escape) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != escape {
			b.WriteByte(s[i])
			i++
			continue
		}
		i += escapeLength(s, i)
	}
	return b.String()
}

// escapeLength reports how many bytes the escape sequence starting at i spans.
// An unterminated sequence consumes the rest of what it could claim rather than
// leaking its bytes into the transcript.
func escapeLength(s string, i int) int {
	if i+1 >= len(s) {
		return 1 // a lone trailing ESC
	}
	switch s[i+1] {
	case '[': // CSI: parameter and intermediate bytes, then a final byte
		j := i + 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
			return j - i + 1
		}
		return j - i
	case ']': // OSC: terminated by BEL or by ESC backslash
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j - i + 1
			}
			if s[j] == escape && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2 - i
			}
		}
		return len(s) - i
	case '(', ')', '*', '+': // character set designation, three bytes
		if i+2 < len(s) {
			return 3
		}
		return 2
	default:
		return 2
	}
}

// CollapseCarriageReturns keeps only what a terminal would still be showing on
// each line: a progress bar that wrote "10%\r50%\r100%" is read as "100%".
func CollapseCarriageReturns(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if !strings.ContainsRune(line, '\r') {
			continue
		}
		segments := strings.Split(line, "\r")
		// A trailing carriage return parks the cursor without writing anything,
		// so the last segment that carries text is the one left on screen.
		last := len(segments) - 1
		for last > 0 && segments[last] == "" {
			last--
		}
		lines[i] = segments[last]
	}
	return strings.Join(lines, "\n")
}

// collapseBlankRuns leaves at most one blank line in a row. Stripping a redrawn
// progress bar leaves behind the blank lines it used to overwrite, and they
// would otherwise consume the tool output ceiling. It only ever runs on output
// that carried escape sequences or carriage returns.
func collapseBlankRuns(s string) string {
	if !strings.Contains(s, "\n\n\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blanks++
			if blanks > 1 {
				continue
			}
		} else {
			blanks = 0
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
