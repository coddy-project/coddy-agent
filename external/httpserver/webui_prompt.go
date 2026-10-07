//go:build http

package httpserver

import (
	"encoding/json"
	"strings"
)

// What the bundled web UI tells the model about answering into it. The SPA
// names itself in the request (`metadata.surface: "webui"`), and the turn then
// carries this block as its surface system prompt
// (session.PromptRunOpts.SurfaceSystemPrompt), the way a messenger gateway
// describes the syntax its chat renders. An API client, the remote console and
// anything else that calls /v1/responses without naming the web UI gets
// nothing: they show the answer as text, where a diagram is only its source.
//
// The web UI draws a `mermaid` or `svg` fence as a picture and typesets LaTeX
// (docs/surfaces/web-ui.md, "Diagrams and formulas"). Without being told, a
// model reaches for an ASCII drawing or a table where a diagram would do.
//
// A turn from another surface on the same session carries a different prompt
// prefix and cannot reuse this one's cached prefix: the deliberate cost of
// letting each surface speak for itself.

// webUISurface is the value of `metadata.surface` the SPA sends with a turn.
const webUISurface = "webui"

const webUISurfacePrompt = "## Answering in the Coddy web UI\n\n" +
	"This conversation is read in Coddy's web UI, which draws some Markdown as pictures. When a diagram, a chart or a formula says something better than prose, a list or a table would, use one; for everything else plain Markdown stays the right answer.\n\n" +
	"- A fenced block labelled `mermaid` is drawn as a diagram: flowcharts, sequence, class, state and ER diagrams, Gantt charts, pie charts, mindmaps, timelines and the other types Mermaid draws. It suits architecture, flows, dependencies, plans and proportions. Keep node labels short and put a label holding punctuation in double quotes; a diagram that does not parse is shown as its source with the error.\n" +
	"- A fenced block labelled `svg` holding a whole `<svg>` document with a `viewBox` is drawn as an image, for a small illustration Mermaid cannot express. Scripts, event handlers and external resources in it do nothing.\n" +
	"- LaTeX is typeset: `$...$` inline, `$$...$$` on lines of their own for a display formula; `\\(...\\)`, `\\[...\\]` and a fenced block labelled `math` work too. A dollar sign with a space inside it or a letter or digit right outside it stays a literal dollar, so prices and shell variables are safe, but put code that holds dollars in backticks anyway.\n" +
	"- Diagrams take their colours from the reader's theme, light or dark: leave colours out unless they carry meaning.\n" +
	"- The reader can switch every picture back to its source, copy it and download it, so the source does not need repeating in prose.\n\n" +
	"None of this is visible to the person, and nothing about it belongs in the answer itself."

// surfacePromptFromHTTP returns the surface system prompt a request's metadata
// asks for, or "" when it names no surface this server knows.
func surfacePromptFromHTTP(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m struct {
		Surface string `json:"surface"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(m.Surface), webUISurface) {
		return webUISurfacePrompt
	}
	return ""
}
