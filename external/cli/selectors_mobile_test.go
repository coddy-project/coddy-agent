//go:build cli

package cli

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
)

func TestSelectorFitsNineRowMobileTerminalAfterNavigationAndResize(t *testing.T) {
	items := make([]tui.SelectItem, 20)
	for i := range items {
		items[i] = tui.SelectItem{Value: fmt.Sprint(i), Label: fmt.Sprintf("Command %d", i)}
	}
	modal := newSelectorModal(newTheme("dark"), "Select command", items, 10, nil)
	modal.SetTerminalRows(9)
	for i := 0; i < 15; i++ {
		modal.HandleInput([]byte("\x1b[B"))
	}
	if lines := modal.Render(61); len(lines) > 8 {
		t.Fatalf("selector uses %d rows plus footer in a nine-row terminal", len(lines))
	}
	modal.SetTerminalRows(7)
	if lines := modal.Render(61); len(lines) > 6 {
		t.Fatalf("selector uses %d rows plus footer after resize to seven rows", len(lines))
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
