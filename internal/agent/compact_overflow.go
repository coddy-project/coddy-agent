package agent

// Recovery from a request the provider refused as larger than the model's
// window: compact the turn (compact_in_turn.go) and ask the same step again,
// once. The loop's side of it is the overflow branch of runReActLoop (react.go);
// this file is what that branch calls.

import (
	"context"
	"errors"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

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
