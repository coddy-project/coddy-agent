package fs

import (
	"fmt"
	"strconv"
	"strings"
)

type updateChunk struct {
	changeContext string
	oldLines      []string
	newLines      []string
	eof           bool
}

func isV4APatch(diff string) bool {
	if strings.Contains(diff, "*** Begin Patch") {
		return true
	}
	for _, line := range strings.Split(diff, "\n") {
		if isV4AHunkHeader(line) {
			return true
		}
	}
	return false
}

func isV4AHunkHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "@@") {
		return false
	}
	inner := strings.TrimSpace(strings.TrimPrefix(trimmed, "@@"))
	inner = strings.TrimSpace(strings.TrimSuffix(inner, "@@"))
	if inner == "" {
		return true
	}
	if strings.HasPrefix(inner, "-") {
		oldPart, _, _ := strings.Cut(inner, " ")
		oldPart = strings.TrimPrefix(oldPart, "-")
		startStr, _, _ := strings.Cut(oldPart, ",")
		if _, err := strconv.Atoi(startStr); err == nil {
			return false
		}
	}
	return true
}

func applyV4APatch(original, diff string) (string, error) {
	chunks, err := parseV4AChunks(diff)
	if err != nil {
		return "", err
	}
	if len(chunks) == 0 {
		return "", fmt.Errorf("patch contains no change hunks")
	}
	lines := splitFileLines(original)
	replacements, err := computeV4AReplacements(lines, chunks)
	if err != nil {
		return "", err
	}
	return strings.Join(applyV4AReplacements(lines, replacements), "\n"), nil
}

func parseV4AChunks(diff string) ([]updateChunk, error) {
	var chunks []updateChunk
	var cur *updateChunk

	flush := func() {
		if cur == nil {
			return
		}
		if len(cur.oldLines) > 0 || len(cur.newLines) > 0 {
			chunks = append(chunks, *cur)
		}
		cur = nil
	}

	for _, line := range strings.Split(diff, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "*** Begin Patch" || trimmed == "*** End Patch":
			continue
		case strings.HasPrefix(trimmed, "*** Add File:"),
			strings.HasPrefix(trimmed, "*** Delete File:"),
			strings.HasPrefix(trimmed, "*** Update File:"),
			strings.HasPrefix(trimmed, "*** Move to:"),
			strings.HasPrefix(trimmed, "*** Environment ID:"):
			continue
		case strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "+++"):
			continue
		case trimmed == "@@" || strings.HasPrefix(trimmed, "@@ "):
			flush()
			ctx := ""
			if trimmed != "@@" {
				ctx = strings.TrimSpace(strings.TrimPrefix(trimmed, "@@"))
			}
			cur = &updateChunk{changeContext: ctx}
			continue
		case trimmed == "*** End of File":
			if cur == nil {
				return nil, fmt.Errorf("patch: %q without a preceding hunk", trimmed)
			}
			cur.eof = true
			continue
		}

		if cur == nil {
			if len(line) == 0 {
				continue
			}
			if line[0] != ' ' && line[0] != '+' && line[0] != '-' {
				continue
			}
			cur = &updateChunk{}
		}

		switch {
		case len(line) == 0:
			cur.oldLines = append(cur.oldLines, "")
			cur.newLines = append(cur.newLines, "")
		case line[0] == ' ':
			text := line[1:]
			cur.oldLines = append(cur.oldLines, text)
			cur.newLines = append(cur.newLines, text)
		case line[0] == '+':
			cur.newLines = append(cur.newLines, line[1:])
		case line[0] == '-':
			cur.oldLines = append(cur.oldLines, line[1:])
		default:
			return nil, fmt.Errorf("patch hunk line %q: expected leading ' ', '+', or '-'", line)
		}
	}
	flush()
	return chunks, nil
}

func computeV4AReplacements(original []string, chunks []updateChunk) ([]replacement, error) {
	var replacements []replacement
	lineIndex := 0

	for _, chunk := range chunks {
		if chunk.changeContext != "" {
			idx, ok := seekSequence(original, []string{chunk.changeContext}, lineIndex, false)
			if !ok {
				return nil, fmt.Errorf("patch context not found: %q", chunk.changeContext)
			}
			lineIndex = idx + 1
		}

		if len(chunk.oldLines) == 0 {
			insertAt := len(original)
			if len(original) > 0 && original[len(original)-1] == "" {
				insertAt = len(original) - 1
			}
			replacements = append(replacements, replacement{insertAt, 0, append([]string(nil), chunk.newLines...)})
			continue
		}

		pattern := chunk.oldLines
		newLines := chunk.newLines
		start, ok := seekSequence(original, pattern, lineIndex, chunk.eof)
		if !ok && len(pattern) > 0 && pattern[len(pattern)-1] == "" {
			pattern = pattern[:len(pattern)-1]
			if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
				newLines = newLines[:len(newLines)-1]
			}
			start, ok = seekSequence(original, pattern, lineIndex, chunk.eof)
		}
		if !ok {
			return nil, fmt.Errorf("patch context not found in file:\n%s", strings.Join(chunk.oldLines, "\n"))
		}
		replacements = append(replacements, replacement{start, len(pattern), append([]string(nil), newLines...)})
		lineIndex = start + len(pattern)
	}

	return replacements, nil
}

type replacement struct {
	start    int
	oldLen   int
	newLines []string
}

func applyV4AReplacements(lines []string, replacements []replacement) []string {
	result := append([]string(nil), lines...)
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		for j := 0; j < r.oldLen; j++ {
			if r.start < len(result) {
				result = append(result[:r.start], result[r.start+1:]...)
			}
		}
		for offset, nl := range r.newLines {
			result = append(result[:r.start+offset], append([]string{nl}, result[r.start+offset:]...)...)
		}
	}
	return result
}

func seekSequence(lines, pattern []string, start int, eof bool) (int, bool) {
	if len(pattern) == 0 {
		return start, true
	}
	if len(pattern) > len(lines) {
		return 0, false
	}
	searchStart := start
	if eof && len(lines) >= len(pattern) {
		searchStart = len(lines) - len(pattern)
	}
	for i := searchStart; i <= len(lines)-len(pattern); i++ {
		if linesSliceEqual(lines[i:i+len(pattern)], pattern) {
			return i, true
		}
	}
	for i := searchStart; i <= len(lines)-len(pattern); i++ {
		if linesSliceEqualTrimEnd(lines[i:i+len(pattern)], pattern) {
			return i, true
		}
	}
	for i := searchStart; i <= len(lines)-len(pattern); i++ {
		if linesSliceEqualTrim(lines[i:i+len(pattern)], pattern) {
			return i, true
		}
	}
	return 0, false
}

func linesSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func linesSliceEqualTrimEnd(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimRight(a[i], " \t") != strings.TrimRight(b[i], " \t") {
			return false
		}
	}
	return true
}

func linesSliceEqualTrim(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

func splitFileLines(original string) []string {
	lines := strings.Split(original, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
