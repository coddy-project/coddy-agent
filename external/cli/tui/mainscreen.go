//go:build cli

package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// CursorMarker is the zero-width APC sequence a Focusable component embeds
// right before its fake cursor so the renderer can position the hardware
// cursor (IME support). Mirrors pi-tui CURSOR_MARKER.
const CursorMarker = "\x1b_pi:c\x07"

// segmentReset terminates every rendered line: full SGR reset + OSC 8 close.
const segmentReset = "\x1b[0m\x1b]8;;\x07"

// minRenderInterval throttles animation-driven renders (pi uses 16 ms).
const minRenderInterval = 16 * time.Millisecond

// Component is one renderable block. Render returns one string per line; the
// visible width of every line must not exceed width. Components that consume
// keyboard input additionally implement InputHandler.
type Component interface {
	Render(width int) []string
	Invalidate()
}

// InputHandler receives raw terminal input when the component has focus.
type InputHandler interface {
	HandleInput(data []byte)
}

// Focusable is implemented by components that render a fake cursor and emit
// CursorMarker when focused.
type Focusable interface {
	SetFocused(focused bool)
}

// Container groups child components vertically.
type Container struct {
	children []Component
}

// AddChild appends a component.
func (c *Container) AddChild(child Component) { c.children = append(c.children, child) }

// RemoveChild removes a component if present.
func (c *Container) RemoveChild(child Component) {
	for i, ch := range c.children {
		if ch == child {
			c.children = append(c.children[:i], c.children[i+1:]...)
			return
		}
	}
}

// Clear removes all children.
func (c *Container) Clear() { c.children = nil }

// Children returns the current child list.
func (c *Container) Children() []Component { return c.children }

// Render concatenates all child lines.
func (c *Container) Render(width int) []string {
	var lines []string
	for _, ch := range c.children {
		lines = append(lines, ch.Render(width)...)
	}
	return lines
}

// Invalidate clears every child's cached render state.
func (c *Container) Invalidate() {
	for _, ch := range c.children {
		ch.Invalidate()
	}
}

// Terminal abstracts the tty for the renderer; the real implementation lives
// in terminal.go, tests use a recording fake.
type Terminal interface {
	Write(s string)
	Columns() int
	Rows() int
	HideCursor()
	ShowCursor()
}

// MainScreen renders the component tree inline into the terminal main buffer,
// diffing against the previous frame (port of pi TuiMainScreen).
type MainScreen struct {
	mu sync.Mutex

	Root *Container

	terminal Terminal
	focused  Component

	previousLines       []string
	previousWidth       int
	previousHeight      int
	cursorRow           int
	hardwareCursorRow   int
	maxLinesRendered    int
	previousViewportTop int

	clearOnShrink      bool
	showHardwareCursor bool
	stopped            bool

	lastRender time.Time
	renderCh   chan struct{}
}

// NewMainScreen creates a renderer over t.
func NewMainScreen(t Terminal) *MainScreen {
	return &MainScreen{
		Root:          &Container{},
		terminal:      t,
		clearOnShrink: true,
		renderCh:      make(chan struct{}, 1),
	}
}

// SetFocus directs subsequent input to c and updates Focusable state.
func (m *MainScreen) SetFocus(c Component) {
	m.mu.Lock()
	prev := m.focused
	m.focused = c
	m.mu.Unlock()
	if f, ok := prev.(Focusable); ok && prev != nil {
		f.SetFocused(false)
	}
	if f, ok := c.(Focusable); ok && c != nil {
		f.SetFocused(true)
	}
}

// Focused returns the component currently receiving input.
func (m *MainScreen) Focused() Component {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.focused
}

// HandleInput dispatches raw input to the focused component and re-renders
// immediately (keyboard latency must not wait for the throttle).
func (m *MainScreen) HandleInput(data []byte) {
	m.mu.Lock()
	focused := m.focused
	m.mu.Unlock()
	if h, ok := focused.(InputHandler); ok && focused != nil {
		h.HandleInput(data)
	}
	m.RenderNow()
}

