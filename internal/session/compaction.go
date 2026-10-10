package session

import (
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// compactionSummaryPreamble prefixes the generated summary so the LLM (and the
// transcript reader) knows earlier history was replaced by this message.
const compactionSummaryPreamble = "The earlier conversation was compacted. Summary of the compacted part:\n\n"

// llmWindowStart returns the index where the LLM-visible window begins: the
// last compaction summary message (inclusive), or 0 when none exists.
func llmWindowStart(msgs []llm.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].CompactionSummary {
			return i
		}
	}
	return 0
}

// MessagesForLLM returns the slice of history visible to the LLM: everything
// from the last compaction summary (inclusive) to the end. Messages before the
// summary stay in the persisted transcript for UI replay only.
func MessagesForLLM(msgs []llm.Message) []llm.Message {
	return msgs[llmWindowStart(msgs):]
}

// inTurnSummaryPreamble follows the user's request at the start of the summary
// row an in-turn fold writes. It says what the text above it is: the row is the
// first message of the replay window, and without this line the request would
// read as a message of its own that the summary below it has nothing to do with.
const inTurnSummaryPreamble = "The text above is the user's request, verbatim. Coddy compacted the conversation before this point, " +
	"including the earlier steps of the work on this request. Summary of the compacted part:\n\n"

// CompactionSplitIndex returns the absolute index in msgs where the kept tail
// begins when compacting with keepRecentTurns: the boundary sits at the
// keepRecentTurns-th from last real user message inside the LLM-visible
// window (keepRecentTurns 0 keeps nothing verbatim). The head to summarize is
// msgs[llmWindowStart:idx], which includes the previous summary on chained
// compaction. ok is false when the window has no full user turn to fold away.
//
// A window that opens with a summary row followed straight by an assistant
// message is the remainder of a turn whose prompt a fold took away - an
// in-turn fold, or a compact_context call in the middle of a turn. That row
// counts as the start of a turn, so the remainder can be folded the way any
// older turn is once a new prompt arrives; a summary followed by a user message
// is a regular compaction and stays out of the count.
func CompactionSplitIndex(msgs []llm.Message, keepRecentTurns int) (idx int, ok bool) {
	if keepRecentTurns < 0 {
		keepRecentTurns = 0
	}
	start := llmWindowStart(msgs)
	var userIdx []int
	if start+1 < len(msgs) && msgs[start].CompactionSummary && msgs[start+1].Role == llm.RoleAssistant {
		userIdx = append(userIdx, start)
	}
	for i := start; i < len(msgs); i++ {
		if msgs[i].Role == llm.RoleUser && !msgs[i].CompactionSummary {
			userIdx = append(userIdx, i)
		}
	}
	if len(userIdx) <= keepRecentTurns {
		return 0, false
	}
	if keepRecentTurns == 0 {
		return len(msgs), true
	}
	return userIdx[len(userIdx)-keepRecentTurns], true
}

// TurnStepSplitIndex returns the absolute index in msgs where the last
// keepSteps steps of the turn being answered begin, for a fold that stays
// inside the turn. A step is an assistant message with the tool results that
// follow it, so the boundary is always an assistant message: a batch of
// parallel calls is never cut between its call and its results, and the head
// ends on a complete batch.
//
// The turn starts after its anchor, the last real user message of the
// LLM-visible window; when the window holds none and opens with a summary row,
// that row is the anchor (an earlier in-turn fold or a compact_context call
// took the prompt away and left the rest of the turn behind it). The steps are
// the assistant messages after the anchor; steps of earlier turns are not
// counted. keepSteps below zero counts as zero, and zero keeps nothing: the
// result is len(msgs). ok is false when the turn has no more steps than
// keepSteps, or when there is no turn.
func TurnStepSplitIndex(msgs []llm.Message, keepSteps int) (idx int, ok bool) {
	if keepSteps < 0 {
		keepSteps = 0
	}
	steps := turnSteps(msgs)
	if len(steps) <= keepSteps {
		return 0, false
	}
	if keepSteps == 0 {
		return len(msgs), true
	}
	return steps[len(steps)-keepSteps], true
}

