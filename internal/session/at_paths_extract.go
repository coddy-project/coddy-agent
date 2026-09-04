package session

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// markdownFenceBeforeIndex reports whether caretExclusive sits inside fenced code per draftSlash parity.
func markdownFenceBeforeIndex(text string, caretExclusive int) bool {
	if caretExclusive < 0 {
		caretExclusive = 0
	}
	if caretExclusive > len(text) {
		caretExclusive = len(text)
	}
	head := text[:caretExclusive]
	inFence := false
	for _, line := range strings.Split(head, "\n") {
		trimmedLead := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmedLead, "```") {
			inFence = !inFence
		}
	}
	return inFence
}

func blockQuoteLineStrict(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(t, ">")
}

func isFilePathRune(r rune) bool {
	switch r {
	case '.', '/', '_', '-', '\\':
		return true
	default:
		return unicode.IsLetter(r) || unicode.IsNumber(r)
	}
}

// continuationLooksLikeMorePath skips ASCII space inside paths like "readme here.md".
func continuationLooksLikeMorePath(afterSpaces string) bool {
	trim := strings.TrimLeft(afterSpaces, " \t")
	if trim == "" {
		return false
	}
	r0, sz := utf8.DecodeRuneInString(trim)
	if r0 == utf8.RuneError || sz == 0 {
		return false
	}
	if !unicode.IsLetter(r0) && !unicode.IsNumber(r0) && r0 != '_' {
		return false
	}
	// Consume same class as [\p{L}\p{N}_][\p{L}\p{N}_.\-]* (first segment after space).
	var end int
	for end = 0; end < len(trim); {
		r, sz2 := utf8.DecodeRuneInString(trim[end:])
		if r == utf8.RuneError {
			break
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' || r == '.' {
			end += sz2
			continue
		}
		break
	}
	word := trim[:end]
	return strings.ContainsAny(word, "/.")
}

// AtFileRef is one @path mention, optionally narrowed to a 1-based inclusive line range.
// StartLine and EndLine are zero when the mention carried no ":N-M" suffix.
type AtFileRef struct {
	Path      string
	StartLine int
	EndLine   int
}

// parseAtLineRangeSuffix reads a ":<start>-<end>" suffix at text[k:] and returns
// the range plus the index past it. The suffix counts only when both numbers are
// valid (1 <= start <= end) and the token ends there: the next rune must not be a
// letter, digit, or '-', so ":21-31x" stays prose.
// Mirrors external/ui draftAt.parseAtLineRangeSuffix.
func parseAtLineRangeSuffix(text string, k int) (start, end, next int, ok bool) {
	n := len(text)
	if k >= n || text[k] != ':' {
		return 0, 0, k, false
	}
	p := k + 1
	d0 := p
	for p < n && text[p] >= '0' && text[p] <= '9' {
		p++
	}
	if p == d0 || p >= n || text[p] != '-' {
		return 0, 0, k, false
	}
	first := text[d0:p]
	p++
	d1 := p
	for p < n && text[p] >= '0' && text[p] <= '9' {
		p++
	}
	if p == d1 {
		return 0, 0, k, false
	}
	second := text[d1:p]
	if p < n {
		r, _ := utf8.DecodeRuneInString(text[p:])
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '-' {
			return 0, 0, k, false
		}
	}
	// Bound the digits so a pathological run cannot overflow the accumulator.
	if len(first) > 9 || len(second) > 9 {
		return 0, 0, k, false
	}
	s := atoiDigits(first)
	e := atoiDigits(second)
	if s < 1 || e < s {
		return 0, 0, k, false
	}
	return s, e, p, true
}

// atoiDigits converts an all-digit string; callers bound its length first.
func atoiDigits(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		v = v*10 + int(s[i]-'0')
	}
	return v
}

// ExtractAtFilePathsFromText returns workspace-relative paths from plain @mentions,
// deduplicated by path and without line ranges. Callers that need the ":N-M" suffix
// use ExtractAtFileRefsFromText.
func ExtractAtFilePathsFromText(text string) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, ref := range ExtractAtFileRefsFromText(text) {
		key := filepath.ToSlash(ref.Path)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref.Path)
	}
	return out
}

// ExtractAtFileRefsFromText returns @path mentions with optional ":N-M" line
// ranges in document order, deduplicated by path plus range.
// Mirrors external/ui draftAt.extractAtFileAttachments (file tokens only).
func ExtractAtFileRefsFromText(text string) []AtFileRef {
	var out []AtFileRef
	seen := make(map[string]struct{})
	n := len(text)

	for i := 0; i < n; {
		j := strings.Index(text[i:], "@")
		if j < 0 {
			break
		}
		j += i

		fenceIdx := j + 1
		if fenceIdx > len(text) {
			fenceIdx = len(text)
		}
		if markdownFenceBeforeIndex(text, fenceIdx) {
			i = j + 1
			continue
		}

		lineStart := strings.LastIndex(text[:j], "\n") + 1
		lineEnd := strings.Index(text[j:], "\n")
		if lineEnd < 0 {
			lineEnd = len(text)
		} else {
			lineEnd += j
		}
		line := text[lineStart:lineEnd]
		if blockQuoteLineStrict(line) {
			i = j + 1
			continue
		}
		if j > 0 {
			prev, _ := utf8.DecodeLastRuneInString(text[:j])
			if !unicode.IsSpace(prev) && prev != utf8.RuneError {
				i = j + 1
				continue
			}
		}

		k := j + 1
		for k < n {
			r, size := utf8.DecodeRuneInString(text[k:])
			if r == utf8.RuneError || size == 0 {
				break
			}
			if r == '\r' || r == '\n' {
				break
			}
			if r == '@' {
				break
			}
			if isFilePathRune(r) {
				k += size
				continue
			}
			if unicode.IsSpace(r) {
				tail := text[k+size:]
				if continuationLooksLikeMorePath(tail) {
					k += size
					continue
				}
				break
			}
			break
		}

		raw := strings.TrimRight(text[j+1:k], " \t")
		i = k
		startLine, endLine := 0, 0
		// Only an unpadded token may carry a range: "@foo :1-5" had its trailing
		// space trimmed, and the suffix there belongs to the prose, not the path.
		if raw != "" && raw == text[j+1:k] {
			if s, e, next, ok := parseAtLineRangeSuffix(text, k); ok {
				startLine, endLine = s, e
				i = next
			}
		}
		if raw == "" || strings.Contains(raw, "..") {
			continue
		}
		if strings.HasSuffix(filepath.ToSlash(raw), "/") {
			continue
		}
		key := fmt.Sprintf("%s#L%d-%d", filepath.ToSlash(raw), startLine, endLine)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, AtFileRef{Path: raw, StartLine: startLine, EndLine: endLine})
	}
	return out
}