// RequestRender schedules a throttled render on the UI goroutine channel.
// Safe to call from any goroutine.
func (m *MainScreen) RequestRender() {
	select {
	case m.renderCh <- struct{}{}:
	default:
	}
}

// RenderSignal exposes the channel the UI loop selects on; receiving from it
// must be followed by RenderThrottled.
func (m *MainScreen) RenderSignal() <-chan struct{} { return m.renderCh }

// RenderThrottled renders now unless the previous render was under the
// 16 ms interval, in which case it sleeps the remainder first.
func (m *MainScreen) RenderThrottled() {
	m.mu.Lock()
	since := time.Since(m.lastRender)
	m.mu.Unlock()
	if since < minRenderInterval {
		time.Sleep(minRenderInterval - since)
	}
	m.RenderNow()
}

// SetClearOnShrink toggles full clears when content shrinks.
func (m *MainScreen) SetClearOnShrink(v bool) { m.mu.Lock(); m.clearOnShrink = v; m.mu.Unlock() }

// SetShowHardwareCursor toggles hardware cursor visibility at the marker.
func (m *MainScreen) SetShowHardwareCursor(v bool) {
	m.mu.Lock()
	m.showHardwareCursor = v
	m.mu.Unlock()
}

// Stop marks the screen stopped; subsequent renders are no-ops. The caller
// moves the terminal cursor below the content first via FinishInline.
func (m *MainScreen) Stop() { m.mu.Lock(); m.stopped = true; m.mu.Unlock() }

// FinishInline moves the hardware cursor to the last rendered row and emits a
// newline so the shell prompt continues below the TUI content.
func (m *MainScreen) FinishInline() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.previousLines) == 0 {
		return
	}
	m.terminal.Write(" ")
	target := len(m.previousLines)
	diff := target - m.hardwareCursorRow
	if diff > 0 {
		m.terminal.Write(fmt.Sprintf("\x1b[%dB", diff))
	} else if diff < 0 {
		m.terminal.Write(fmt.Sprintf("\x1b[%dA", -diff))
	}
	m.terminal.Write("\r\n")
}

// Invalidate clears cached renders across the tree (theme changes).
func (m *MainScreen) Invalidate() { m.Root.Invalidate() }

type cursorPos struct{ row, col int }

// RenderNow renders synchronously.
func (m *MainScreen) RenderNow() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	m.lastRender = time.Now()
	m.doRender()
}

