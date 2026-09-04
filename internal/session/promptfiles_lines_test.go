package session

import "testing"

func TestSliceLines(t *testing.T) {
	const text = "one\ntwo\nthree\nfour\n"
	cases := []struct {
		name       string
		start, end int
		want       string
	}{
		{"middle range", 2, 3, "two\nthree"},
		{"single line", 1, 1, "one"},
		{"end past last line clamps", 3, 99, "three\nfour"},
		{"start past last line falls back to whole text", 99, 100, text},
		{"no range returns whole text", 0, 0, text},
		{"inverted range returns whole text", 4, 2, text},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sliceLines(text, c.start, c.end); got != c.want {
				t.Fatalf("sliceLines(%d,%d) = %q, want %q", c.start, c.end, got, c.want)
			}
		})
	}
}

// CRLF content keeps its "\r" between lines and drops it only at the tail, so a
// pasted fragment matches what the editor showed.
func TestSliceLinesCRLF(t *testing.T) {
	got := sliceLines("a\r\nb\r\nc\r\n", 1, 2)
	if got != "a\r\nb" {
		t.Fatalf("got %q", got)
	}
}

func TestSliceLinesNoTrailingNewline(t *testing.T) {
	if got := sliceLines("a\nb\nc", 2, 3); got != "b\nc" {
		t.Fatalf("got %q", got)
	}
}

func TestLineRangeURIRoundTrip(t *testing.T) {
	uri := lineRangeURI("docs/ui.md", 10, 20)
	if uri != "docs/ui.md#L10-20" {
		t.Fatalf("got %q", uri)
	}
	base, s, e := splitLineRangeURI(uri)
	if base != "docs/ui.md" || s != 10 || e != 20 {
		t.Fatalf("got %q %d %d", base, s, e)
	}
}

func TestLineRangeURIOmitsInvalidRanges(t *testing.T) {
	for _, c := range [][2]int{{0, 0}, {0, 5}, {5, 4}} {
		if got := lineRangeURI("f.go", c[0], c[1]); got != "f.go" {
			t.Fatalf("lineRangeURI(%d,%d) = %q", c[0], c[1], got)
		}
	}
}

// A path that merely contains "#L" without a well-formed range is left alone,
// so a real file named that way still resolves.
func TestSplitLineRangeURIIgnoresMalformedFragment(t *testing.T) {
	for _, uri := range []string{"f.go", "f.go#L5", "f.go#Labc-def", "f.go#L0-3", "f.go#L9-2"} {
		base, s, e := splitLineRangeURI(uri)
		if base != uri || s != 0 || e != 0 {
			t.Fatalf("splitLineRangeURI(%q) = %q %d %d", uri, base, s, e)
		}
	}
}

func TestStripCoddyAttachmentXML(t *testing.T) {
	raw := "@Dockerfile:21-31 why slow?\n\n<coddy_attachment path=\"Dockerfile\" name=\"Dockerfile\" lines=\"21-31\">\n<![CDATA[FROM x]]>\n</coddy_attachment>"
	if got := stripCoddyAttachmentXML(raw); got != "@Dockerfile:21-31 why slow?\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStripCoddyAttachmentXMLMultipleBlocks(t *testing.T) {
	raw := "a<coddy_attachment path=\"x\">\n<![CDATA[1]]>\n</coddy_attachment>b<coddy_attachment path=\"y\">\n<![CDATA[2]]>\n</coddy_attachment>c"
	if got := stripCoddyAttachmentXML(raw); got != "abc" {
		t.Fatalf("got %q", got)
	}
}
