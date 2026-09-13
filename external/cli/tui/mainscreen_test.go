//go:build cli

package tui

import (
	"strconv"
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

// plainSelectListTheme styles nothing, so assertions read the raw text the
// list lays out.
func plainSelectListTheme() SelectListTheme {
	id := func(s string) string { return s }
	return SelectListTheme{SelectedText: id, Description: id, ScrollInfo: id, NoMatch: id}
}

// A question option carries a whole sentence of explanation. Squeezed into the
// row's description column it loses most of itself and steals the label's
// width on the way; pi's SettingsList instead wraps the selected item's
// description under the list, and DescriptionBelow asks for that layout.
func TestSelectListWrapsTheSelectedDescriptionBelowTheList(t *testing.T) {
	long := "A self-contained pair on nas02 itself: the relay listens on a port and the node dials it, " +
		"so the swarm survives the laptop going away."
	items := []SelectItem{
		{Value: "0", Label: "Relay and node both on nas02 (recommended)", Description: long},
		{Value: "1", Label: "Relay on the laptop, node only on nas02", Description: "short"},
	}
	list := NewSelectList(items, 8, plainSelectListTheme(), SelectListLayout{DescriptionBelow: true})
	lines := list.Render(60)
	if len(lines) == 0 {
		t.Fatal("nothing rendered")
	}
	if !strings.Contains(lines[0], "Relay and node both on nas02 (recommended)") {
		t.Fatalf("the row must spend the full width on its label, got %q", lines[0])
	}
	for i, row := range lines[:2] {
		if strings.Contains(row, "self-contained") {
			t.Fatalf("row %d still carries the description: %q", i, row)
		}
	}
	block := strings.Join(lines[2:], "\n")
	if !strings.Contains(block, "self-contained") || !strings.Contains(block, "laptop going away") {
		t.Fatalf("the selected description is not readable below the list:\n%s", block)
	}
	if got := len(strings.Split(strings.TrimSpace(block), "\n")); got < 2 {
		t.Fatalf("a sentence that long must wrap, got %d line(s):\n%s", got, block)
	}
	for _, line := range lines {
		if w := VisibleWidth(line); w > 60 {
			t.Fatalf("line wider than the terminal (%d): %q", w, line)
		}
	}
	// The description follows the cursor.
	list.MoveDown()
	if tail := strings.Join(list.Render(60), "\n"); !strings.Contains(tail, "short") {
		t.Fatalf("the second option's description is missing:\n%s", tail)
	}
}

// Without the option nothing moves: slash commands and the selectors keep the
// two-column layout they were ported with.
func TestSelectListKeepsTheDescriptionColumnByDefault(t *testing.T) {
	items := []SelectItem{{Value: "compact", Label: "/compact", Description: "Compact the session"}}
	lines := NewSelectList(items, 8, plainSelectListTheme(), SelectListLayout{}).Render(60)
	if len(lines) != 1 {
		t.Fatalf("expected one row, got %q", lines)
	}
	if !strings.Contains(lines[0], "/compact") || !strings.Contains(lines[0], "Compact the session") {
		t.Fatalf("row lost its description column: %q", lines[0])
	}
}

// A label the width cannot hold is cut with an ellipsis, so the operator can
// see that the text continues instead of reading a word broken mid-syllable.
func TestSelectListMarksATruncatedLabel(t *testing.T) {
	items := []SelectItem{{Value: "0", Label: "Relay and node both on nas02 (recommended)"}}
	lines := NewSelectList(items, 8, plainSelectListTheme(), SelectListLayout{DescriptionBelow: true}).Render(24)
	if len(lines) == 0 || !strings.Contains(lines[0], "...") {
		t.Fatalf("truncated label carries no ellipsis: %q", lines)
	}
	if w := VisibleWidth(lines[0]); w > 24 {
		t.Fatalf("truncated row is %d wide: %q", w, lines[0])
	}
}

// A long console session keeps every past turn in the component tree, so the
// renderer walks the whole transcript on every frame. The tests below hold the
// steady-state cost of a frame to what actually changed: a container that sees
// the same child lines must hand back the composition it already built, and a
// frame rendered over a long backlog must not cost more than the same frame
// over a short one.

// discardTerminal is a Terminal that keeps no history, so allocation
// measurements see the renderer's own work and not the recorder's.
type discardTerminal struct{ cols, rows int }

func (d *discardTerminal) Write(string) {}
func (d *discardTerminal) Columns() int { return d.cols }
func (d *discardTerminal) Rows() int    { return d.rows }
func (d *discardTerminal) HideCursor()  {}
func (d *discardTerminal) ShowCursor()  {}

// transcriptTurns appends n turns shaped like the console chat: a user box, an
// assistant markdown block and a tool box with output.
func transcriptTurns(root *Container, n int) {
	bg := func(s string) string { return "\x1b[48;5;236m" + s + "\x1b[0m" }
	for i := 0; i < n; i++ {
		root.AddChild(NewSpacer(1))
		user := NewBox(1, 1, bg)
		user.AddChild(NewText("user prompt "+strconv.Itoa(i)+" with a reasonably long line of text", 0, 0, nil))
		root.AddChild(user)
		root.AddChild(NewMarkdown("Answer "+strconv.Itoa(i)+"\n\nSome **markdown** and `code`:\n\n- alpha\n- beta\n", 1, 0, MarkdownTheme{}))
		tool := NewBox(1, 1, bg)
		out := ""
		for j := 0; j < 10; j++ {
			out += "tool output line " + strconv.Itoa(j) + "\n"
		}
		tool.AddChild(NewText(out, 0, 0, nil))
		root.AddChild(tool)
	}
}

// A container whose children all returned the lines they returned last time
// has nothing to recompose: it must reuse the slice it already built, so the
// cost of an unchanged subtree is the walk itself and no per-line work.
func TestContainerReusesCompositionWhenChildrenUnchanged(t *testing.T) {
	c := &Container{}
	c.AddChild(NewText("alpha", 0, 0, nil))
	c.AddChild(NewText("beta", 0, 0, nil))
	first := c.Render(40)
	second := c.Render(40)
	if !sameLines(first, second) {
		t.Fatalf("container recomposed unchanged children")
	}
}

// The same holds for a Box, which additionally pads and paints every line: an
// unchanged box must not repaint its background on every frame.
func TestBoxReusesCompositionWhenChildrenUnchanged(t *testing.T) {
	b := NewBox(1, 1, func(s string) string { return "[" + s + "]" })
	b.AddChild(NewText("alpha", 0, 0, nil))
	first := b.Render(40)
	second := b.Render(40)
	if !sameLines(first, second) {
		t.Fatalf("box repainted unchanged children")
	}
}

// A Spacer is the most common child in the transcript; returning a fresh slice
// every time would make every ancestor recompose.
func TestSpacerReusesItsLines(t *testing.T) {
	s := NewSpacer(2)
	if !sameLines(s.Render(40), s.Render(40)) {
		t.Fatalf("spacer rebuilt its lines")
	}
}

// Reuse must not outlive the content: a change deep in the tree still reaches
// the top, and so does a width change.
func TestCachedCompositionStillFollowsContentAndWidth(t *testing.T) {
	leaf := NewText("alpha", 0, 0, nil)
	inner := &Container{}
	inner.AddChild(leaf)
	outer := &Container{}
	outer.AddChild(inner)

	outer.Render(40)
	leaf.SetText("omega")
	if got := strings.Join(outer.Render(40), " "); !strings.Contains(got, "omega") {
		t.Fatalf("deep content change did not reach the top: %q", got)
	}
	if got := outer.Render(20); VisibleWidth(got[0]) > 20 {
		t.Fatalf("width change did not reach the top: %q", got[0])
	}

	outer.AddChild(NewText("added", 0, 0, nil))
	if got := strings.Join(outer.Render(20), " "); !strings.Contains(got, "added") {
		t.Fatalf("new child did not reach the top: %q", got)
	}
	inner.Clear()
	if got := strings.Join(outer.Render(20), " "); strings.Contains(got, "omega") {
		t.Fatalf("cleared child still rendered: %q", got)
	}
}

// The renderer post-processes the frame in place (cursor marker, per-line
// resets). Now that Render can hand back a component's own cached slice, that
// rewriting must happen on a private copy, or the cache accumulates a reset
// suffix per frame and the transcript decays into escape sequences.
func TestRenderDoesNotRewriteTheComponentCache(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	text := NewText("alpha", 0, 0, nil)
	s.Root.AddChild(text)
	for i := 0; i < 3; i++ {
		s.RenderNow()
	}
	cached := text.Render(40)
	if n := strings.Count(cached[0], segmentReset); n != 0 {
		t.Fatalf("renderer wrote %d resets back into the component cache: %q", n, cached[0])
	}
	if n := strings.Count(s.Snapshot()[0], segmentReset); n != 1 {
		t.Fatalf("frame line carries %d resets, want 1: %q", n, s.Snapshot()[0])
	}
}

// The regression itself: streaming into the tail of a long transcript must
// cost what streaming into a short one costs. Allocation count stands in for
// the work, being the part of it that is deterministic across machines.
func TestSteadyFrameWorkIsIndependentOfTranscriptLength(t *testing.T) {
	frameAllocs := func(turns int) float64 {
		s := NewMainScreen(&discardTerminal{cols: 120, rows: 40})
		backlog := &Container{}
		transcriptTurns(backlog, turns)
		s.Root.AddChild(backlog)
		live := NewMarkdown("", 1, 0, MarkdownTheme{})
		s.Root.AddChild(live)
		s.RenderNow()
		// Alternate between two bodies of equal height so the frame length is
		// stable and the measurement sees only the steady-state path.
		bodies := []string{"streaming alpha", "streaming bravo"}
		i := 0
		return testing.AllocsPerRun(30, func() {
			live.SetText(bodies[i%2])
			i++
			s.RenderNow()
		})
	}

	short := frameAllocs(10)
	long := frameAllocs(400)
	t.Logf("allocations per frame: 10 turns=%.0f  400 turns=%.0f", short, long)
	// The backlog is 40x longer; a frame that re-walks it shows up as a large
	// multiple. Allow generous headroom for the unchanged-child scan itself.
	if long > short*2+50 {
		t.Fatalf("frame over a long transcript allocates %.0f vs %.0f over a short one: the backlog is re-rendered every frame", long, short)
	}
}

// A box renders its children at the content width but paints the background
// across the outer width, and max() maps several narrow outer widths onto one
// content width. Caching on the content width alone served rows painted for
// the previous terminal width (found in cross-review).
func TestBoxCacheFollowsTheOuterWidthNotOnlyTheContentWidth(t *testing.T) {
	b := NewBox(1, 1, func(s string) string { return "\x1b[48;5;236m" + s + "\x1b[0m" })
	b.AddChild(NewText("x", 0, 0, nil))
	for _, width := range []int{2, 3, 4, 3, 2} {
		lines := b.Render(width)
		for _, line := range lines {
			if got := VisibleWidth(StripTerminalSequences(line)); got != width {
				t.Fatalf("width %d: row is %d wide: %q", width, got, line)
			}
		}
	}
}

// The cache reads "the child handed back the same slice" as "the child did not
// change", so a leaf that changed must return a different backing array. Text
// and Markdown are the leaves the transcript is built from.
func TestChangedLeavesReturnADifferentBackingArray(t *testing.T) {
	text := NewText("alpha", 0, 0, nil)
	before := text.Render(40)
	text.SetText("omega")
	if sameLines(before, text.Render(40)) {
		t.Fatalf("Text reused its backing array after SetText")
	}

	md := NewMarkdown("alpha", 0, 0, MarkdownTheme{})
	mdBefore := md.Render(40)
	md.SetText("omega")
	if sameLines(mdBefore, md.Render(40)) {
		t.Fatalf("Markdown reused its backing array after SetText")
	}
}

// Clearing a transcript must release the rendered lines: they are the largest
// thing the console holds, and the scratch array keeps entries past its own
// length where nothing overwrites them (found in cross-review).
func TestClearingReleasesTheCachedRenders(t *testing.T) {
	c := &Container{}
	for i := 0; i < 4; i++ {
		c.AddChild(NewText("line "+strconv.Itoa(i), 0, 0, nil))
	}
	c.Render(40)
	c.Clear()
	if c.childLines != nil || c.cachedLines != nil {
		t.Fatalf("Clear left renders pinned: childLines=%v cachedLines=%v", c.childLines != nil, c.cachedLines != nil)
	}

	// Shrinking by removal must release the tail of the scratch array too.
	d := &Container{}
	kept := NewText("kept", 0, 0, nil)
	dropped := NewText("dropped", 0, 0, nil)
	d.AddChild(kept)
	d.AddChild(dropped)
	d.Render(40)
	d.RemoveChild(dropped)
	d.Render(40)
	full := d.childLines[:cap(d.childLines)]
	for i := len(d.childLines); i < len(full); i++ {
		if full[i] != nil {
			t.Fatalf("scratch slot %d still pins a removed child's render", i)
		}
	}
}

// The renderer reuses two frame buffers, and a shorter frame overwrites only
// their prefix. The garbage collector scans the whole backing array, so a
// cleared transcript would stay alive in the tails (found in cross-review).
func TestShrinkingTheFrameReleasesTheBufferTails(t *testing.T) {
	s := NewMainScreen(&discardTerminal{cols: 80, rows: 24})
	long := &Container{}
	for i := 0; i < 40; i++ {
		long.AddChild(NewText("a long transcript row "+strconv.Itoa(i), 0, 0, nil))
	}
	s.Root.AddChild(long)
	// Two frames fill both slots.
	s.RenderNow()
	s.RenderNow()

	long.Clear()
	long.AddChild(NewText("just one row", 0, 0, nil))
	s.RenderNow()
	s.RenderNow()

	for slot := range s.frameBufs {
		for _, buf := range [][]string{s.frameBufs[slot], s.rawBufs[slot]} {
			full := buf[:cap(buf)]
			for i := len(buf); i < len(full); i++ {
				if strings.Contains(full[i], "a long transcript row") {
					t.Fatalf("slot %d still pins a dropped row at index %d: %q", slot, i, full[i])
				}
			}
		}
	}
}

// Removing a box's last child leaves it rendering nothing, and that early
// return reaches neither renderChildren nor store, so the release has to
// happen on that path (found in cross-review).
func TestEmptyingABoxReleasesItsCachedRender(t *testing.T) {
	b := NewBox(1, 1, nil)
	child := NewText("tool output that is worth releasing", 0, 0, nil)
	b.AddChild(child)
	b.Render(40)
	b.RemoveChild(child)
	if lines := b.Render(40); lines != nil {
		t.Fatalf("an empty box rendered %q", lines)
	}
	if b.childLines != nil || b.cachedLines != nil {
		t.Fatalf("emptied box still pins its render: childLines=%v cachedLines=%v", b.childLines != nil, b.cachedLines != nil)
	}
}

// The frame is composed into two buffers that alternate, and previousLines
// points at whichever one the last frame used. If a frame were written into
// the buffer the diff still reads, an unchanged render in between would make
// the next change compare equal to itself and never reach the terminal, while
// a snapshot of the frame still looked right. Render the same content twice,
// then change it (found in cross-review).
func TestAChangeAfterAnUnchangedFrameStillReachesTheTerminal(t *testing.T) {
	term := newFakeTerminal(40, 10)
	s := NewMainScreen(term)
	text := NewText("alpha", 0, 0, nil)
	s.Root.AddChild(text)

	s.RenderNow()
	s.RenderNow() // identical frame: nothing moves
	term.reset()

	text.SetText("omega")
	s.RenderNow()
	if out := term.all(); !strings.Contains(out, "omega") {
		t.Fatalf("the change never reached the terminal: %q", out)
	}
	if snap := strings.Join(s.Snapshot(), " "); !strings.Contains(snap, "omega") {
		t.Fatalf("the frame does not carry the change: %q", snap)
	}

	// And once more, so the buffers have alternated a full cycle.
	s.RenderNow()
	term.reset()
	text.SetText("third")
	s.RenderNow()
	if out := term.all(); !strings.Contains(out, "third") {
		t.Fatalf("the second change never reached the terminal: %q", out)
	}
}