func (m *MainScreen) doRender() {
	width := m.terminal.Columns()
	height := m.terminal.Rows()
	widthChanged := m.previousWidth != 0 && m.previousWidth != width
	heightChanged := m.previousHeight != 0 && m.previousHeight != height

	previousBufferLength := height
	if m.previousHeight > 0 {
		previousBufferLength = m.previousViewportTop + m.previousHeight
	}
	prevViewportTop := m.previousViewportTop
	if heightChanged {
		prevViewportTop = max(0, previousBufferLength-height)
	}
	viewportTop := prevViewportTop
	hardwareCursorRow := m.hardwareCursorRow

	computeLineDiff := func(targetRow int) int {
		currentScreenRow := hardwareCursorRow - prevViewportTop
		targetScreenRow := targetRow - viewportTop
		return targetScreenRow - currentScreenRow
	}

	newLines := m.Root.Render(width)
	cursor := extractCursorPosition(newLines, height)
	applyLineResets(newLines)

	fullRender := func(clear bool) {
		var b strings.Builder
		b.WriteString("\x1b[?2026h")
		if clear {
			b.WriteString("\x1b[2J\x1b[H\x1b[3J")
		}
		for i, line := range newLines {
			if i > 0 {
				b.WriteString("\r\n")
			}
			if VisibleWidth(line) > width {
				// A component failed to truncate; clip so the physical wrap
				// cannot desync the row bookkeeping.
				line = clipToWidth(line, width) + segmentReset
			}
			b.WriteString(line)
		}
		b.WriteString("\x1b[?2026l")
		m.terminal.Write(b.String())
		m.cursorRow = max(0, len(newLines)-1)
		m.hardwareCursorRow = m.cursorRow
		if clear {
			m.maxLinesRendered = len(newLines)
		} else {
			m.maxLinesRendered = max(m.maxLinesRendered, len(newLines))
		}
		bufferLength := max(height, len(newLines))
		m.previousViewportTop = max(0, bufferLength-height)
		m.positionHardwareCursor(cursor, len(newLines))
		m.previousLines = newLines
		m.previousWidth = width
		m.previousHeight = height
	}

	if len(m.previousLines) == 0 && !widthChanged && !heightChanged {
		fullRender(false)
		return
	}
	if widthChanged {
		fullRender(true)
		return
	}
	if heightChanged {
		fullRender(true)
		return
	}
	if m.clearOnShrink && len(newLines) < m.maxLinesRendered {
		fullRender(true)
		return
	}

	firstChanged, lastChanged := -1, -1
	maxLines := max(len(newLines), len(m.previousLines))
	for i := 0; i < maxLines; i++ {
		oldLine, newLine := "", ""
		if i < len(m.previousLines) {
			oldLine = m.previousLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}
	appendedLines := len(newLines) > len(m.previousLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(m.previousLines)
		}
		lastChanged = len(newLines) - 1
	}
	appendStart := appendedLines && firstChanged == len(m.previousLines) && firstChanged > 0

	if firstChanged == -1 {
		m.positionHardwareCursor(cursor, len(newLines))
		m.previousViewportTop = prevViewportTop
		m.previousHeight = height
		return
	}

	// All changes are in deleted trailing lines: clear them in place.
	if firstChanged >= len(newLines) {
		if len(m.previousLines) > len(newLines) {
			var b strings.Builder
			b.WriteString("\x1b[?2026h")
			targetRow := max(0, len(newLines)-1)
			if targetRow < prevViewportTop {
				fullRender(true)
				return
			}
			extraLines := len(m.previousLines) - len(newLines)
			if extraLines > height {
				fullRender(true)
				return
			}
			diff := computeLineDiff(targetRow)
			if diff > 0 {
				fmt.Fprintf(&b, "\x1b[%dB", diff)
			} else if diff < 0 {
				fmt.Fprintf(&b, "\x1b[%dA", -diff)
			}
			b.WriteString("\r")
			clearStartOffset := 1
			if len(newLines) == 0 {
				clearStartOffset = 0
			}
			if extraLines > 0 && clearStartOffset > 0 {
				fmt.Fprintf(&b, "\x1b[%dB", clearStartOffset)
			}
			for i := 0; i < extraLines; i++ {
				b.WriteString("\r\x1b[2K")
				if i < extraLines-1 {
					b.WriteString("\x1b[1B")
				}
			}
			if moveBack := max(0, extraLines-1+clearStartOffset); moveBack > 0 {
				fmt.Fprintf(&b, "\x1b[%dA", moveBack)
			}
			b.WriteString("\x1b[?2026l")
			m.terminal.Write(b.String())
			m.cursorRow = targetRow
			m.hardwareCursorRow = targetRow
		}
		m.positionHardwareCursor(cursor, len(newLines))
		m.previousLines = newLines
		m.previousWidth = width
		m.previousHeight = height
		m.previousViewportTop = prevViewportTop
		return
	}

	// Changes above the previously visible viewport need a full redraw.
	if firstChanged < prevViewportTop {
		fullRender(true)
		return
	}

	var b strings.Builder
	b.WriteString("\x1b[?2026h")
	prevViewportBottom := prevViewportTop + height - 1
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}
	if moveTargetRow > prevViewportBottom {
		currentScreenRow := max(0, min(height-1, hardwareCursorRow-prevViewportTop))
		if moveToBottom := height - 1 - currentScreenRow; moveToBottom > 0 {
			fmt.Fprintf(&b, "\x1b[%dB", moveToBottom)
		}
		scroll := moveTargetRow - prevViewportBottom
		b.WriteString(strings.Repeat("\r\n", scroll))
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	diff := computeLineDiff(moveTargetRow)
	if diff > 0 {
		fmt.Fprintf(&b, "\x1b[%dB", diff)
	} else if diff < 0 {
		fmt.Fprintf(&b, "\x1b[%dA", -diff)
	}
	if appendStart {
		b.WriteString("\r\n")
	} else {
		b.WriteString("\r")
	}

	renderEnd := min(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			b.WriteString("\r\n")
		}
		b.WriteString("\x1b[2K")
		line := newLines[i]
		if VisibleWidth(line) > width {
			// A component failed to truncate. Render a hard-clipped line rather
			// than corrupting the layout; the bug surfaces in tests.
			line = clipToWidth(line, width) + segmentReset
		}
		b.WriteString(line)
	}

	finalCursorRow := renderEnd
	if len(m.previousLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			fmt.Fprintf(&b, "\x1b[%dB", len(newLines)-1-renderEnd)
			finalCursorRow = len(newLines) - 1
		}
		extraLines := len(m.previousLines) - len(newLines)
		for i := len(newLines); i < len(m.previousLines); i++ {
			b.WriteString("\r\n\x1b[2K")
		}
		fmt.Fprintf(&b, "\x1b[%dA", extraLines)
	}
	b.WriteString("\x1b[?2026l")
	m.terminal.Write(b.String())

	m.cursorRow = max(0, len(newLines)-1)
	m.hardwareCursorRow = finalCursorRow
	m.maxLinesRendered = max(m.maxLinesRendered, len(newLines))
	m.previousViewportTop = max(prevViewportTop, finalCursorRow-height+1)
	m.positionHardwareCursor(cursor, len(newLines))
	m.previousLines = newLines
	m.previousWidth = width
	m.previousHeight = height
}

