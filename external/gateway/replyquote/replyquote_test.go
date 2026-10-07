//go:build gateway || gateway.telegram || gateway.pachca

package replyquote_test

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/replyquote"
)

func TestPrompt(t *testing.T) {
	cases := []struct {
		name, author, quoted, text, want string
	}{
		{"no reply", "", "", "hello", "hello"},
		{"reply with an author", "Anna", "the build is red\nsince noon", "why?",
			"> Anna:\n> the build is red\n> since noon\n\nwhy?"},
		{"reply without an author", "", "a line", "explain", "> a line\n\nexplain"},
		{"reply and nothing typed", "Anna", "look at this", "", "> Anna:\n> look at this"},
		{"quoted text that quotes", "Boris", "> earlier\nnow", "ok", "> Boris:\n> > earlier\n> now\n\nok"},
		{"blank quoted text is no reply", "Anna", "  \n ", "hi", "hi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := replyquote.Prompt(c.author, c.quoted, c.text); got != c.want {
				t.Fatalf("got\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestPromptCutsALongQuote(t *testing.T) {
	long := strings.Repeat("x", replyquote.MaxQuotedRunes+500)
	got := replyquote.Prompt("", long, "q")
	if n := len([]rune(got)); n > replyquote.MaxQuotedRunes+20 {
		t.Fatalf("a long quote was kept whole: %d runes", n)
	}
	if !strings.Contains(got, "…") || !strings.HasSuffix(got, "\n\nq") {
		t.Fatalf("cut quote: %q", got[len(got)-40:])
	}
}
