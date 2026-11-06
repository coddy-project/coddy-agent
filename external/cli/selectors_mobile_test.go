//go:build cli

package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
)

// selectorItems builds a list longer than any terminal under test.
func selectorItems(n int) []tui.SelectItem {
	items := make([]tui.SelectItem, n)
	for i := range items {
		items[i] = tui.SelectItem{Value: fmt.Sprint(i), Label: fmt.Sprintf("Command %d", i)}
	}
	return items
}

func TestSelectorFitsNineRowMobileTerminalAfterNavigationAndResize(t *testing.T) {
	modal := newSelectorModal(newTheme("dark"), "Select command", selectorItems(20), 10, nil)
	modal.SetTerminalRows(9)
	for i := 0; i < 15; i++ {
		modal.HandleInput([]byte("\x1b[B"))
	}
	assertSelectorFits(t, modal, 9, 61)
	modal.SetTerminalRows(8)
	assertSelectorFits(t, modal, 8, 61)
}

// The goal menu and the MCP trust prompt write a body between the title and
// the list, and the goal menu rewrites it while the modal is on screen, so the
// rows it takes come out of the list rather than out of the title.
func TestSelectorWithABodyFitsAShortTerminal(t *testing.T) {
	modal := newSelectorModal(newTheme("dark"), "Session goal", selectorItems(20), 10, nil)
	modal.body = strings.Repeat("a body line\n", 5) + "a body line"
	modal.rebuild()
	modal.SetTerminalRows(16)
	assertSelectorFits(t, modal, 16, 61)
}

// A terminal too short for the chrome alone still shows one row of the list:
// the selection is the point of the modal.
func TestSelectorKeepsOneRowInATerminalTooShortForIt(t *testing.T) {
	modal := newSelectorModal(newTheme("dark"), "Select command", selectorItems(20), 10, nil)
	modal.SetTerminalRows(4)
	lines := modal.Render(61)
	if want := 1 + modal.chromeRows(61) - selectorFooterRows; len(lines) != want {
		t.Fatalf("selector uses %d rows, want %d (one list row and its chrome)", len(lines), want)
	}
}

// assertSelectorFits holds the modal and the footer under it to the terminal,
// and the highlighted row to the part of the list that is drawn.
func assertSelectorFits(t *testing.T, m *selectorModal, rows, width int) {
	t.Helper()
	lines := m.Render(width)
	if len(lines)+selectorFooterRows > rows {
		t.Fatalf("selector uses %d rows plus a %d-row footer in a %d-row terminal",
			len(lines), selectorFooterRows, rows)
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "→ ") }) {
		t.Fatalf("the highlighted row is not among the %d drawn rows", len(lines))
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, m.title) }) {
		t.Fatalf("the title %q scrolled out of the %d drawn rows", m.title, len(lines))
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
