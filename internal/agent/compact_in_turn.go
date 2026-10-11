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
// share of the window, and only then carries the summary. The follow-ups the
// user queued while the turn was being worked on go with it (inTurnPromptPrefix).
//
// How many steps stay is decided by a budget, not only by compaction.in_turn.
// keep_recent_steps: the fold aims the next request at half of the share of the
// window that triggers compaction. Without that aim, a fold that kept four huge
// steps would leave the request over the threshold and fold again at the next
// step.

import (
	"fmt"
	"math"
	"strings"

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

// inTurnFollowUpIntro introduces each follow-up the user sent during the turn in
// the prefix of the summary row.
const inTurnFollowUpIntro = "Follow-up the user sent while this request was being worked on:"

// inTurnSplitRequest is everything the choice of an in-turn boundary reads.
type inTurnSplitRequest struct {
	// msgs is the whole transcript; visibleStart is where the replay window
	// begins in it. Every index is absolute.
	msgs         []llm.Message
	visibleStart int
	// anchor is the record of the message the turn opened with; the zero value
	// is none (session.OpeningPromptIndex says what then).
	anchor session.TurnAnchor
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
	steps := session.TurnStepCount(r.msgs, r.anchor)
	hasSteps := steps > 0

	if r.recovery {
		// The regular candidate cuts at user turns only, and a follow-up the
		// user sent during this turn is not one: it would fold the opening prompt.
		last := len(r.msgs) - 1
		if open, ok := session.OpeningPromptIndex(r.msgs, r.anchor); ok {
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
		floor = 0
	}
	var last inTurnSplit
	found := false
	// At most steps-1 can stay (the head holds a step at least, or nothing at
	// all in a recovery), whatever the cap says: a cap of a million is no reason
	// to walk a million boundaries.
	for k := min(r.keepSteps, steps-1); k >= floor; k-- {
		idx, ok := session.TurnStepSplitIndex(r.msgs, k, r.anchor)
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

// inTurnPromptPrefix is what goes in front of the summary: the opening prompt
// and then each follow-up the user sent during the turn that the fold takes
// away, every one introduced by a line of its own (inTurnFollowUpIntro). It is
// verbatim when it fits its share of the limit, else cut in the middle with a
// marker of its own that says so; the share is the same one for the whole, so a
// long prompt and a long run of follow-ups together never take more than a tenth
// of the window, and the cut keeps both ends of it - the head of the prompt and
// the latest follow-up.
//
// A follow-up is the user's own words correcting or extending the request, and
// usually short. A summarizer that paraphrases it can drop the correction, so it
// is carried like the request is. A Stop-hook follow-up is hook output, not the
// user's text, and is summarized with the rest of the head; session.TurnFollowUps
// leaves it out.
func inTurnPromptPrefix(prompt string, followUps []string, limit int) string {
	text := prompt
	if len(followUps) > 0 {
		var b strings.Builder
		b.WriteString(prompt)
		for _, f := range followUps {
			b.WriteString("\n\n" + inTurnFollowUpIntro + "\n")
			b.WriteString(f)
		}
		text = b.String()
	}
	maxTokens := max(inTurnPromptMinTokens, limit*inTurnPromptSharePercent/100)
	if session.EstimateContextTokens(text) <= maxTokens {
		return text
	}
	return elideToTokensMarked(text, maxTokens, inTurnPromptCutMarker)
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

// inTurnPrefix is the prefix of the summary row of a compaction that cuts at
// split while a turn is in progress, and ok says whether the row has one: it has
// when the turn's opening prompt lies before the split (in the head, or hidden
// behind the summary row an earlier fold wrote) and carries text. The follow-ups
// are the ones the user queued between the prompt and the split; those in the kept
// tail stay where they are, so nothing is written twice. source is who wrote the
// opening message - the user, the goal supervisor or a finished background task -
// which the row says in front of its summary (session.TurnSourceOf).
func (a *Agent) inTurnPrefix(msgs []llm.Message, split, limit int) (prefix string, source session.TurnSource, ok bool) {
	open, found := session.OpeningPromptIndex(msgs, a.state.TurnAnchor())
	if !found || open >= split || strings.TrimSpace(msgs[open].Content) == "" {
		return "", session.TurnSourceUser, false
	}
	return inTurnPromptPrefix(msgs[open].Content, session.TurnFollowUps(msgs, open, split), limit), session.TurnSourceOf(msgs[open]), true
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

// foldFutileError is why an automatic fold inside the turn was not made: the
// smallest request it can produce is not under the threshold, so the very next
// step would trigger it again. It unwraps to ErrNothingToCompact, the answer the
// loop already continues on, and carries the numbers the skip is logged with.
type foldFutileError struct {
	// estimate is the smallest request the fold can produce, in the units the
	// threshold is measured in: the sum of the four parts below, in estimate
	// tokens, times scale.
	estimate int
	// threshold is compaction.threshold_percent of the window, the trigger.
	threshold int
	// overhead is what every request carries besides the conversation.
	overhead int
	// row is the summary row's own text apart from the summary: the prefix and
	// the preamble.
	row int
	// summary is the estimate of the summary itself.
	summary int
	// tail is the kept tail at the floor of one step.
	tail int
	// scale is how many tokens the provider counted for each token Coddy estimated
	// for the last request (providerScale), 1 when it reported none.
	scale float64
}

func (e *foldFutileError) Error() string {
	units := ""
	if e.scale != 1 {
		units = fmt.Sprintf(", estimate tokens, times %.2f as the provider counts", e.scale)
	}
	return fmt.Sprintf("folding the earlier steps of the turn cannot bring the request under the threshold: "+
		"the smallest request is about %d tokens (%d overhead + %d prefix and preamble + %d summary + %d latest step%s) against %d",
		e.estimate, e.overhead, e.row, e.summary, e.tail, units, e.threshold)
}

func (e *foldFutileError) Unwrap() error { return ErrNothingToCompact }

// checkFoldGain refuses an automatic fold inside the turn that cannot bring the
// request under the threshold. The smallest request the fold can produce is the
// request's overhead, the summary row's prefix and preamble, an estimate of the
// summary, and the kept tail at the floor of one step. The summary is estimated
// as the one the window opens with, when it opens with one: the next summary
// folds that one and what came after it, so it will be about as long; a window
// that opens with a prompt has no summary to go by and counts 0.
//
// The bar is the threshold, the trigger itself. A request at or above it triggers
// the fold again at the next step, which is a summarizer call per step and a
// summary of a summary. The aim of the fold is lower, half of the threshold share
// (inTurnAimPercent), and a fold that lands between the aim and the threshold
// still buys several steps before the next one; only a fold that cannot get under
// the trigger is futile, so that is what is refused. The recovery from a refused
// request does not come here: it runs once and aims at the refusal's limit.
//
// The trigger measures the context by the provider's own count of the last
// request (maybeAutoCompact), so the smallest request is scaled the same way
// before it meets the threshold: by how many tokens the provider counted for each
// one Coddy estimated (providerScale). Compared as a bare estimate, a provider
// that counts denser would see a fold pass that lands over the real threshold and
// folds again at the next step, and one that counts lighter would see a fold
// refused that fits.
func (a *Agent) checkFoldGain(msgs []llm.Message, visibleStart int, projected []llm.Message, limit int) error {
	floor, ok := session.TurnStepSplitIndex(msgs, 1, a.state.TurnAnchor())
	if !ok {
		return nil
	}
	pct := a.cfg.Compaction.EffectiveThresholdPercent()
	e := &foldFutileError{
		overhead:  a.requestOverhead(),
		threshold: limit * pct / 100,
		tail:      conversationTokens(projected[floor-visibleStart:], a.modelReadsImages()),
		scale:     a.providerScale(),
	}
	if prefix, source, ok := a.inTurnPrefix(msgs, floor, limit); ok {
		e.row = session.EstimateContextTokens(session.NewInTurnCompactionSummaryMessage(source, prefix, "", "").Content)
	} else {
		e.row = session.EstimateContextTokens(session.NewCompactionSummaryMessage("", "").Content)
	}
	if visibleStart < len(msgs) && msgs[visibleStart].CompactionSummary {
		e.summary = session.EstimateContextTokens(session.SummaryBody(msgs[visibleStart].Content))
	}
	e.estimate = int(math.Round(float64(e.overhead+e.row+e.summary+e.tail) * e.scale))
	if e.estimate*100 < limit*pct {
		return nil
	}
	return e
}

// providerScale is how many tokens the provider counted for each token Coddy
// estimated, read off the last request that came back with a count: the
// provider's input tokens over the estimate of that same prompt (the context
// breakdown's ProviderInputTokens and ProviderEstimateTokens). It is 1 when
// there is no such pair, which is the case before the first response and after a
// compaction, and when either number is not positive.
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
	// source is who wrote the message the prompt starts with (session.TurnSource);
	// the row's preamble says so.
	source session.TurnSource
}

// planInTurn decides where an in-turn compaction cuts, and the prompt that goes
// in front of its summary. opts.LimitTokens is the limit the next request has to
// fit under; unset, it is the session's window.
func (a *Agent) planInTurn(opts CompactOptions, msgs []llm.Message, visibleStart int, projected []llm.Message) (*inTurnPlan, error) {
	comp := &a.cfg.Compaction
	limit := a.promptShareLimit(opts.LimitTokens)
	target := inTurnTargetTokens(limit, comp.EffectiveThresholdPercent())
	overhead := a.requestOverhead()

	// The budget is measured with the largest prefix the fold can write, every
	// follow-up of the turn included: the prefix of the boundary chosen below holds
	// no more of them, so a fold never keeps more than the budget it was given.
	prefixTokens := 0
	if prefix, source, ok := a.inTurnPrefix(msgs, len(msgs), limit); ok {
		prefixTokens = session.EstimateContextTokens(session.NewInTurnCompactionSummaryMessage(source, prefix, "", "").Content)
	}
	regularPreamble := session.EstimateContextTokens(session.NewCompactionSummaryMessage("", "").Content)

	choice, err := chooseInTurnSplit(inTurnSplitRequest{
		msgs:          msgs,
		visibleStart:  visibleStart,
		anchor:        a.state.TurnAnchor(),
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
	if !opts.Recovery {
		if err := a.checkFoldGain(msgs, visibleStart, projected, limit); err != nil {
			return nil, err
		}
	}
	plan := &inTurnPlan{inTurnSplit: choice}
	if !choice.regular {
		plan.prompt, plan.source, _ = a.inTurnPrefix(msgs, choice.idx, limit)
	}
	return plan, nil
}