// TurnStepCount is how many steps the turn being answered has: the length of
// the run TurnStepSplitIndex counts back from. 0 when there is no turn.
func TurnStepCount(msgs []llm.Message) int {
	return len(turnSteps(msgs))
}

// turnSteps returns the absolute indexes of the assistant messages that open
// the steps of the turn being answered, in order (see TurnStepSplitIndex for
// what the turn is).
func turnSteps(msgs []llm.Message) []int {
	start := llmWindowStart(msgs)
	anchor := -1
	for i := len(msgs) - 1; i >= start; i-- {
		if msgs[i].Role == llm.RoleUser && !msgs[i].CompactionSummary {
			anchor = i
			break
		}
	}
	if anchor < 0 && start < len(msgs) && msgs[start].CompactionSummary {
		anchor = start
	}
	if anchor < 0 {
		return nil
	}
	var steps []int
	for i := anchor + 1; i < len(msgs); i++ {
		if msgs[i].Role == llm.RoleAssistant && !isUIOnlyRow(msgs[i]) {
			steps = append(steps, i)
		}
	}
	return steps
}

// isUIOnlyRow reports a transcript row that only the UI shows: a plan document
// snapshot, an assistant-role row with no text, no reasoning and no tool calls.
// It can sit between an assistant message and the results of its calls, so it is
// no step of its own and no place to split at.
func isUIOnlyRow(m llm.Message) bool {
	return m.PlanDocument != nil && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && strings.TrimSpace(m.Reasoning) == ""
}

// TurnPrompt returns the text of the last real user message of the whole
// transcript: the prompt being answered, found even when a summary row hides it
// from the replay window. ok is false when there is none or it carries no text
// - an older prompt never stands in for a blank latest one.
func TurnPrompt(msgs []llm.Message) (string, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != llm.RoleUser || msgs[i].CompactionSummary {
			continue
		}
		if strings.TrimSpace(msgs[i].Content) == "" {
			return "", false
		}
		return msgs[i].Content, true
	}
	return "", false
}

// NewInTurnCompactionSummaryMessage builds the summary row of a fold that
// stayed inside the turn being answered. The row becomes the start of the
// replay window and the prompt it replaced must not vanish with the steps, so
// the row begins with the prompt itself, verbatim (prompt is already capped by
// the caller), then says what the text above it is, then carries the summary.
// The prompt's pictures are not copied onto the row.
func NewInTurnCompactionSummaryMessage(prompt, summary, model string) llm.Message {
	m := NewCompactionSummaryMessage(summary, model)
	m.Content = prompt + "\n\n" + inTurnSummaryPreamble + strings.TrimSpace(summary)
	return m
}

// NewCompactionSummaryMessage builds the transcript row holding a generated
// summary. It uses the user role so every provider replays it as plain
// conversation input (tool results already travel as user-role messages).
func NewCompactionSummaryMessage(summary, model string) llm.Message {
	return llm.Message{
		Role:              llm.RoleUser,
		Content:           compactionSummaryPreamble + strings.TrimSpace(summary),
		CompactionSummary: true,
		Model:             model,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339),
	}
}

// InsertCompactionSummary inserts msg at index idx (append when out of range)
// and persists the session.
func (s *State) InsertCompactionSummary(idx int, msg llm.Message) {
	s.mu.Lock()
	if idx < 0 || idx > len(s.Messages) {
		idx = len(s.Messages)
	}
	s.Messages = append(s.Messages[:idx], append([]llm.Message{msg}, s.Messages[idx:]...)...)
	// An insert is an append only when it lands at the end; anywhere else it
	// rewrites the tail, and persistence must encode the history afresh.
	if idx == len(s.Messages)-1 {
		s.markMessagesAppended()
	} else {
		s.markMessagesEdited()
	}
	s.mu.Unlock()
	s.touchPersist()
}
