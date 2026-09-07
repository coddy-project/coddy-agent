//go:build cli

package tui

import (
	"strings"
	"testing"
)

func newTestEditor() *Editor {
	return NewEditor(nil, EditorTheme{}, 0)
}

func TestEditorTypingAndSubmitRoundTrip(t *testing.T) {
	e := newTestEditor()
	var submitted string
	e.OnSubmit = func(text string) { submitted = text }
	e.HandleInput([]byte("hello"))
	e.HandleInput([]byte(" world"))
	e.HandleInput([]byte("\r"))
	if submitted != "hello world" {
		t.Fatalf("submitted %q", submitted)
	}
	if !e.IsEmpty() {
		t.Fatal("editor must reset after submit")
	}
}

func TestEditorNewlineViaCtrlJKeepsMultiline(t *testing.T) {
	e := newTestEditor()
	e.HandleInput([]byte("first"))
	e.HandleInput([]byte("\n")) // ctrl+j
	e.HandleInput([]byte("second"))
	if e.Text() != "first\nsecond" {
		t.Fatalf("text %q", e.Text())
	}
}

func TestEditorBackslashEnterInsertsNewline(t *testing.T) {
	e := newTestEditor()
	e.HandleInput([]byte(`line\`))
	e.HandleInput([]byte("\r"))
	if e.Text() != "line\n" {
		t.Fatalf("text %q", e.Text())
	}
}

func TestEditorBackspaceRespectsGraphemeBoundaries(t *testing.T) {
	e := newTestEditor()
	e.HandleInput([]byte("a🙂"))
	e.HandleInput([]byte{0x7f})
	if e.Text() != "a" {
		t.Fatalf("text %q", e.Text())
	}
}

func TestEditorHistoryBrowsesOnUpAndRestoresDraft(t *testing.T) {
	e := newTestEditor()
	e.AddToHistory("older prompt")
	e.AddToHistory("newer prompt")
	e.HandleInput([]byte("draft"))
	e.HandleInput([]byte("\x01")) // ctrl+a -> line start so up enters history
	e.HandleInput([]byte("\x1b[A"))
	if e.Text() != "newer prompt" {
		t.Fatalf("first up should load newest entry, got %q", e.Text())
	}
	e.HandleInput([]byte("\x1b[A"))
	if e.Text() != "older prompt" {
		t.Fatalf("second up should load older entry, got %q", e.Text())
	}
	e.HandleInput([]byte("\x1b[B"))
	e.HandleInput([]byte("\x1b[B"))
	if e.Text() != "draft" {
		t.Fatalf("down past newest must restore the draft, got %q", e.Text())
	}
}

func TestEditorLargePasteCollapsesToMarkerAndExpandsOnSubmit(t *testing.T) {
	e := newTestEditor()
	body := strings.Repeat("line\n", 30) + "tail"
	e.InsertPaste(body)
	if !strings.Contains(e.Text(), "[paste #1 +31 lines]") {
		t.Fatalf("expected paste marker, got %q", e.Text())
	}
	var submitted string
	e.OnSubmit = func(text string) { submitted = text }
	e.HandleInput([]byte("\r"))
	if !strings.Contains(submitted, "tail") || strings.Contains(submitted, "[paste #1") {
		t.Fatalf("submit must expand the marker, got %q", submitted)
	}
}

func TestEditorSmallPasteStaysInline(t *testing.T) {
	e := newTestEditor()
	e.InsertPaste("short paste")
	if e.Text() != "short paste" {
		t.Fatalf("text %q", e.Text())
	}
}

func TestEditorRenderShowsBordersAndCursor(t *testing.T) {
	e := newTestEditor()
	e.SetFocused(true)
	e.HandleInput([]byte("abc"))
	lines := e.Render(40)
	if len(lines) < 3 {
		t.Fatalf("expected borders around content, got %d lines", len(lines))
	}
	if !strings.Contains(lines[0], "─") || !strings.Contains(lines[len(lines)-1], "─") {
		t.Fatalf("borders missing: %q / %q", lines[0], lines[len(lines)-1])
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, CursorMarker) {
		t.Fatal("focused editor must emit the cursor marker")
	}
	if !strings.Contains(joined, "\x1b[7m") {
		t.Fatal("fake cursor must render in reverse video")
	}
}

func TestEditorWordDeleteBackward(t *testing.T) {
	e := newTestEditor()
	e.HandleInput([]byte("one two three"))
	e.HandleInput([]byte{0x17}) // ctrl+w
	if e.Text() != "one two " {
		t.Fatalf("text %q", e.Text())
	}
}
