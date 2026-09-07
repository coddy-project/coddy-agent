//go:build cli

package tui

import (
	"strings"
	"testing"
)

func TestVisibleWidthCountsCellsNotBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"plain ascii", "hello", 5},
		{"csi styling is invisible", "\x1b[31mred\x1b[0m", 3},
		{"truecolor styling is invisible", "\x1b[38;2;147;51;234mx\x1b[39m", 1},
		{"osc8 hyperlink is invisible", "\x1b]8;;https://x\x07link\x1b]8;;\x07", 4},
		{"apc cursor marker is invisible", "\x1b_pi:c\x07a", 1},
		{"tab is three cells", "a\tb", 5},
		{"cjk is double width", "你好", 4},
		{"mixed ascii and cjk", "a你b", 4},
		{"emoji is double width", "🙂", 2},
		{"zwj family is double width", "👨‍👩‍👧", 2},
		{"regional indicator pair", "🇺🇦", 2},
		{"combining mark adds nothing", "é", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VisibleWidth(tc.in); got != tc.want {
				t.Fatalf("VisibleWidth(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripTerminalSequencesKeepsOnlyVisibleText(t *testing.T) {
	in := "\x1b[1m\x1b[38;5;10mbold\x1b[0m \x1b]8;;http://a\x07link\x1b]8;;\x07\x1b_pi:c\x07!"
	if got := StripTerminalSequences(in); got != "bold link!" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractAnsiCodeRecognizesSupportedFinalsOnly(t *testing.T) {
	if got := ExtractAnsiCode("\x1b[2K", 0); got == nil || got.Code != "\x1b[2K" {
		t.Fatalf("CSI K not extracted: %#v", got)
	}
	// CSI ending in A (cursor up) is not in the supported set and counts as text.
	if got := ExtractAnsiCode("\x1b[2A", 0); got != nil {
		t.Fatalf("CSI A unexpectedly extracted: %#v", got)
	}
	if got := ExtractAnsiCode("no escape", 0); got != nil {
		t.Fatalf("expected nil for plain text")
	}
}

func TestWrapTextWrapsAtWordBoundaries(t *testing.T) {
	lines := WrapTextWithANSI("the quick brown fox jumps", 10)
	want := []string{"the quick", "brown fox", "jumps"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", lines)
	}
}

func TestWrapTextPreservesStylingAcrossBreaks(t *testing.T) {
	lines := WrapTextWithANSI("\x1b[31maaaa bbbb cccc\x1b[0m", 9)
	if len(lines) != 2 {
		t.Fatalf("expected two lines, got %q", lines)
	}
	if !strings.HasPrefix(lines[1], "\x1b[31m") {
		t.Fatalf("continuation must re-apply red, got %q", lines[1])
	}
}

func TestWrapTextBreaksOversizedWordByCell(t *testing.T) {
	lines := WrapTextWithANSI("abcdefghij", 4)
	want := []string{"abcd", "efgh", "ij"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", lines)
	}
}

func TestWrapTextAllowsBreaksBetweenCJKGraphemes(t *testing.T) {
	lines := WrapTextWithANSI("你好世界你好", 4)
	for i, l := range lines {
		if w := VisibleWidth(l); w > 4 {
			t.Fatalf("line %d %q exceeds width: %d", i, l, w)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %q", lines)
	}
}

func TestWrapTextKeepsLiteralNewlines(t *testing.T) {
	lines := WrapTextWithANSI("a\nb\r\nc", 10)
	want := []string{"a", "b", "c"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", lines)
	}
}

func TestWrapTextClosesUnderlineAtLineEnd(t *testing.T) {
	lines := WrapTextWithANSI("\x1b[4maaaa bbbb\x1b[24m", 4)
	if len(lines) < 2 {
		t.Fatalf("expected wrap, got %q", lines)
	}
	if !strings.Contains(lines[0], "\x1b[24m") {
		t.Fatalf("first line must close underline, got %q", lines[0])
	}
}

func TestTruncateToWidthAddsEllipsisAndReset(t *testing.T) {
	got := TruncateToWidth("abcdefghij", 6, "...")
	if VisibleWidth(got) != 6 {
		t.Fatalf("width %d, got %q", VisibleWidth(got), got)
	}
	if !strings.HasSuffix(got, "...\x1b[0m") || !strings.Contains(got, "abc") {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateToWidthKeepsShortTextVerbatim(t *testing.T) {
	if got := TruncateToWidth("short", 10, "..."); got != "short" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateToWidthEmptyEllipsisHardClips(t *testing.T) {
	got := TruncateToWidth("abcdef", 3, "")
	if VisibleWidth(got) != 3 || !strings.HasPrefix(got, "abc") {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateToWidthPadReachesExactWidth(t *testing.T) {
	got := TruncateToWidthPad("ab", 5, "...")
	if got != "ab   " {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateToWidthDoesNotSplitWideRune(t *testing.T) {
	got := TruncateToWidth("你好世界", 5, "...")
	if VisibleWidth(got) > 5 {
		t.Fatalf("width %d for %q", VisibleWidth(got), got)
	}
}

func TestApplyBackgroundToLinePadsToFullWidth(t *testing.T) {
	bg := func(s string) string { return "\x1b[48;2;45;45;45m" + s + "\x1b[49m" }
	got := ApplyBackgroundToLine("hi", 6, bg)
	if got != "\x1b[48;2;45;45;45mhi    \x1b[49m" {
		t.Fatalf("got %q", got)
	}
}

func TestSliceByColumnHonorsAnsiAndWideChars(t *testing.T) {
	if got := SliceByColumn("abcdef", 1, 3); got != "bcd" {
		t.Fatalf("got %q", got)
	}
	styled := "\x1b[31mabcdef\x1b[0m"
	got := SliceByColumn(styled, 2, 2)
	if StripTerminalSequences(got) != "cd" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Fatalf("pending ansi must be carried into the slice, got %q", got)
	}
}
