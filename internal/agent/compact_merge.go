package agent

// Merging the earliest summaries of the chain.
//
// A compaction adds a block after the summary rows before it and leaves them as
// they were written (issue #361), so the provider's cached prefix holds up to
// the end of the last of them and no detail is retold away at every fold. The
// price is a chain that grows by a row per compaction and, in a session long
// enough, would fill the window by itself.
//
// So once the chain passes compactionChainSharePercent of the session's window,
// its earliest rows are retold as one summary row that starts the chain anew: a
// row without the block mark, inserted right after the last row it retells, so
// the chain reads from it on (session.LLMWindow) and the transcript keeps every
// row it replaced. As many rows are merged as bring the chain, the merged row
// included, down to compactionChainAimPercent of that share. The distance
// between the two is the point: a merge rewrites the prefix the provider
// cached, and a chain merged down to the threshold would be merged again at the
// next compaction.
//
// The row the compaction just wrote is never merged: no merge could make it
// smaller. The rows of the turn in progress go last, and only when the earlier
// ones are not enough, which is the case of one long turn folded again and
// again. The first of them carries the turn's prompt verbatim in front of its
// summary; a merge that takes it puts the prompt in front of the merged row the
// same way, so the model still reads the prompt once.

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	// compactionChainSharePercent is how much of the session's window the chain
	// of summary rows may take before its earliest rows are merged. It is the
	// session's window, not the summarizer's: the chain is part of every request
	// the session's model is sent.
	compactionChainSharePercent = 33
	// compactionChainAimPercent is how much of that share a merge brings the
	// chain down to.
	compactionChainAimPercent = 50
	// compactionChainMinMerge is the fewest rows a merge retells: one row
	// retold alone is the same row in other words.
	compactionChainMinMerge = 2
)

// chainMergeCount is how many of the earliest rows of the chain a merge retells
// for a session window of window tokens: the fewest that bring the chain to the
// aim, counting the merged row as long as the longest row it retells (the
// summarizer is asked for no more). ok is false when the chain is within its
// share, or when fewer than compactionChainMinMerge rows may be merged.
func chainMergeCount(msgs []llm.Message, window int) (k int, ok bool) {
	if window <= 0 {
		window = config.DefaultContextWindowTokens
	}
	blocks, _ := session.LLMWindow(msgs)
	sizes := make([]int, len(blocks))
	total := 0
	for i, idx := range blocks {
		sizes[i] = session.EstimateContextTokens(msgs[idx].Content)
		total += sizes[i]
	}
	share := window * compactionChainSharePercent / 100
	if total <= share {
		return 0, false
	}
	aim := share * compactionChainAimPercent / 100
	rest, longest := total, 0
	// The newest row is never merged.
	for k < len(blocks)-1 && (k < compactionChainMinMerge || rest+longest > aim) {
		rest -= sizes[k]
		longest = max(longest, sizes[k])
		k++
	}
	if k < compactionChainMinMerge {
		return 0, false
	}
	return k, true
}

// withChainMergeInstructions tells the summarizer the transcript it reads is a
// run of summaries, and what the one it writes replaces.
func withChainMergeInstructions(instructions string) string {
	note := "The transcript is a run of consecutive summaries of one conversation, oldest first; there are no other messages in it. " +
		"Write one summary that replaces them all. Keep every detail of them that can still matter: the user's requirements, " +
		"the decisions and the approaches rejected, exact paths, names, commands and values, and what is still open. " +
		"Where a later summary corrects an earlier one, keep the later account. " +
		"Make it no longer than the longest of the summaries you are given."
	if instructions == "" {
		return note
	}
	return instructions + "\n\n" + note
}

// mergeChain merges the earliest rows of the chain when it has outgrown its
// share of the session's window (chainMergeCount), and reports how many rows
// the merge retold; 0 with a nil error when no merge was due.
func (a *Agent) mergeChain(ctx context.Context, chain []compactionCandidate, instructions string, progress compactionProgress) (int, error) {
	msgs := a.state.GetMessages()
	window, _ := a.contextWindow()
	k, ok := chainMergeCount(msgs, window)
	if !ok {
		return 0, nil
	}
	blocks, _ := session.LLMWindow(msgs)
	open, hasOpen := session.OpeningPromptIndex(msgs)
	rows := make([]llm.Message, k)
	prompt := ""
	for i := range rows {
		rows[i] = msgs[blocks[i]]
		// The row of the turn in progress that carries its prompt: the prompt
		// goes in front of the merged row as it stands, and only the summary
		// after it is retold.
		if hasOpen && blocks[i] > open && prompt == "" {
			if p, summary, ok := session.SplitInTurnSummary(rows[i].Content); ok {
				prompt, rows[i].Content = p, summary
			}
		}
	}
	instructions = withChainMergeInstructions(instructions)
	if prompt != "" {
		instructions = withInTurnSummaryInstructions(instructions)
	}
	sumWindow, _ := a.contextWindowFor(chain[0].modelID)
	budget := compactionInputBudget(sumWindow, session.EstimateContextTokens(instructions))
	summary, modelID, _, err := a.foldCompactionHead(ctx, chain, "", rows, instructions, budget, progress)
	if err != nil {
		return 0, err
	}
	row := session.NewCompactionSummaryMessage(summary, modelID)
	if prompt != "" {
		row = session.NewInTurnCompactionSummaryMessage(prompt, summary, modelID)
	}
	a.state.InsertCompactionSummary(blocks[k-1]+1, row)
	return k, nil
}
