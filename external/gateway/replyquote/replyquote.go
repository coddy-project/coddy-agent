//go:build gateway || gateway.telegram || gateway.pachca

// Package replyquote is how a messenger reply reaches the agent. A person who
// replies to a message in a chat asks about that message, so the session
// receives it as a Markdown quote in front of what they typed: the first line
// names the author, the rest is the message. The quote is part of the user
// message and stays in the transcript, which then reads the way the chat did.
package replyquote

import "strings"

// MaxQuotedRunes is how much of a replied-to message is quoted.
const MaxQuotedRunes = 3000

// Guidance is the paragraph an adapter's surface prompt carries about it.
const Guidance = "When the person's message starts with a quote - `>` lines, the first of them naming the author - they replied to that message in the chat: what they write is about the quoted message, and a quote with nothing under it asks you to deal with that message. The quoted message may be one of your own earlier answers."

// Prompt is the text the session receives: the quoted message when the
// person replied to one, then what they wrote. A reply to a message with no
// text quotes nothing.
func Prompt(author, quoted, text string) string {
	quoted = strings.TrimSpace(strings.ReplaceAll(quoted, "\r\n", "\n"))
	text = strings.TrimSpace(text)
	if quoted == "" {
		return text
	}
	if r := []rune(quoted); len(r) > MaxQuotedRunes {
		quoted = strings.TrimSpace(string(r[:MaxQuotedRunes])) + "…"
	}
	var b strings.Builder
	if author = strings.TrimSpace(author); author != "" {
		b.WriteString("> " + author + ":\n")
	}
	for i, line := range strings.Split(quoted, "\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		if line == "" {
			b.WriteString(">")
			continue
		}
		b.WriteString("> " + line)
	}
	if text != "" {
		b.WriteString("\n\n" + text)
	}
	return b.String()
}
