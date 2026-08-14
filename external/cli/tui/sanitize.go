//go:build cli

package tui

import "strings"

// SanitizeText removes terminal control bytes from untrusted text before it
// enters any component: ESC (so no CSI/OSC/DCS/APC can form), every C0
// control except newline and tab, DEL, and C1 controls (0x80-0x9f appear as
// UTF-8 runes). Model output, tool results, file names, titles, skill and
// MCP names all pass through here; the only escape sequences in rendered
// lines are the ones the renderer itself generates.
func SanitizeText(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 && b != '\n' && b != '\t' {
			clean = false
			break
		}
		if b == 0x7f {
			clean = false
			break
		}
	}
	if clean && !hasC1(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			// dropped
		case r >= 0x80 && r <= 0x9f:
			// C1 controls dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hasC1(s string) bool {
	for _, r := range s {
		if r >= 0x80 && r <= 0x9f {
			return true
		}
	}
	return false
}
