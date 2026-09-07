//go:build cli

package tui

import (
	"strings"
	"testing"
)

// fakeTerminal records writes for renderer assertions (stands in for pi's
// virtual terminal harness).
type fakeTerminal struct {
	cols, rows int
	writes     []string
	cursorOps  []string
}

func newFakeTerminal(cols, rows int) *fakeTerminal {
	return &fakeTerminal{cols: cols, rows: rows}
}

func (f *fakeTerminal) Write(s string) { f.writes = append(f.writes, s) }
func (f *fakeTerminal) Columns() int   { return f.cols }
func (f *fakeTerminal) Rows() int      { return f.rows }
func (f *fakeTerminal) HideCursor()    { f.cursorOps = append(f.cursorOps, "hide") }
func (f *fakeTerminal) ShowCursor()    { f.cursorOps = append(f.cursorOps, "show") }
func (f *fakeTerminal) all() string    { return strings.Join(f.writes, "") }
func (f *fakeTerminal) reset()         { f.writes = nil }

func TestFirstRenderPrintsAllLinesInsideSynchronizedOutput(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	s.Root.AddChild(NewText("hello", 0, 0, nil))
	s.RenderNow()
	out := term.all()
	if !strings.HasPrefix(out, "\x1b[?2026h") {
		t.Fatalf("missing sync begin: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("missing content: %q", out)
	}
	if !strings.Contains(out, "\x1b[?2026l") {
		t.Fatalf("missing sync end: %q", out)
	}
	// First render must not clear the screen.
	if strings.Contains(out, "\x1b[2J") {
		t.Fatalf("first render must not clear: %q", out)
	}
}

func TestEveryRenderedLineEndsWithSegmentReset(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	s.Root.AddChild(NewText("one\ntwo", 0, 0, nil))
	s.RenderNow()
	out := term.all()
	if strings.Count(out, "\x1b[0m\x1b]8;;\x07") < 2 {
		t.Fatalf("expected per-line segment resets, got %q", out)
	}
}

func TestUnchangedFrameWritesNoLineUpdates(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	s.Root.AddChild(NewText("stable", 0, 0, nil))
	s.RenderNow()
	term.reset()
	s.RenderNow()
	out := term.all()
	if strings.Contains(out, "\x1b[2K") || strings.Contains(out, "stable") {
		t.Fatalf("no-change render must not rewrite lines, got %q", out)
	}
}

func TestSingleLineChangeRewritesOnlyChangedRange(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	top := NewText("header", 0, 0, nil)
	spin := NewText("frame-1", 0, 0, nil)
	s.Root.AddChild(top)
	s.Root.AddChild(spin)
	s.RenderNow()
	term.reset()
	spin.SetText("frame-2")
	s.RenderNow()
	out := term.all()
	if !strings.Contains(out, "frame-2") {
		t.Fatalf("changed line must be rewritten: %q", out)
	}
	if strings.Contains(out, "header") {
		t.Fatalf("unchanged line must not be rewritten: %q", out)
	}
	if !strings.Contains(out, "\x1b[2K") {
		t.Fatalf("changed line must clear before write: %q", out)
	}
}

func TestWidthChangeForcesFullClearRedraw(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	s.Root.AddChild(NewText("resize me", 0, 0, nil))
	s.RenderNow()
	term.reset()
	term.cols = 60
	s.RenderNow()
	out := term.all()
	if !strings.Contains(out, "\x1b[2J\x1b[H\x1b[3J") {
		t.Fatalf("width change must clear screen and scrollback: %q", out)
	}
}

func TestShrinkingContentTriggersClearOnShrink(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	body := NewText("a\nb\nc\nd", 0, 0, nil)
	s.Root.AddChild(body)
	s.RenderNow()
	term.reset()
	body.SetText("a")
	s.RenderNow()
	out := term.all()
	if !strings.Contains(out, "\x1b[2J") {
		t.Fatalf("shrink must clear by default: %q", out)
	}
}

func TestCursorMarkerPositionsHardwareCursor(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	s.Root.AddChild(NewText("above", 0, 0, nil))
	s.Root.AddChild(&markerComponent{})
	s.RenderNow()
	out := term.all()
	if strings.Contains(out, CursorMarker) {
		t.Fatalf("marker must be stripped from output: %q", out)
	}
	// Column of "xy|" cursor = 2 -> 1-indexed column 3.
	if !strings.Contains(out, "\x1b[3G") {
		t.Fatalf("expected column positioning to col 3: %q", out)
	}
}

type markerComponent struct{}

func (m *markerComponent) Invalidate() {}
func (m *markerComponent) Render(width int) []string {
	return []string{"xy" + CursorMarker + "\x1b[7m \x1b[0m"}
}

func TestOversizedLineIsClippedNotCorrupting(t *testing.T) {
	term := newFakeTerminal(10, 5)
	s := NewMainScreen(term)
	s.Root.AddChild(&oversized{})
	s.RenderNow()
	term.reset()
	// Change to trigger the incremental path where the guard applies.
	s.Root.AddChild(NewText("x", 0, 0, nil))
	s.RenderNow()
	for _, w := range term.writes {
		// Sync markers use CSI finals outside the styling set; drop them
		// before measuring so only content width is asserted.
		w = strings.ReplaceAll(w, "\x1b[?2026h", "")
		w = strings.ReplaceAll(w, "\x1b[?2026l", "")
		for _, line := range strings.Split(w, "\r\n") {
			if VisibleWidth(line) > 10 {
				// The clip guard keeps content lines within terminal width.
				t.Fatalf("line exceeds width: %q", line)
			}
		}
	}
}

type oversized struct{}

func (o *oversized) Invalidate() {}
func (o *oversized) Render(width int) []string {
	return []string{strings.Repeat("A", width+7)}
}

func TestAppendingLinesUsesAppendPathWithoutFullRedraw(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	chat := NewText("line1", 0, 0, nil)
	s.Root.AddChild(chat)
	s.RenderNow()
	term.reset()
	chat.SetText("line1\nline2")
	s.RenderNow()
	out := term.all()
	if strings.Contains(out, "\x1b[2J") {
		t.Fatalf("append must not clear: %q", out)
	}
	if !strings.Contains(out, "line2") {
		t.Fatalf("appended line missing: %q", out)
	}
}
