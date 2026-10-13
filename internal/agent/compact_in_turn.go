package agent

// Compaction inside the turn being answered.
//
// The automatic trigger folds earlier user turns and keeps the prompt being
// answered verbatim. A turn that is one prompt and dozens of tool steps has no
// earlier turn, so nothing was ever folded while its results grew past the
// model's window, and when the provider finally refused the request the turn
// ended on that refusal (issue #490).
//
// The in-turn fold takes the same road as every other compaction - the
// summarizer chain, the multi-pass fold, the hooks and the summary row - and
// differs in where it cuts. The boundary is a step of the turn: an assistant
// message with the tool results after it, so a batch of parallel calls is never
// split. The steps before it are summarized; the latest ones stay as they were.
// The prompt cannot be summarized away - a model that is not told what it was
// asked answers nothing - so the summary row, which becomes the first message
// of the replay window, begins with the prompt itself, verbatim, capped to a
// share of the window, and only then carries the summary.
//
// How many steps stay is decided by a budget, not only by inTurnKeepRecentSteps:
// the fold aims the next request at half of the share of the window that
// triggers compaction. Without that aim, a fold that kept four huge steps would
// leave the request over the threshold and fold again at the next step.

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	// inTurnKeepRecentSteps is the most steps of the turn an in-turn fold keeps
	// verbatim; the budget keeps fewer when they would not fit.
	inTurnKeepRecentSteps = 4
	// inTurnAimPercent is how much of the threshold share of the window an
	// in-turn fold aims the next request at. The fold aims at half of it, so the
	// turn has room for its next steps (and for the summary, which the budget
	// does not count) before the trigger fires again.
	inTurnAimPercent = 50
	// inTurnPromptSharePercent is the most of the window, in estimate tokens, the
	// prompt at the start of the summary row may take. A prompt that is a pasted
	// log must not be the thing that fills the window the fold exists to clear.
	inTurnPromptSharePercent = 10
	// inTurnPromptMinTokens is the least that cap is, so a small window still
	// carries a request of a few paragraphs whole.
	inTurnPromptMinTokens = 300
)

// inTurnPromptCutMarker replaces the middle of a prompt too long to be carried
// whole at the start of the summary row. The full text stays in the transcript.
const inTurnPromptCutMarker = "\n[... %d characters omitted from the middle of this request by Coddy to keep it within its share of the context window ...]\n"

// inTurnSplitRequest is everything the choice of an in-turn boundary reads.
type inTurnSplitRequest struct {
	// msgs is the whole transcript; visibleStart is where the replay window
	// begins in it. Every index is absolute.
	msgs         []llm.Message
	visibleStart int
	// projected is the window as the fold summarizes it and as the next request
	// replays it (result eviction applied), index-aligned with
	// msgs[visibleStart:]. The kept tail is measured on it.
	projected []llm.Message
	// keepSteps is the most steps kept (inTurnKeepRecentSteps).
	keepSteps int
	// recovery marks a request the provider refused: the floor is no step kept
	// instead of one (the steps of an answer the model is asked to go on from
	// stay all the same, continuedAnswerSteps), and a regular split is preferred
	// while the turn in progress fits.
	recovery bool
	// budget is how many tokens the kept steps may take: the target less the
	// request's overhead and the prompt at the start of the summary row. It may
	// be negative; then nothing fits and the floor is taken.
	budget int
	// regularBudget is the same for a regular split, which writes no prompt in
	// front of the summary.
	regularBudget int
	readsImages   bool
}

// inTurnSplit is where a fold cuts.
type inTurnSplit struct {
	// idx is the absolute index of the first message that stays verbatim; the
	// summary row is inserted there. len(msgs) means nothing stays.
	idx int
	// keptSteps is how many steps of the turn stay (step boundaries only).
	keptSteps int
	// regular marks the boundary of a user turn, the one the regular compaction
	// would use: the prompt stays an ordinary message and the row has no prompt
	// in front.
	regular bool
}

