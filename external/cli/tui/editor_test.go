//go:build cli

package tui

import (
	"strings"
	"testing"
)

func newTestEditor() *Editor {
	return NewEditor(nil, EditorTheme{}, 0)
}

func TestEditorEncodedPrintableInput(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"modifyOtherKeys uppercase", "\x1b[27;2;65~", "A"},
		{"modifyOtherKeys punctuation", "\x1b[27;2;33~", "!"},
		{"modifyOtherKeys Cyrillic", "\x1b[27;2;1046~", "Ж"},
		{"modifyOtherKeys space", "\x1b[27;2;32~", " "},
		{"Kitty uppercase", "\x1b[97;2u", "A"},
		{"Kitty shifted punctuation", "\x1b[49:33;2u", "!"},
		{"Kitty Cyrillic", "\x1b[1078;2u", "Ж"},
		{"Kitty associated text", "\x1b[97;2;65u", "A"},
		{"Kitty composed text", "\x1b[0;;101:769u", "e\u0301"},
		{"Kitty repeat", "\x1b[97;2:2u", "A"},
		{"plain encoded character", "\x1b[97u", "a"},
		{"encoded space", "\x1b[32u", " "},
		{"Kitty release", "\x1b[97;2:3u", ""},
		{"Kitty control", "\x1b[97;6u", ""},
		{"Kitty alt", "\x1b[97;3u", ""},
		{"Kitty super", "\x1b[97;9u", ""},
		{"Kitty hyper", "\x1b[97;17u", ""},
		{"Kitty private key", "\x1b[57441;2u", ""},
		{"Kitty invalid codepoint", "\x1b[1114112;2u", ""},
		{"Kitty surrogate", "\x1b[55296;2u", ""},
		{"Kitty wrapped codepoint", "\x1b[4294967361;2u", ""},
		{"modifyOtherKeys control", "\x1b[27;6;65~", ""},
		{"Kitty shortcut with text", "\x1b[97;6;65u", ""},
		{"Kitty invalid text", "\x1b[97;2;27u", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEditor()
			e.HandleInput([]byte(tc.input))
			if got := e.Text(); got != tc.want {
				t.Fatalf("text %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEncodedKeyShortcutMatchingIgnoresText(t *testing.T) {
	for _, tc := range []struct{ input, spec string }{
		{"\x1b[97u", "a"},
		{"\x1b[97;2u", "shift+a"},
		{"\x1b[27;2;65~", "shift+a"},
		{"\x1b[99;5u", "ctrl+c"},
		{"\x1b[13;2u", "shift+enter"},
	} {
		if !MatchesKey([]byte(tc.input), tc.spec) {
			t.Errorf("%q must match %s", tc.input, tc.spec)
		}
	}
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

func TestEditorShiftBackspace(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"modifyOtherKeys DEL", "\x1b[27;2;127~", "hello "},
		{"modifyOtherKeys BS", "\x1b[27;2;8~", "hello "},
		{"Kitty press", "\x1b[127;2u", "hello "},
		{"Kitty repeat", "\x1b[127;2:2u", "hello "},
		{"Kitty release", "\x1b[127;2:3u", "hello 🙂"},
		{"shift enter keeps its binding", "\x1b[13;2u", "hello 🙂\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEditor()
			e.HandleInput([]byte("hello 🙂"))
			e.HandleInput([]byte(tc.input))
			if got := e.Text(); got != tc.want {
				t.Fatalf("text %q, want %q", got, tc.want)
			}
		})
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

func TestEditorDeletesAnImageMarkerAsOnePiece(t *testing.T) {
	e := NewEditor(nil, EditorTheme{}, 0)
	e.SetText("look")
	e.InsertAtom("[image #1]")
	if e.Text() != "look [image #1] " {
		t.Fatalf("atom inserted as %q", e.Text())
	}
	e.HandleInput([]byte{0x7f}) // the space after it
	e.HandleInput([]byte{0x7f}) // the whole marker
	if e.Text() != "look " {
		t.Fatalf("after two backspaces %q, want the marker gone in one", e.Text())
	}
	e.SetText("[image #2] x")
	e.cursorCol = 0
	e.HandleInput([]byte("\x1b[3~")) // delete
	if e.Text() != " x" {
		t.Fatalf("delete before the marker left %q", e.Text())
	}
}
