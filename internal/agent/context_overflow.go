package agent

// The end of a turn whose request the provider refused as larger than the
// model's context window.
//
// A refusal like that used to surface as the provider's own error, wrapped in
// "LLM error:" - "404 Not Found: Context limit is 49152 tokens; prompt=51402
// leaves 0 output tokens" - and said nothing about what had happened to the
// session or what to do about it. The turn still ends there, because nothing in
// the loop makes a request smaller in the middle of a turn, but the person
// reading it is told what the error means and where the numbers stand.

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// contextOverflowError is the error a turn ends with when streamErr is the
// provider refusing the request for its size. estimated is the prompt estimate
// taken when the request was built (0 when there was none). The provider's own
// error stays in the chain: surfaces read its status from it, and its words are
// the last thing the message says.
func (a *Agent) contextOverflowError(streamErr error, estimated int) error {
	window, source := a.contextWindow()
	detail := llm.OverflowDetailOf(streamErr)
	// The numbers side by side are what tells a window set too high from an
	// estimate that fell short, so they are logged whatever the message shows.
	a.log.Warn("provider refused the request as larger than the context window",
		"estimatedTokens", estimated, "contextWindow", window, "windowSource", source,
		"providerTokens", detail.Prompt, "providerLimit", detail.Limit, "error", streamErr)
	return fmt.Errorf("%s: %w", contextOverflowMessage(estimated, window, source, detail), streamErr)
}

// contextOverflowMessage says that the request did not fit, what Coddy and the
// provider each know about the sizes, and what the person can do. A size that
// is not known is left out rather than shown as zero.
func contextOverflowMessage(estimated, window int, source string, detail llm.OverflowDetail) string {
	var b strings.Builder
	b.WriteString("context window exceeded: the provider refused the request because it does not fit the model's context window")

	var facts []string
	if estimated > 0 {
		facts = append(facts, fmt.Sprintf("Coddy estimated about %d tokens", estimated))
	}
	switch {
	case window > 0 && source == session.ContextWindowDefault:
		facts = append(facts, fmt.Sprintf("the window is assumed to be %d tokens", window))
	case window > 0:
		facts = append(facts, fmt.Sprintf("the window is %d tokens", window))
	}
	switch {
	case detail.Prompt > 0 && detail.Limit > 0:
		facts = append(facts, fmt.Sprintf("the provider counted %d tokens against a limit of %d", detail.Prompt, detail.Limit))
	case detail.Limit > 0:
		facts = append(facts, fmt.Sprintf("the provider's limit is %d tokens", detail.Limit))
	}
	if len(facts) > 0 {
		b.WriteString(" (" + strings.Join(facts, "; ") + ")")
	}

	b.WriteString("; run /compact, start a new session or split the task into smaller steps")

	// A window set above what the backend serves is the commonest reason the
	// estimate looked safe: compaction triggers against the configured number.
	switch {
	case detail.Limit > 0 && (window == 0 || detail.Limit < window):
		fmt.Fprintf(&b, "; set models[].max_context_tokens to %d so compaction starts earlier", detail.Limit)
	case window > 0 && source == session.ContextWindowDefault:
		b.WriteString("; set models[].max_context_tokens to the window the provider serves so compaction starts earlier")
	}
	return b.String()
}