// chooseInTurnSplit picks the boundary of an in-turn fold. The candidates are
// the step boundaries from the cap down to the floor - one kept step for the
// automatic trigger, none for a recovery unless the turn ends on an answer the
// model is asked to go on from (continuedAnswerSteps); the first (the most
// steps) whose kept tail fits the budget wins, and when none fits the floor does.
//
// A recovery looks at the regular split first: when earlier turns can be folded
// and the turn in progress, from its prompt on, fits what is left, folding them
// keeps the prompt an ordinary message and the turn untouched. A turn with no
// step at all (its first request was the refused one) has nothing else to fold.
// ErrNothingToCompact is the answer when there is no boundary to cut at.
func chooseInTurnSplit(r inTurnSplitRequest) (inTurnSplit, error) {
	tail := func(idx int) int {
		return conversationTokens(r.projected[idx-r.visibleStart:], r.readsImages)
	}
	steps := session.TurnStepCount(r.msgs)
	hasSteps := steps > 0

	if r.recovery {
		// The regular candidate cuts at user turns only, and a follow-up the
		// user sent during this turn is not one: it would fold the opening prompt.
		last := len(r.msgs) - 1
		if open, ok := session.OpeningPromptIndex(r.msgs); ok {
			last = open
		}
		if idx, ok := session.CompactionSplitIndexUpTo(r.msgs, 1, last); ok && idx >= r.visibleStart {
			if !hasSteps || tail(idx) <= r.regularBudget {
				return inTurnSplit{idx: idx, regular: true}, nil
			}
		}
	}
	if !hasSteps {
		return inTurnSplit{}, ErrNothingToCompact
	}

	floor := 1
	if r.recovery {
		floor = continuedAnswerSteps(r.msgs)
	}
	var last inTurnSplit
	found := false
	// At most steps-1 can stay (the head holds a step at least, or nothing at
	// all in a recovery), whatever the cap says.
	for k := min(r.keepSteps, steps-1); k >= floor; k-- {
		idx, ok := session.TurnStepSplitIndex(r.msgs, k)
		if !ok {
			continue
		}
		last, found = inTurnSplit{idx: idx, keptSteps: k}, true
		if tail(idx) <= r.budget {
			return last, nil
		}
	}
	if !found {
		return inTurnSplit{}, ErrNothingToCompact
	}
	// None fits: the loop ended on the floor.
	return last, nil
}

// inTurnTargetTokens is the size an in-turn fold aims the next request at: the
// share of the limit that triggers compaction, halved (inTurnAimPercent). limit
// is the window for the automatic trigger and, for a recovery, the limit the
// refusal revealed in Coddy's estimate units.
func inTurnTargetTokens(limit, thresholdPercent int) int {
	if limit <= 0 {
		return 0
	}
	return limit * thresholdPercent * inTurnAimPercent / 10000
}

// inTurnPromptPrefix is what goes in front of the summary: the opening prompt,
// verbatim when it fits its share of the limit, else cut in the middle with a
// marker of its own that says so, so both ends of it survive.
func inTurnPromptPrefix(prompt string, limit int) string {
	maxTokens := max(inTurnPromptMinTokens, limit*inTurnPromptSharePercent/100)
	if session.EstimateContextTokens(prompt) <= maxTokens {
		return prompt
	}
	return elideToTokensMarked(prompt, maxTokens, inTurnPromptCutMarker)
}

// promptShareLimit is the limit the share of the prefix is taken of: the limit
// the next request has to fit under, else the session's window.
func (a *Agent) promptShareLimit(limitTokens int) int {
	limit := limitTokens
	if limit <= 0 {
		limit, _ = a.contextWindow()
	}
	if limit <= 0 {
		// A session with no model entry has no window to go by.
		limit = config.DefaultContextWindowTokens
	}
	return limit
}

// inTurnPrefix is the prefix of the summary row of an in-turn fold that cuts at
// split, and ok says whether the row has one: it has when the turn's opening
// prompt lies before the split and carries text.
func (a *Agent) inTurnPrefix(msgs []llm.Message, split, limit int) (prefix string, ok bool) {
	open, found := session.OpeningPromptIndex(msgs)
	if !found || open >= split || strings.TrimSpace(msgs[open].Content) == "" {
		return "", false
	}
	return inTurnPromptPrefix(msgs[open].Content, limit), true
}

// withInTurnSummaryInstructions tells the summarizer the transcript it reads
// stops in the middle of the work, and that the message the turn answers is
// kept verbatim in front of its summary.
func withInTurnSummaryInstructions(instructions string) string {
	note := "The transcript ends in the middle of the agent's work on the message this turn started with. " +
		"That message stays verbatim in front of your summary, so do not repeat it. " +
		"Summarize what was done for it so far, what it found, the files touched, and what remains to be done."
	if instructions == "" {
		return note
	}
	return instructions + "\n\n" + note
}

// requestOverhead is what a request carries besides the conversation - the
// system message, the tool definitions, the rules - read off the last context
// estimate. It does not move with a fold.
func (a *Agent) requestOverhead() int {
	if rs, ok := a.state.(rulesState); ok {
		if b := rs.GetLastContextBreakdown(); b != nil && b.EstimatedTotal > b.Conversation {
			return b.EstimatedTotal - b.Conversation
		}
	}
	return 0
}

