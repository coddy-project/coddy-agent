package platform

import "testing"

func TestSanitizeOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text is untouched", in: "App running at http://localhost:8081/", want: "App running at http://localhost:8081/"},
		{name: "empty", in: "", want: ""},
		{name: "colour", in: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "webpack redraw", in: "\x1b[2K\x1b[1A\x1b[2K\x1b[G WAIT  Compiling...", want: " WAIT  Compiling..."},
		{name: "osc with bel", in: "\x1b]0;npm run serve\x07done", want: "done"},
		{name: "osc with string terminator", in: "\x1b]0;title\x1b\\done", want: "done"},
		{name: "charset designation", in: "\x1b(Bplain", want: "plain"},
		{name: "progress redraw", in: "10%\r50%\r100%\n", want: "100%\n"},
		{name: "crlf becomes lf", in: "one\r\ntwo\r\n", want: "one\ntwo\n"},
		{name: "trailing carriage return", in: "value\r", want: "value"},
		{name: "blank runs collapse", in: "a\r\n\n\n\n\nb", want: "a\n\nb"},
		{name: "non-ascii survives", in: "\x1b[32mРусский текст — em dash ✅ 中文\x1b[0m", want: "Русский текст — em dash ✅ 中文"},
		{name: "lone trailing escape", in: "text\x1b", want: "text"},
		{name: "unterminated csi", in: "text\x1b[12", want: "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeOutput(tc.in)
			if got != tc.want {
				t.Fatalf("SanitizeOutput(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := SanitizeOutput(got); again != got {
				t.Fatalf("SanitizeOutput is not idempotent: %q became %q", got, again)
			}
		})
	}
}

func TestStripANSIKeepsOrdinaryText(t *testing.T) {
	const in = "Expand `agent.qwen.md` from 5 points to **7 structured blocks** (21 items)"
	if got := StripANSI(in); got != in {
		t.Fatalf("StripANSI altered plain text: %q", got)
	}
}

func TestCollapseCarriageReturnsKeepsLinesWithoutOne(t *testing.T) {
	const in = "first\nsecond\n"
	if got := CollapseCarriageReturns(in); got != in {
		t.Fatalf("CollapseCarriageReturns altered %q into %q", in, got)
	}
}
