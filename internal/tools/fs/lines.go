package fs

import (
	"fmt"
	"strings"
)

func sliceLines(content string, start, end int) (string, error) {
	if content == "" {
		return "", fmt.Errorf("offset %d is beyond end of file (0 lines)", max(start, 1))
	}

	lines := strings.Split(content, "\n")
	hasTrailingNewline := lines[len(lines)-1] == ""
	if hasTrailingNewline {
		lines = lines[:len(lines)-1]
	}
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return "", fmt.Errorf("offset %d is beyond end of file (%d lines)", start, len(lines))
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	result := strings.Join(lines[start-1:end], "\n")
	if hasTrailingNewline && end == len(lines) {
		result += "\n"
	}
	return result, nil
}
