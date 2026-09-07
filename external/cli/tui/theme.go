//go:build cli

package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// Theme carries the color roles used by the console surface. Role names mirror
// pi's theme schema; values are hex "#rrggbb" strings resolved from the coddy
// palette (see NewDarkTheme / NewLightTheme in package cli).
type Theme struct {
	// Colors maps a role name to a hex color like "#9333ea".
	Colors map[string]string
	// TrueColor selects 24-bit SGR output; when false colors quantize to the
	// xterm 256 palette.
	TrueColor bool
}

// Fg wraps text in the foreground color of role, resetting foreground only.
func (t *Theme) Fg(role, text string) string {
	hex, ok := t.Colors[role]
	if !ok || hex == "" {
		return text
	}
	return t.fgAnsi(hex) + text + "\x1b[39m"
}

// Bg wraps text in the background color of role, resetting background only.
func (t *Theme) Bg(role, text string) string {
	hex, ok := t.Colors[role]
	if !ok || hex == "" {
		return text
	}
	return t.bgAnsi(hex) + text + "\x1b[49m"
}

// BgFn returns a background function suitable for ApplyBackgroundToLine.
func (t *Theme) BgFn(role string) func(string) string {
	return func(s string) string { return t.Bg(role, s) }
}

// FgFn returns a foreground function for the role.
func (t *Theme) FgFn(role string) func(string) string {
	return func(s string) string { return t.Fg(role, s) }
}

// Bold wraps text in bold.
func (t *Theme) Bold(text string) string { return "\x1b[1m" + text + "\x1b[22m" }

// Italic wraps text in italic.
func (t *Theme) Italic(text string) string { return "\x1b[3m" + text + "\x1b[23m" }

// Underline wraps text in underline.
func (t *Theme) Underline(text string) string { return "\x1b[4m" + text + "\x1b[24m" }

// Inverse wraps text in reverse video.
func (t *Theme) Inverse(text string) string { return "\x1b[7m" + text + "\x1b[27m" }

// Strikethrough wraps text in strikethrough.
func (t *Theme) Strikethrough(text string) string { return "\x1b[9m" + text + "\x1b[29m" }

func (t *Theme) fgAnsi(hex string) string {
	r, g, b, ok := parseHexColor(hex)
	if !ok {
		return ""
	}
	if t.TrueColor {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
	}
	return fmt.Sprintf("\x1b[38;5;%dm", rgbTo256(r, g, b))
}

func (t *Theme) bgAnsi(hex string) string {
	r, g, b, ok := parseHexColor(hex)
	if !ok {
		return ""
	}
	if t.TrueColor {
		return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
	}
	return fmt.Sprintf("\x1b[48;5;%dm", rgbTo256(r, g, b))
}

func parseHexColor(hex string) (r, g, b int, ok bool) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff), true
}

// cube256 holds the xterm color-cube component values.
var cube256 = [6]int{0, 95, 135, 175, 215, 255}

// rgbTo256 quantizes an RGB color to the closest xterm 256 palette index,
// considering both the 6x6x6 cube and the 24-step gray ramp (pi rgbTo256).
func rgbTo256(r, g, b int) int {
	ri, rv := nearestCube(r)
	gi, gv := nearestCube(g)
	bi, bv := nearestCube(b)
	cubeIdx := 16 + 36*ri + 6*gi + bi
	cubeDist := sq(r-rv) + sq(g-gv) + sq(b-bv)

	gray := (r + g + b) / 3
	gi24 := (gray - 8) / 10
	if gi24 < 0 {
		gi24 = 0
	}
	if gi24 > 23 {
		gi24 = 23
	}
	gv24 := 8 + gi24*10
	grayIdx := 232 + gi24
	grayDist := sq(r-gv24) + sq(g-gv24) + sq(b-gv24)

	if grayDist < cubeDist {
		return grayIdx
	}
	return cubeIdx
}

func nearestCube(v int) (idx, value int) {
	bestIdx, bestDist := 0, 1<<30
	for i, cv := range cube256 {
		d := sq(v - cv)
		if d < bestDist {
			bestIdx, bestDist = i, d
		}
	}
	return bestIdx, cube256[bestIdx]
}

func sq(v int) int { return v * v }
