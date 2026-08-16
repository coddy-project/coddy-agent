//go:build cli

package tui

import "strings"

// Text renders multi-line word-wrapped text with horizontal and vertical
// padding and an optional background (port of pi-tui Text).
type Text struct {
	text     string
	paddingX int
	paddingY int
	bgFn     func(string) string

	cachedText  string
	cachedWidth int
	cachedLines []string
	cacheValid  bool
}

// NewText creates a Text with pi defaults (paddingX=1, paddingY=1).
func NewText(text string, paddingX, paddingY int, bgFn func(string) string) *Text {
	return &Text{text: text, paddingX: paddingX, paddingY: paddingY, bgFn: bgFn}
}

// SetText replaces the content.
func (t *Text) SetText(text string) {
	t.text = text
	t.cacheValid = false
}

// SetBgFn replaces the background function.
func (t *Text) SetBgFn(bgFn func(string) string) {
	t.bgFn = bgFn
	t.cacheValid = false
}

// Invalidate clears the render cache.
func (t *Text) Invalidate() { t.cacheValid = false }

// Render wraps and pads the text; whitespace-only text renders nothing.
func (t *Text) Render(width int) []string {
	if t.cacheValid && t.cachedText == t.text && t.cachedWidth == width {
		return t.cachedLines
	}
	store := func(lines []string) []string {
		t.cachedText = t.text
		t.cachedWidth = width
		t.cachedLines = lines
		t.cacheValid = true
		return lines
	}
	if strings.TrimSpace(t.text) == "" {
		return store(nil)
	}
	normalized := strings.ReplaceAll(t.text, "\t", "   ")
	contentWidth := max(1, width-t.paddingX*2)
	wrapped := WrapTextWithANSI(normalized, contentWidth)

	margin := strings.Repeat(" ", t.paddingX)
	var content []string
	for _, line := range wrapped {
		withMargins := margin + line + margin
		if t.bgFn != nil {
			content = append(content, ApplyBackgroundToLine(withMargins, width, t.bgFn))
		} else {
			pad := max(0, width-VisibleWidth(withMargins))
			content = append(content, withMargins+strings.Repeat(" ", pad))
		}
	}
	empty := strings.Repeat(" ", width)
	if t.bgFn != nil {
		empty = ApplyBackgroundToLine(empty, width, t.bgFn)
	}
	var result []string
	for i := 0; i < t.paddingY; i++ {
		result = append(result, empty)
	}
	result = append(result, content...)
	for i := 0; i < t.paddingY; i++ {
		result = append(result, empty)
	}
	if len(result) == 0 {
		result = []string{""}
	}
	return store(result)
}

// TruncatedText renders a single line truncated with an ellipsis.
type TruncatedText struct {
	text     string
	paddingX int
	paddingY int
}

// NewTruncatedText creates a TruncatedText (pi defaults: no padding).
func NewTruncatedText(text string, paddingX, paddingY int) *TruncatedText {
	return &TruncatedText{text: text, paddingX: paddingX, paddingY: paddingY}
}

// SetText replaces the content.
func (t *TruncatedText) SetText(text string) { t.text = text }

// Invalidate is a no-op (no cache).
func (t *TruncatedText) Invalidate() {}

// Render truncates the first line of text to the available width.
func (t *TruncatedText) Render(width int) []string {
	line := t.text
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	contentWidth := max(1, width-t.paddingX*2)
	margin := strings.Repeat(" ", t.paddingX)
	out := margin + TruncateToWidth(line, contentWidth, "...") + margin
	var result []string
	for i := 0; i < t.paddingY; i++ {
		result = append(result, "")
	}
	result = append(result, out)
	for i := 0; i < t.paddingY; i++ {
		result = append(result, "")
	}
	return result
}

// Spacer renders n empty lines.
type Spacer struct{ n int }

// NewSpacer creates a Spacer of n blank lines.
func NewSpacer(n int) *Spacer { return &Spacer{n: n} }

// Invalidate is a no-op.
func (s *Spacer) Invalidate() {}

// Render returns n empty strings.
func (s *Spacer) Render(int) []string {
	lines := make([]string, s.n)
	return lines
}

// Box applies padding and a background to child components.
type Box struct {
	Container
	paddingX int
	paddingY int
	bgFn     func(string) string
}

// NewBox creates a Box (pi defaults: paddingX=1, paddingY=1).
func NewBox(paddingX, paddingY int, bgFn func(string) string) *Box {
	return &Box{paddingX: paddingX, paddingY: paddingY, bgFn: bgFn}
}

// SetBgFn replaces the background function.
func (b *Box) SetBgFn(bgFn func(string) string) { b.bgFn = bgFn }

// Render pads children left by paddingX and wraps every row in the background.
func (b *Box) Render(width int) []string {
	if len(b.children) == 0 {
		return nil
	}
	contentWidth := max(1, width-b.paddingX*2)
	leftPad := strings.Repeat(" ", b.paddingX)
	var childLines []string
	for _, child := range b.children {
		for _, line := range child.Render(contentWidth) {
			childLines = append(childLines, leftPad+line)
		}
	}
	if len(childLines) == 0 {
		return nil
	}
	var result []string
	for i := 0; i < b.paddingY; i++ {
		result = append(result, b.applyBg("", width))
	}
	for _, line := range childLines {
		result = append(result, b.applyBg(line, width))
	}
	for i := 0; i < b.paddingY; i++ {
		result = append(result, b.applyBg("", width))
	}
	return result
}

func (b *Box) applyBg(line string, width int) string {
	pad := max(0, width-VisibleWidth(line))
	padded := line + strings.Repeat(" ", pad)
	if b.bgFn != nil {
		return ApplyBackgroundToLine(padded, width, b.bgFn)
	}
	return padded
}

// DynamicBorder renders one full-width horizontal rule through a color fn.
type DynamicBorder struct {
	colorFn func(string) string
}

// NewDynamicBorder creates a border line colored by colorFn.
func NewDynamicBorder(colorFn func(string) string) *DynamicBorder {
	return &DynamicBorder{colorFn: colorFn}
}

// Invalidate is a no-op.
func (d *DynamicBorder) Invalidate() {}

// Render emits a single `─` rule across the width.
func (d *DynamicBorder) Render(width int) []string {
	line := strings.Repeat("─", max(0, width))
	if d.colorFn != nil {
		line = d.colorFn(line)
	}
	return []string{line}
}
