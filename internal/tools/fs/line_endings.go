package fs

import "strings"

const defaultLineEnding = "\n"

// detectLineEnding returns the first line-ending sequence used by text.
func detectLineEnding(text string) string {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			return "\n"
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				return "\r\n"
			}
			return "\r"
		}
	}
	return defaultLineEnding
}

// normalizeLineEndings converts CRLF and lone CR sequences to LF.
func normalizeLineEndings(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

// convertToLineEnding normalizes text and converts it to the requested style.
func convertToLineEnding(text, ending string) string {
	normalized := normalizeLineEndings(text)
	if ending == defaultLineEnding {
		return normalized
	}
	return strings.ReplaceAll(normalized, defaultLineEnding, ending)
}

func hasFinalLineEnding(text string) bool {
	return strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r")
}

// restoreLineEndings applies the source file's line-ending and final-newline style.
func restoreLineEndings(text, source string) string {
	normalized := normalizeLineEndings(text)
	if hasFinalLineEnding(source) {
		if normalized != "" {
			normalized += defaultLineEnding
		}
	} else {
		normalized = strings.TrimSuffix(normalized, defaultLineEnding)
	}
	return convertToLineEnding(normalized, detectLineEnding(source))
}
