//go:build gateway || gateway.pachca

package pachca

import "github.com/EvilFreelancer/coddy-agent/external/gateway/replyquote"

// What this adapter tells the model about answering into a Pachca chat: a
// system prompt block for the length of one turn
// (session.PromptRunOpts.SurfaceSystemPrompt), never a line added to what the
// person wrote, so the transcript reads the same from every surface.
// markdown.go still renders the answer on its way out.

// pachcaGuidance describes the Markdown subset Pachca renders.
const pachcaGuidance = "## Answering in a Pachca chat\n\n" +
	"This conversation reaches the person through Pachca, a corporate messenger that renders a subset of Markdown. Write the answer for that subset.\n\n" +
	"- `**bold**`, `*italic*`, `~~strikethrough~~`, `[links](https://example.com)` and `` `inline code` `` render as usual;\n" +
	"- fenced code blocks render when each fence stands on a line of its own; name the language after the opening fence, and use `text` for a block that is not code;\n" +
	"- a `# heading` line is shown as a bold line, so a short heading is fine;\n" +
	"- lists are not rendered: `-` and `1.` stay as typed, which still reads well, so keep list items short;\n" +
	"- tables are not rendered: prefer a short list, or a fenced `text` block when the columns really matter;\n" +
	"- a chat is a narrow column: answer in a few short paragraphs and put the thing that was asked for first.\n\n" +
	replyquote.Guidance + "\n\n" +
	"None of this is visible to the person, and nothing about it belongs in the answer itself."

// surfaceSystemPrompt is what the gateway hands the session for one turn.
func surfaceSystemPrompt() string { return pachcaGuidance }
