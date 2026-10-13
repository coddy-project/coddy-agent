package agent

// A request the provider refused as larger than the model's context window:
// the recovery that compacts the turn and asks the same step again, once, and
// the end of the turn when that does not help.
//
// A refusal like that used to surface as the provider's own error, wrapped in
// "LLM error:" - "404 Not Found: Context limit is 49152 tokens; prompt=51402
// leaves 0 output tokens" - and said nothing about what had happened to the
// session or what to do about it. The loop now compacts the turn once and asks
// again (compact_in_turn.go); the turn ends here when that is switched off, when
// there was nothing to fold, or when the smaller request was refused too, and
// the person reading it is told what the error means, where the numbers stand
// and whether a compaction was already tried.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// contextOverflowError is the error a turn ends with when streamErr is the
// provider refusing the request for its size. estimated is the prompt estimate
// taken when the request was built (0 when there was none). The provider's own
// error stays in the chain: surfaces read its status from it, and its words are
// the last thing the message says. compacted says the turn was already compacted
// for this request and the smaller one was refused as well.
func (a *Agent) contextOverflowError(streamErr error, estimated int, compacted bool) error {
	window, source := a.contextWindow()
	detail := llm.OverflowDetailOf(streamErr)
	// The numbers side by side are what tells a window set too high from an
	// estimate that fell short, so they are logged whatever the message shows.
	a.log.Warn("provider refused the request as larger than the context window",
		"estimatedTokens", estimated, "contextWindow", window, "windowSource", source,
		"providerTokens", detail.Prompt, "providerLimit", detail.Limit, "compactedFirst", compacted, "error", streamErr)
	return fmt.Errorf("%s: %w", contextOverflowMessage(estimated, window, source, detail, compacted), streamErr)
}

// contextOverflowMessage says that the request did not fit, what Coddy and the
// provider each know about the sizes, whether the turn was already compacted
// once for it, and what the person can do. A size that is not known is left out
// rather than shown as zero.
func contextOverflowMessage(estimated, window int, source string, detail llm.OverflowDetail, compacted bool) string {
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

	if compacted {
		b.WriteString("; Coddy compacted this turn and asked again, but the provider refused the smaller request too")
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

// maxOverflowRecoveries is how many times one step compacts the turn and asks
// again after the provider refused its request as too large. A second refusal of
// the same step ends the turn: the compaction already did what it can.
const maxOverflowRecoveries = 1

// recoveryLimitTokens is the limit a request the provider refused has to fit
// under, in the units of Coddy's own estimate. The window is the start. When the
// refusal said the provider's limit, that replaces it if lower, scaled by how
// much denser the provider counted the refused request than Coddy did (their
// prompt over the estimate sent, never below 1, so a provider that counts fewer
// tokens than Coddy is not believed to). And the refused request is over the
// limit whatever the window says, so its own estimate is a ceiling too. 0 means
// nothing is known.
func recoveryLimitTokens(window int, detail llm.OverflowDetail, estimateAtSend int) int {
	limit := window
	if detail.Limit > 0 {
		scaled := detail.Limit
		if detail.Prompt > 0 && estimateAtSend > 0 && detail.Prompt > estimateAtSend {
			scaled = int(int64(detail.Limit) * int64(estimateAtSend) / int64(detail.Prompt))
		}
		if limit <= 0 || scaled < limit {
			limit = scaled
		}
	}
	if estimateAtSend > 0 && (limit <= 0 || estimateAtSend < limit) {
		limit = estimateAtSend
	}
	return limit
}

// compactForOverflow compacts the turn after the provider refused the request
// of the step as larger than its window, so the step can be asked again. The
// limit the new request has to fit under is the one the refusal revealed
// (recoveryLimitTokens), in Coddy's units.
func (a *Agent) compactForOverflow(ctx context.Context, streamErr error, estimateAtSend int) (*CompactionResult, int, error) {
	window, _ := a.contextWindow()
	limit := recoveryLimitTokens(window, llm.OverflowDetailOf(streamErr), estimateAtSend)
	res, err := a.CompactSession(ctx, CompactOptions{InTurn: true, Recovery: true, LimitTokens: limit})
	return res, limit, err
}

// recoverFromContextOverflow runs the compaction a refused request asks for and
// says what came of it. It reports true when the history was folded and the
// step can be asked again; the reason it did not is logged either way.
func (a *Agent) recoverFromContextOverflow(ctx context.Context, streamErr error, estimateAtSend int) bool {
	detail := llm.OverflowDetailOf(streamErr)
	res, limit, err := a.compactForOverflow(ctx, streamErr, estimateAtSend)
	if err != nil {
		switch {
		case errors.Is(err, ErrNothingToCompact):
			a.log.Info("provider refused the request as larger than the context window; nothing in the turn can be folded",
				"estimatedTokens", estimateAtSend, "providerTokens", detail.Prompt, "providerLimit", detail.Limit)
		case ctx.Err() != nil:
			a.log.Info("compaction after a refused request interrupted", "error", err)
		default:
			a.log.Warn("provider refused the request as larger than the context window; compacting the turn did not work",
				"estimatedTokens", estimateAtSend, "providerTokens", detail.Prompt, "providerLimit", detail.Limit, "error", err)
		}
		return false
	}
	a.log.Warn("provider refused the request as larger than the context window; compacted the turn and asking again",
		"estimatedTokens", estimateAtSend, "providerTokens", detail.Prompt, "providerLimit", detail.Limit,
		"limitTokens", limit, "inTurn", res.InTurn, "keptSteps", res.KeptSteps,
		"compactedMessages", res.CompactedMessages, "keptMessages", res.KeptMessages)
	if st := sessionStatePtr(a.state); st != nil {
		st.AppendUILogNotice(session.CountUserTurns(a.state.GetMessages()), fmt.Sprintf(
			"The provider refused the request as larger than its context window. Coddy compacted the turn (%d message(s) summarized, %d kept verbatim) and asked again.",
			res.CompactedMessages, res.KeptMessages))
	}
	return true
}
