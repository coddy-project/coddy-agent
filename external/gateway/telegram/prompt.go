//go:build gateway || gateway.telegram

package telegram

// What this adapter tells the model about answering into a Telegram chat. It
// is a system prompt block the gateway contributes for the length of one turn
// (session.PromptRunOpts.SurfaceSystemPrompt), not a line prepended to what
// the person wrote: the transcript keeps the conversation, and the session
// reads the same whether the next turn comes from a chat, a browser or a
// terminal.
//
// It is where a messenger's quirks belong. Telegram's are about syntax, so
// this is mostly a format; the next integration will have others, and will say
// them here in its own file. `markdown.go` still renders the answer on its way
// out - a model does not always comply, and a chat that shows raw `##` is a
// worse answer than one the renderer quietly fixed.
//
// A turn from another surface on the same session carries a different prompt
// prefix and therefore cannot reuse this one's cached prefix. That is the
// deliberate cost of letting each surface speak for itself.

// telegramLegacyGuidance is sent when the bot formats with Telegram's legacy
// Markdown, which is a narrow subset: the model is asked for what that subset
// can carry. Wrapping identifiers in backticks matters more than it looks -
// an underscore in `foo_bar` outside code opens italics and makes Telegram
// reject the whole message, which costs the answer its formatting.
const telegramLegacyGuidance = "## Answering in a Telegram chat\n\n" +
	"This conversation reaches the person through a Telegram chat, which renders a narrow subset of Markdown. Write the answer for that subset.\n\n" +
	"- `*bold*` with single asterisks for emphasis, `_italic_` with underscores for a lighter one; `**double asterisks**` are not bold there;\n" +
	"- `` `inline code` `` and ```` ```language ```` fenced blocks, both of which render properly;\n" +
	"- no `#` headings: a short `*bold line*` is how a section is titled;\n" +
	"- no tables: a table renders as a wall of pipes, so use a bullet list, or a fenced block when the columns really matter;\n" +
	"- `-` or `•` for bullets, never `*`, which the chat reads as emphasis;\n" +
	"- put every identifier, path, filename, flag and command in backticks. Outside code an `_` opens italics, so `foo_bar` written bare costs the message its formatting;\n" +
	"- a chat is a narrow column on a phone: answer in a few short paragraphs, and put the thing that was asked for first.\n\n" +
	"None of this is visible to the person, and nothing about it belongs in the answer itself."

// telegramRichGuidance is sent when rich_messages is on: the answer goes out
// as the model wrote it, so the only thing worth saying is what the chat can
// now render and how much of it a phone screen wants.
const telegramRichGuidance = "## Answering in a Telegram chat\n\n" +
	"This conversation reaches the person through a Telegram chat that renders GitHub-flavoured Markdown in full: headings, tables, task lists, fenced code and inline code all display correctly, so write the answer the way you normally would.\n\n" +
	"A chat is still a narrow column on a phone. Keep it to a few short paragraphs, put the thing that was asked for first, and reach for a table only when the columns carry the point.\n\n" +
	"None of this is visible to the person, and nothing about it belongs in the answer itself."

// surfaceSystemPrompt is what the gateway hands the session for one turn.
func surfaceSystemPrompt(rich bool) string {
	if rich {
		return telegramRichGuidance
	}
	return telegramLegacyGuidance
}
