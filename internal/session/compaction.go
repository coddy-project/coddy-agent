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

// inTurnSummaryPreamble follows the opening message of the turn at the start of
// the summary row an in-turn fold writes. It says what the text above it is: the
// row is the first message of the replay window, and without this line the
// request would read as a message of its own that the summary below it has
// nothing to do with. The wording names no author: a goal turn's instruction and
// a background wake's notice say who wrote them in their own first words. The
// line ends in the words the web UI cuts a summary row at, and it is the string
// the row is split on (SplitInTurnSummary), so every reader of a row finds the
// same boundary.
const inTurnSummaryPreamble = "The text above is the message this turn started with, verbatim. Coddy compacted the conversation before this point, including the earlier steps of the work on it. Summary of the compacted part:\n\n"

// StopHookPrefix marks the follow-up a Stop hook submits as the next user
// message, so the transcript says where it came from. The text after it is hook
// output, not something the user typed.
const StopHookPrefix = "[Stop hook] "

// isRealUser reports a user-role message that is not a compaction summary row.
func isRealUser(m llm.Message) bool {
	return m.Role == llm.RoleUser && !m.CompactionSummary
}

// isFollowUp reports a user message that belongs to the turn it was sent in
// rather than opening one: a message the user queued while the turn ran
// (llm.Message.FollowUp) or the follow-up a Stop hook submitted (StopHookPrefix).
func isFollowUp(m llm.Message) bool {
	return m.FollowUp || strings.HasPrefix(m.Content, StopHookPrefix)
}

// OpeningPromptIndex returns the absolute index in msgs of the message the turn
// in progress opened with: the last real user message that is not a follow-up
// (isFollowUp). A woken turn's or a goal turn's first message counts, it is that
// turn's request. ok is false when there is no such message.
//
// The user messages after the opening prompt are follow-ups: they belong to the
// turn and never start a turn of their own. A transcript written before the
// marker existed has none, and its last user message counts as the opening one.
func OpeningPromptIndex(msgs []llm.Message) (int, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if isRealUser(msgs[i]) && !isFollowUp(msgs[i]) {
			return i, true
		}
	}
	return 0, false
}

// CompactionSplitIndex returns the absolute index in msgs where the kept tail
// begins when compacting with keepRecentTurns: the boundary sits at the
// keepRecentTurns-th from last real user message inside the LLM-visible
// window (keepRecentTurns 0 keeps nothing verbatim). The head to summarize is
// msgs[llmWindowStart:idx], which includes the previous summary on chained
// compaction. ok is false when the window has no full user turn to fold away.
//
// A window that opens with a summary row followed straight by an assistant
// message is the remainder of a turn whose prompt an in-turn fold took away.
// That row counts as the start of a turn, so the remainder can be folded the
// way any older turn is once a new prompt arrives; a summary followed by a user
// message is a regular compaction and stays out of the count.
func CompactionSplitIndex(msgs []llm.Message, keepRecentTurns int) (idx int, ok bool) {
	return CompactionSplitIndexUpTo(msgs, keepRecentTurns, len(msgs)-1)
}

// CompactionSplitIndexUpTo is CompactionSplitIndex for a turn in progress: only
// the user messages up to and including the one at index lastTurnStart (the
// turn's opening prompt, OpeningPromptIndex) are turn boundaries. The user
// messages after it are follow-ups to the same turn, and a split at one of them
// would fold the opening prompt into a plain summary, which is what the automatic
// compaction exists never to do.
func CompactionSplitIndexUpTo(msgs []llm.Message, keepRecentTurns, lastTurnStart int) (idx int, ok bool) {
	if keepRecentTurns < 0 {
		keepRecentTurns = 0
	}
	start := llmWindowStart(msgs)
	var userIdx []int
	if start+1 < len(msgs) && msgs[start].CompactionSummary && msgs[start+1].Role == llm.RoleAssistant {
		userIdx = append(userIdx, start)
	}
	for i := start; i < len(msgs) && i <= lastTurnStart; i++ {
		if isRealUser(msgs[i]) {
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
// The turn starts after its opening prompt (OpeningPromptIndex): the follow-ups
// the user sent during the turn belong to it, so the steps before a follow-up
// are steps of this turn as well. When a fold hid the opening prompt behind a
// summary row, that row at the start of the window is where the turn starts
// (an earlier in-turn fold took the prompt away and left the rest of the turn
// behind it). Steps of earlier turns are not counted. keepSteps below zero
// counts as zero, and zero keeps nothing: the result is len(msgs). ok is false
// when the turn has no more steps than keepSteps, or when there is no turn.
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
	from := -1
	if p, ok := OpeningPromptIndex(msgs); ok && p >= start {
		from = p
	}
	if from < 0 && start < len(msgs) && msgs[start].CompactionSummary {
		from = start
	}
	if from < 0 {
		return nil
	}
	var steps []int
	for i := from + 1; i < len(msgs); i++ {
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

// SplitInTurnSummary splits the content of an in-turn summary row, as
// NewInTurnCompactionSummaryMessage wrote it, into the opening message of the
// turn in front of the summary, verbatim, and the summary the worker's model
// wrote. ok is false for any other content, a plain summary row included.
//
// The split is at the first occurrence of the preamble, so every reader of a row
// (the supervisor's digest, the replay for ACP clients, the rule bookkeeping)
// finds the same boundary. A request that itself quotes the preamble is the one
// input it can misread: the part of the request after the quote is taken for
// the summary. A plain row is recognised by its own leading preamble and is never
// split, whatever its summary quotes.
func SplitInTurnSummary(content string) (prompt, summary string, ok bool) {
	if strings.HasPrefix(content, compactionSummaryPreamble) {
		return "", "", false
	}
	before, after, found := strings.Cut(content, "\n\n"+inTurnSummaryPreamble)
	if !found {
		return "", "", false
	}
	return before, after, true
}

// SummaryReplayText is the text of a summary row as a client that replays the
// transcript is shown it. A plain row is shown as stored. An in-turn row starts
// with the opening message, which the replay has already shown at its own place,
// so it is shown from its preamble on.
func SummaryReplayText(content string) string {
	if _, summary, ok := SplitInTurnSummary(content); ok {
		return inTurnSummaryPreamble + summary
	}
	return content
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
