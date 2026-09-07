//go:build cli

package tui

import (
	"strings"
	"unicode/utf8"
)

// SanitizeText removes terminal control bytes from untrusted text before it
// enters any component: ESC (so no CSI/OSC/DCS/APC can form), every C0
// control except newline and tab, DEL, C1 controls (both as UTF-8 runes and
// as raw invalid bytes), and invalid UTF-8 bytes. Model output, tool
// results, file names, titles, skill and MCP names all pass through here;
// the only escape sequences in rendered lines are the ones the renderer
// itself generates.
func SanitizeText(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 && b != '\n' && b != '\t' {
			clean = false
			break
		}
		if b == 0x7f || b >= 0x80 {
			// Non-ASCII needs the rune-level pass (C1 and invalid bytes).
			clean = b < 0x80
			if !clean {
				break
			}
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// Invalid byte (raw C1 or broken UTF-8): dropped.
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			// C0 controls and DEL: dropped.
		case r >= 0x80 && r <= 0x9f:
			// C1 controls: dropped.
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}