// providerScale is how many tokens the provider counted for each token Coddy
// estimated, read off the last request that came back with a count: the
// provider's input tokens over the estimate of that same prompt (the context
// breakdown's ProviderInputTokens and ProviderEstimateTokens). It is 1 when
// there is no such pair, which is the case before the first response and after a
// compaction, and when either number is not positive. The trigger measures the
// context by the provider's count, so an in-turn fold scales its aim by it.
func (a *Agent) providerScale() float64 {
	rs, ok := a.state.(rulesState)
	if !ok {
		return 1
	}
	b := rs.GetLastContextBreakdown()
	if b == nil || b.ProviderInputTokens <= 0 || b.ProviderEstimateTokens <= 0 {
		return 1
	}
	return float64(b.ProviderInputTokens) / float64(b.ProviderEstimateTokens)
}

// inTurnPlan is the boundary of an in-turn compaction and what goes in front of
// the summary written for it.
type inTurnPlan struct {
	inTurnSplit
	// prompt is the text that begins the summary row of a step boundary, already
	// capped; empty for a regular boundary and for a history with no prompt.
	prompt string
}

// planInTurn decides where an in-turn compaction cuts, and the prompt that goes
// in front of its summary. opts.LimitTokens is the limit the next request has to
// fit under; unset, it is the session's window.
func (a *Agent) planInTurn(opts CompactOptions, msgs []llm.Message, visibleStart int, projected []llm.Message) (*inTurnPlan, error) {
	comp := &a.cfg.Compaction
	limit := a.promptShareLimit(opts.LimitTokens)
	target := inTurnTargetTokens(limit, comp.EffectiveThresholdPercent())
	if !opts.Recovery {
		// The trigger counts the provider's tokens and the budget below counts
		// Coddy's estimate: a provider that counts denser would otherwise see a
		// kept tail that lands over the threshold and folds again at the next
		// step. A recovery's limit is already in estimate units.
		if s := a.providerScale(); s > 1 {
			target = int(float64(target) / s)
		}
	}
	overhead := a.requestOverhead()

	prefixTokens := 0
	if prefix, ok := a.inTurnPrefix(msgs, len(msgs), limit); ok {
		prefixTokens = session.EstimateContextTokens(session.NewInTurnCompactionSummaryMessage(prefix, "", "").Content)
	}
	regularPreamble := session.EstimateContextTokens(session.NewCompactionSummaryMessage("", "").Content)

	choice, err := chooseInTurnSplit(inTurnSplitRequest{
		msgs:          msgs,
		visibleStart:  visibleStart,
		projected:     projected,
		keepSteps:     inTurnKeepRecentSteps,
		recovery:      opts.Recovery,
		budget:        target - overhead - prefixTokens,
		regularBudget: target - overhead - regularPreamble,
		readsImages:   a.modelReadsImages(),
	})
	if err != nil {
		return nil, err
	}
	plan := &inTurnPlan{inTurnSplit: choice}
	if !choice.regular {
		plan.prompt, _ = a.inTurnPrefix(msgs, choice.idx, limit)
	}
	return plan, nil
}

// inTurnRequestTokens is the size of the next request after a fold, in the
// provider's tokens as far as they are known: what every request carries besides
// the conversation, plus the window the fold left, scaled by scale (the
// providerScale read before the fold, since a compaction clears it).
func (a *Agent) inTurnRequestTokens(scale float64) int {
	window := session.MessagesForLLM(a.state.GetMessages())
	n := a.requestOverhead() + conversationTokens(a.prunedForLLM(window), a.modelReadsImages())
	return int(float64(n) * scale)
}

// continuedAnswerSteps is how many steps at the end of a turn belong to an answer
// the output limit cut off that the model is asked to go on from: every part of
// it (session.ContinuedAnswer) and the empty steps between and after them
// (session.FollowsTruncated). A fold keeps them verbatim, the way a page of the
// history never splits them: the model goes on from the last word of the text,
// and the answer a turn hands over joins the parts, which a summary row between
// them would cut. When the turn holds nothing else, nothing can be folded and
// the refusal stands. 0 when the turn does not end on such a text.
func continuedAnswerSteps(msgs []llm.Message) int {
	n, end := 0, len(msgs)
	for end > 0 && session.FollowsTruncated(msgs[:end]) {
		for {
			end--
			if !isPlanDocumentRow(msgs[end]) {
				n++
			}
			if msgs[end].Truncated {
				break
			}
		}
	}
	return n
}

// isPlanDocumentRow is the plan document snapshot an assistant-role row with no
// text, no call and no reasoning carries: the UI shows it, and it is no step of
// the turn (session.TurnStepSplitIndex).
func isPlanDocumentRow(m llm.Message) bool {
	return m.PlanDocument != nil && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && strings.TrimSpace(m.Reasoning) == ""
}
