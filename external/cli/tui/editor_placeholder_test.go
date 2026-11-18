//go:build cli

package tui

import (
	"strings"
	"testing"
)

func TestEditorPlaceholderTabTakesItIntoBuffer(t *testing.T) {
	e := newTestEditor()
	var accepted string
	e.OnPlaceholderAccepted = func(text string) { accepted = text }
	e.SetPlaceholder("run the tests")
	e.HandleInput([]byte("\t"))
	if e.Text() != "run the tests" {
		t.Fatalf("after tab the buffer holds %q", e.Text())
	}
	if accepted != "run the tests" {
		t.Fatalf("accept callback saw %q", accepted)
	}
}

func TestEditorPlaceholderRightArrowTakesIt(t *testing.T) {
	e := newTestEditor()
	e.SetPlaceholder("push it")
	e.HandleInput([]byte{0x1b, '[', 'C'}) // right
	if e.Text() != "push it" {
		t.Fatalf("after right the buffer holds %q", e.Text())
	}
}

func TestEditorPlaceholderIgnoredOnceTextTyped(t *testing.T) {
	e := newTestEditor()
	e.SetPlaceholder("commit this")
	// A draft the operator typed hides the suggestion from Tab: the tab
	// inserts as it does without a placeholder (four spaces, like paste
	// normalization).
	e.HandleInput([]byte("f"))
	e.HandleInput([]byte("\t"))
	if e.Text() != "f    " {
		t.Fatalf("buffer holds %q", e.Text())
	}
}

func TestEditorPlaceholderRendersInEmptyEditorOnly(t *testing.T) {
	e := newTestEditor()
	e.SetPlaceholder("try it out")
	if !placeholderRendered(e.Render(40), "try it out") {
		t.Fatalf("render did not show the placeholder: %q", e.Render(40))
	}
	e.HandleInput([]byte("x"))
	if placeholderRendered(e.Render(40), "try it out") {
		t.Fatalf("placeholder still shows with a draft: %q", e.Render(40))
	}
}

func placeholderRendered(lines []string, text string) bool {
	for _, l := range lines {
		if strings.Contains(l, text) {
			return true
		}
	}
	return false
}
