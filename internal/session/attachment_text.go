package session

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// UserMessageText is what a text surface - the console, an editor over ACP -
// shows for the content of a user message: the display text
// (UserMessageDisplayText) with the files the message was sent with named in
// front of it, a picture as [image #N] and any other file as [file: name], so
// a picture attached in the web UI or a Telegram chat is still seen to be
// there. A prompt typed in the console names its pictures with those markers
// itself, and is shown as typed.
func UserMessageText(content string) string {
	text := UserMessageDisplayText(content)
	if imageMarkerRE.MatchString(text) {
		return text
	}
	marks := attachmentPlaceholders(content)
	if len(marks) == 0 {
		return text
	}
	if text == "" {
		return strings.Join(marks, " ")
	}
	return strings.Join(marks, " ") + " " + text
}

// imageMarkerRE matches the [image #N] marker a console prompt names a pasted
// picture with.
var imageMarkerRE = regexp.MustCompile(`\[image #\d+\]`)

// attachmentPlaceholders names the files the <coddy_session_assets> note of a
// message lists (filePathsNote in internal/agent: one "- <path>" line each,
// the original name in parentheses when the saved one differs).
func attachmentPlaceholders(content string) []string {
	const open, close = "<coddy_session_assets>", "</coddy_session_assets>"
	var out []string
	images := 0
	for {
		start := strings.Index(content, open)
		if start < 0 {
			return out
		}
		rest := content[start+len(open):]
		end := strings.Index(rest, close)
		if end < 0 {
			end = len(rest)
		}
		for _, line := range strings.Split(rest[:end], "\n") {
			entry, ok := strings.CutPrefix(strings.TrimSpace(line), "- ")
			if !ok || entry == "" {
				continue
			}
			name := filepath.Base(entry)
			if i := strings.LastIndex(entry, " ("); i > 0 && strings.HasSuffix(entry, ")") {
				name = entry[i+2 : len(entry)-1]
			}
			if isPictureName(name) {
				images++
				out = append(out, fmt.Sprintf("[image #%d]", images))
			} else {
				out = append(out, "[file: "+name+"]")
			}
		}
		if end == len(rest) {
			return out
		}
		content = rest[end+len(close):]
	}
}

func isPictureName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff", ".svg", ".heic", ".avif":
		return true
	}
	return false
}
