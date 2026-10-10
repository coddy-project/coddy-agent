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
// How many steps stay is decided by a budget, not only by compaction.in_turn.
// keep_recent_steps: the fold aims the next request at half of the share of the
// window that triggers compaction. Without that aim, a fold that kept four huge
// steps would leave the request over the threshold and fold again at the next
// step.

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
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
	// keepSteps is compaction.in_turn.keep_recent_steps: the most steps kept.
	keepSteps int
	// recovery marks a request the provider refused: the floor is no step kept
	// instead of one, and a regular split is preferred while the turn in
	// progress fits.
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
// automatic trigger, none for a recovery; the first (the most steps) whose kept
// tail fits the budget wins, and when none fits the floor does.
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
		if idx, ok := session.CompactionSplitIndex(r.msgs, 1); ok && idx >= r.visibleStart {
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
		floor = 0
	}
	var last inTurnSplit
	found := false
	// At most steps-1 can stay (the head holds a step at least, or nothing at
	// all in a recovery), whatever the cap says: a cap of a million is no reason
	// to walk a million boundaries.
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

// inTurnPromptPrefix is the prompt as it goes in front of the summary: verbatim
// when it fits its share of the limit, else cut in the middle with a marker of
// its own that says so.
func inTurnPromptPrefix(prompt string, limit int) string {
	maxTokens := max(inTurnPromptMinTokens, limit*inTurnPromptSharePercent/100)
	if session.EstimateContextTokens(prompt) <= maxTokens {
		return prompt
	}
	return elideToTokensMarked(prompt, maxTokens, inTurnPromptCutMarker)
}

// withInTurnSummaryInstructions tells the summarizer the transcript it reads
// stops in the middle of the work, and that the request it serves is kept
// verbatim in front of its summary.
func withInTurnSummaryInstructions(instructions string) string {
	note := "The transcript ends in the middle of the agent's work on the user's latest request. " +
		"That request stays verbatim in front of your summary, so do not repeat it. " +
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
	limit := opts.LimitTokens
	if limit <= 0 {
		limit, _ = a.contextWindow()
	}
	if limit <= 0 {
		// A session with no model entry has no window to go by.
		limit = config.DefaultContextWindowTokens
	}
	target := inTurnTargetTokens(limit, comp.EffectiveThresholdPercent())
	overhead := a.requestOverhead()

	prompt, hasPrompt := session.TurnPrompt(msgs)
	prefix := ""
	prefixTokens := 0
	if hasPrompt {
		prefix = inTurnPromptPrefix(prompt, limit)
		prefixTokens = session.EstimateContextTokens(session.NewInTurnCompactionSummaryMessage(prefix, "", "").Content)
	}
	regularPreamble := session.EstimateContextTokens(session.NewCompactionSummaryMessage("", "").Content)

	choice, err := chooseInTurnSplit(inTurnSplitRequest{
		msgs:          msgs,
		visibleStart:  visibleStart,
		projected:     projected,
		keepSteps:     comp.InTurn.EffectiveKeepRecentSteps(),
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
		plan.prompt = prefix
	}
	return plan, nil
}
