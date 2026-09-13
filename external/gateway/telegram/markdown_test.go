//go:build gateway || gateway.telegram

package telegram

// Edge cases of the outbound rendering step. The happy path - an answer
// reaching the chat in Telegram's syntax without a word of Telegram in the
// prompt - is features/gateway_session_identity.feature.

import "testing"

func TestMdToTelegramLeavesCodeBlocksAlone(t *testing.T) {
	in := "Before **bold**\n\n```go\nx := **p\n// # not a heading\n* not a bullet\n```\n\nAfter **bold**"
	got := mdToTelegram(in)
	want := "Before *bold*\n\n```go\nx := **p\n// # not a heading\n* not a bullet\n```\n\nAfter *bold*"
	if got != want {
		t.Fatalf("mdToTelegram(%q) =\n%q\nwant\n%q", in, got, want)
	}
}

func TestMdToTelegramConversions(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"heading", "## Findings", "*Findings*"},
		{"deep heading", "###### Small", "*Small*"},
		{"double star", "a **b** c", "a *b* c"},
		{"double underscore", "a __b__ c", "a _b_ c"},
		{"bullet", "* one\n* two", "• one\n• two"},
		{"indented bullet", "- top\n  * nested", "- top\n  • nested"},
		{"rule", "a\n---\nb", "a\n────────────────\nb"},
		{"inline code untouched", "call `x_y` now", "call `x_y` now"},
		{"a hash that is not a heading", "issue #12 is open", "issue #12 is open"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mdToTelegram(c.in); got != c.want {
				t.Fatalf("mdToTelegram(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMdToTelegramFlattensTables(t *testing.T) {
	in := "| Key | Value |\n|-----|-------|\n| a   | 1     |"
	got := mdToTelegram(in)
	want := "Key  │  Value\na  │  1"
	if got != want {
		t.Fatalf("mdToTelegram(table) = %q, want %q", got, want)
	}
}

// An unclosed fence is what a truncated answer looks like. Nothing after it may
// be rewritten either: the model was writing code when it stopped.
func TestMdToTelegramKeepsAnUnclosedCodeBlock(t *testing.T) {
	in := "look:\n\n```go\nx := **p"
	if got := mdToTelegram(in); got != in {
		t.Fatalf("mdToTelegram(unclosed) = %q, want %q", got, in)
	}
}

// The live preview is sent with no parse mode, so a marker there is punctuation
// on the reader's screen rather than formatting: emphasis is dropped instead.
func TestMdToPlainPreviewDropsEmphasis(t *testing.T) {
	in := "## Findings\n\n**two** of them"
	want := "Findings\n\ntwo of them"
	if got := mdToPlainPreview(in); got != want {
		t.Fatalf("mdToPlainPreview(%q) = %q, want %q", in, got, want)
	}
}

func TestBuildStreamPreviewRendersTheAccumulatedText(t *testing.T) {
	if got := buildStreamPreview("## Findings", ""); got != "Findings…" {
		t.Fatalf("preview without a tool = %q, want %q", got, "Findings…")
	}
	if got := buildStreamPreview("", "read"); got != "⚙️ read…" {
		t.Fatalf("preview with no text = %q", got)
	}
	if got := buildStreamPreview("## Findings", "read"); got != "Findings\n\n⚙️ read…" {
		t.Fatalf("preview with a tool = %q", got)
	}
}

// Inline code carries the same weight as a fenced block: an identifier in
// backticks is what the model meant, not emphasis to rewrite.
func TestMdToTelegramLeavesInlineCodeAlone(t *testing.T) {
	in := "Use `**p` for a pointer and `__name__` for the dunder, then **stress** it"
	want := "Use `**p` for a pointer and `__name__` for the dunder, then *stress* it"
	if got := mdToTelegram(in); got != want {
		t.Fatalf("mdToTelegram(%q) = %q, want %q", in, got, want)
	}
}

// A fence longer than three characters is how a model quotes Markdown that
// contains a fence of its own; closing on the inner one would spill the rest
// of the answer into the block and rewrite what followed as prose.
func TestMdToTelegramHonoursTheOpeningFenceLength(t *testing.T) {
	in := "A fence:\n\n````md\n```\n**inner**\n```\n````\n\ndone **after**"
	want := "A fence:\n\n````md\n```\n**inner**\n```\n````\n\ndone *after*"
	if got := mdToTelegram(in); got != want {
		t.Fatalf("mdToTelegram(long fence) = %q, want %q", got, want)
	}
}

func TestMdToTelegramHonoursTildeFences(t *testing.T) {
	in := "tilde:\n\n~~~go\nx := __p__\n~~~\n\nafter **bold**"
	want := "tilde:\n\n~~~go\nx := __p__\n~~~\n\nafter *bold*"
	if got := mdToTelegram(in); got != want {
		t.Fatalf("mdToTelegram(tilde fence) = %q, want %q", got, want)
	}
}
