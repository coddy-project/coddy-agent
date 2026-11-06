//go:build cli

package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// selectorItems builds a list longer than any terminal under test.
func selectorItems(n int) []tui.SelectItem {
	items := make([]tui.SelectItem, n)
	for i := range items {
		items[i] = tui.SelectItem{Value: fmt.Sprint(i), Label: fmt.Sprintf("item-%d", i)}
	}
	return items
}

// drawnItems counts the rows of a rendered selector that carry a list item.
func drawnItems(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "item-") {
			n++
		}
	}
	return n
}

// fitTo gives the modal a terminal of rows with foot drawn under it, the way
// the app does. The footer is the real one, so a budget that miscounts it is a
// failure here rather than an assumption both sides share.
func fitTo(m *selectorModal, foot *footer, rows int) {
	m.FitTerminal(rows, func(width int) int { return len(foot.Render(width)) })
}

func TestSelectorFitsNineRowMobileTerminalAfterNavigationAndResize(t *testing.T) {
	modal := newSelectorModal(newTheme("dark"), "Select model", selectorItems(20), 10, nil)
	foot := newFooter(newTheme("dark"), t.TempDir())
	fitTo(modal, foot, 9)
	for i := 0; i < 15; i++ {
		modal.HandleInput([]byte("\x1b[B"))
	}
	assertSelectorFits(t, modal, foot, 9, 61)
	fitTo(modal, foot, 8)
	assertSelectorFits(t, modal, foot, 8, 61)
}

// The goal menu and the MCP trust prompt write a body between the title and
// the list, and the goal menu rewrites it while the modal is on screen, so the
// rows it takes come out of the list rather than out of the title.
func TestSelectorWithABodyFitsAShortTerminal(t *testing.T) {
	modal := newSelectorModal(newTheme("dark"), "Session goal", selectorItems(20), 10, nil)
	modal.body = strings.Repeat("a body line\n", 5) + "a body line"
	modal.rebuild()
	foot := newFooter(newTheme("dark"), t.TempDir())
	fitTo(modal, foot, 16)
	assertSelectorFits(t, modal, foot, 16, 61)
}

// A turn override gives the footer a third line, and the row it takes comes
// out of the list like any other.
func TestSelectorGivesUpARowToAGrownFooter(t *testing.T) {
	theme := newTheme("dark")
	modal := newSelectorModal(theme, "Select model", selectorItems(20), 10, nil)
	foot := newFooter(theme, t.TempDir())
	foot.SetContext(0.4, 131072)
	fitTo(modal, foot, 10)
	assertSelectorFits(t, modal, foot, 10, 61)
	before := drawnItems(modal.Render(61))

	foot.SetSettings("ask", []acp.TurnOverride{{Setting: "model", Value: "stub/model-two", TurnsLeft: 2}})
	if grown := len(foot.Render(61)); grown != 3 {
		t.Fatalf("the footer drew %d rows with a turn override, want 3", grown)
	}
	assertSelectorFits(t, modal, foot, 10, 61)
	if after := drawnItems(modal.Render(61)); after != before-1 {
		t.Fatalf("the list drew %d rows under the grown footer, want %d", after, before-1)
	}
}

// A terminal too short for the chrome alone still shows one row of the list:
// the selection is the point of the modal.
func TestSelectorKeepsOneRowInATerminalTooShortForIt(t *testing.T) {
	modal := newSelectorModal(newTheme("dark"), "Select model", selectorItems(20), 10, nil)
	fitTo(modal, newFooter(newTheme("dark"), t.TempDir()), 4)
	if n := drawnItems(modal.Render(61)); n != 1 {
		t.Fatalf("the list drew %d rows in a four-row terminal, want 1", n)
	}
}

// assertSelectorFits holds the modal and the footer under it to the terminal,
// and the highlighted row and the title to the rows that are drawn.
func assertSelectorFits(t *testing.T, m *selectorModal, foot *footer, rows, width int) {
	t.Helper()
	lines := m.Render(width)
	under := len(foot.Render(width))
	if len(lines)+under > rows {
		t.Fatalf("selector uses %d rows over a %d-row footer in a %d-row terminal",
			len(lines), under, rows)
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "→ ") }) {
		t.Fatalf("the highlighted row is not among the %d drawn rows", len(lines))
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, m.title) }) {
		t.Fatalf("the title %q is not among the %d drawn rows", m.title, len(lines))
	}
}

func TestFastEscapeThenSlashKeepsNewCommand(t *testing.T) {
	for _, chunks := range [][][]byte{
		{[]byte("\x1b/model\r")},
		{[]byte("\x1b"), []byte("/model\r")},
	} {
		buffer := tui.NewStdinBuffer(time.Second)
		var got []string
		buffer.Emit = func(seq []byte) { got = append(got, string(seq)) }
		for _, chunk := range chunks {
			buffer.Feed(chunk)
		}
		if want := []string{"\x1b", "/model", "\r"}; !slices.Equal(got, want) {
			t.Fatalf("input events = %q, want %q", got, want)
		}
	}
}