func (m *MainScreen) positionHardwareCursor(pos *cursorPos, totalLines int) {
	if pos == nil || totalLines <= 0 {
		m.terminal.HideCursor()
		return
	}
	targetRow := max(0, min(pos.row, totalLines-1))
	targetCol := max(0, pos.col)
	rowDelta := targetRow - m.hardwareCursorRow
	var b strings.Builder
	if rowDelta > 0 {
		fmt.Fprintf(&b, "\x1b[%dB", rowDelta)
	} else if rowDelta < 0 {
		fmt.Fprintf(&b, "\x1b[%dA", -rowDelta)
	}
	fmt.Fprintf(&b, "\x1b[%dG", targetCol+1)
	m.terminal.Write(b.String())
	m.hardwareCursorRow = targetRow
	if m.showHardwareCursor {
		m.terminal.ShowCursor()
	} else {
		m.terminal.HideCursor()
	}
}

// extractCursorPosition finds CursorMarker in the bottom height lines
// (bottom-up), strips it, and returns its row/column.
func extractCursorPosition(lines []string, height int) *cursorPos {
	start := max(0, len(lines)-height)
	for i := len(lines) - 1; i >= start; i-- {
		idx := strings.Index(lines[i], CursorMarker)
		if idx < 0 {
			continue
		}
		before := lines[i][:idx]
		col := VisibleWidth(before)
		lines[i] = before + lines[i][idx+len(CursorMarker):]
		return &cursorPos{row: i, col: col}
	}
	return nil
}

// applyLineResets appends the SGR + OSC 8 reset to every line so styling can
// never bleed across lines.
func applyLineResets(lines []string) {
	for i, line := range lines {
		lines[i] = normalizeTerminalOutput(line) + segmentReset
	}
}

// normalizeTerminalOutput expands visible tabs to the fixed 3-space layout
// width, leaving tabs inside escape sequences untouched.
func normalizeTerminalOutput(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		if ansi := ExtractAnsiCode(s, i); ansi != nil {
			b.WriteString(ansi.Code)
			i += ansi.Length
			continue
		}
		if s[i] == '\t' {
			b.WriteString("   ")
		} else {
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String()
}

// Snapshot returns a copy of the last rendered frame lines (test support).
func (m *MainScreen) Snapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.previousLines))
	copy(out, m.previousLines)
	return out
}
