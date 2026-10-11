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

// TurnSource says who wrote the message a turn opened with. The user typed a
// prompt; a goal turn opens with the instruction the session's goal supervisor
// started it with, and a woken turn with the notice that a background task
// finished. The summary row of an in-turn fold repeats that message in front of
// its summary and has to say whose text it is, or the model reads a supervisor's
// instruction as something the user asked.
type TurnSource int

const (
	// TurnSourceUser is a message the user sent: the zero value, and the source of
	// every message without a marker.
	TurnSourceUser TurnSource = iota
	// TurnSourceSupervisor is a goal turn's first message (llm.Message.GoalTurn).
	TurnSourceSupervisor
	// TurnSourceBackground is the first message of a turn woken by a finished
	// background task (llm.Message.BackgroundWake).
	TurnSourceBackground
)

// TurnSourceOf reads the source of a turn's opening message off its markers.
func TurnSourceOf(m llm.Message) TurnSource {
	switch {
	case m.GoalTurn != nil:
		return TurnSourceSupervisor
	case m.BackgroundWake != nil:
		return TurnSourceBackground
	}
	return TurnSourceUser
}

// inTurnSummaryFollowUps and inTurnSummaryTail are the two ends every preamble of
// an in-turn row shares. The first says the text above may go on with follow-ups
// of the user, whichever source opened the turn, so one wording serves a row with
// follow-ups and one without. The tail ends in the words the web UI cuts a summary
// row at, so the UI renders the same body for every source.
const (
	inTurnSummaryFollowUps = ", followed by any follow-up messages the user sent while it was being worked on. "
	inTurnSummaryTail      = "Coddy compacted the conversation before this point, including the earlier steps of the work on this request. Summary of the compacted part:\n\n"
)

// The preamble follows the opening message at the start of the summary row an
// in-turn fold writes. It says what the text above it is: the row is the first
// message of the replay window, and without this line the request would read as a
// message of its own that the summary below it has nothing to do with. There is
// one variant per source (TurnSource). The text above is the opening message and,
// when the user sent any while it was being worked on, the follow-ups, which the
// line names. The preambles are the strings the row is split on (SplitInTurnRow),
// so every reader of a row finds the same boundary.
const (
	inTurnSummaryPreamble    = "The text above is the user's request, verbatim" + inTurnSummaryFollowUps + inTurnSummaryTail
	inTurnSupervisorPreamble = "The text above is the instruction the session's goal supervisor started this turn with, verbatim" + inTurnSummaryFollowUps + inTurnSummaryTail
	inTurnBackgroundPreamble = "The text above is the notice that woke this turn when a background task finished, verbatim" + inTurnSummaryFollowUps + inTurnSummaryTail
)

// allTurnSources lists the sources in the order SplitInTurnRow tries them.
var allTurnSources = [...]TurnSource{TurnSourceUser, TurnSourceSupervisor, TurnSourceBackground}

// inTurnPreamble is the line a row written for a turn opened by t carries.
func (t TurnSource) inTurnPreamble() string {
	switch t {
	case TurnSourceSupervisor:
		return inTurnSupervisorPreamble
	case TurnSourceBackground:
		return inTurnBackgroundPreamble
	}
	return inTurnSummaryPreamble
}

// StopHookPrefix marks the follow-up a Stop hook submits as the next user
// message, so the transcript says where it came from. The text after it is hook
// output, not something the user typed.
const StopHookPrefix = "[Stop hook] "

// TurnAnchor names the user message a turn opened with: the message the agent
// appended when the turn started. It is known by its index in the transcript,
// which the State keeps in step with the rows a compaction inserts in front of
// it (State.TurnAnchor, InsertCompactionSummary). An earlier version knew it by
// its text and the second it was written in, which a follow-up queued within that
// second with the same words could not be told apart from. The zero value is "no
// record" (the process restarted since the turn began, a test that calls the
// compaction directly), and the functions below then fall back to the last user
// message that is not a Stop-hook follow-up.
type TurnAnchor struct {
	idx int
	set bool
}

// AnchorAt records i, the index in the transcript of the message a turn opens
// with. The functions that take an anchor check that the message there is still a
// real user message, so an index that has gone stale falls back instead of
// naming something else.
func AnchorAt(i int) TurnAnchor {
	return TurnAnchor{idx: i, set: true}
}

// isRealUser reports a user-role message that is not a compaction summary row.
func isRealUser(m llm.Message) bool {
	return m.Role == llm.RoleUser && !m.CompactionSummary
}

// OpeningPromptIndex returns the absolute index in msgs of the message the turn
// in progress opened with: the one the anchor names when it names a real user
// message - a woken turn's or a goal turn's first message counts, it is that
// turn's request. Without a record (or when it names anything else) the opening
// prompt is the last real user message that is not a Stop-hook follow-up; a
// follow-up the user queued is indistinguishable from the prompt then, which is
// the one case the record exists for. ok is false when there is no such message.
//
// The user messages after the opening prompt are follow-ups: they belong to the
// turn and never start a turn of their own.
func OpeningPromptIndex(msgs []llm.Message, anchor TurnAnchor) (int, bool) {
	if anchor.set && anchor.idx >= 0 && anchor.idx < len(msgs) && isRealUser(msgs[anchor.idx]) {
		return anchor.idx, true
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if isRealUser(msgs[i]) && !strings.HasPrefix(msgs[i].Content, StopHookPrefix) {
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
// message is the remainder of a turn whose prompt a fold took away - an
// in-turn fold, or a compact_context call in the middle of a turn. That row
// counts as the start of a turn, so the remainder can be folded the way any
// older turn is once a new prompt arrives; a summary followed by a user message
// is a regular compaction and stays out of the count.
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
// summary row, that row at the start of the window is the anchor (an earlier
// in-turn fold or a compact_context call took the prompt away and left the rest
// of the turn behind it). The steps are the assistant messages after the anchor;
// steps of earlier turns are not counted. keepSteps below zero counts as zero,
// and zero keeps nothing: the result is len(msgs). ok is false when the turn
// has no more steps than keepSteps, or when there is no turn.
func TurnStepSplitIndex(msgs []llm.Message, keepSteps int, anchor TurnAnchor) (idx int, ok bool) {
	if keepSteps < 0 {
		keepSteps = 0
	}
	steps := turnSteps(msgs, anchor)
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
func TurnStepCount(msgs []llm.Message, anchor TurnAnchor) int {
	return len(turnSteps(msgs, anchor))
}

// turnSteps returns the absolute indexes of the assistant messages that open
// the steps of the turn being answered, in order (see TurnStepSplitIndex for
// what the turn is).
func turnSteps(msgs []llm.Message, anchor TurnAnchor) []int {
	start := llmWindowStart(msgs)
	from := -1
	if p, ok := OpeningPromptIndex(msgs, anchor); ok && p >= start {
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

// TurnPrompt returns the text of the message the turn in progress opened with
// (OpeningPromptIndex): the prompt being answered, found even when a summary row
// hides it from the replay window and even when follow-ups came after it. ok is
// false when there is none or it carries no text - an older prompt never stands
// in for a blank one.
func TurnPrompt(msgs []llm.Message, anchor TurnAnchor) (string, bool) {
	i, ok := OpeningPromptIndex(msgs, anchor)
	if !ok || strings.TrimSpace(msgs[i].Content) == "" {
		return "", false
	}
	return msgs[i].Content, true
}

// TurnFollowUps returns the follow-ups the user sent while the turn that opened
// at msgs[open] was being worked on and that lie before index end, verbatim and
// in order: the user messages after the opening prompt, however long ago a fold
// hid them from the replay window. A follow-up the user queued is the user's own
// text. A Stop-hook follow-up (StopHookPrefix) is hook output and a summary row
// is the harness's, so neither is one; nor is a blank message.
func TurnFollowUps(msgs []llm.Message, open, end int) []string {
	var out []string
	for i := open + 1; i < end && i < len(msgs); i++ {
		m := msgs[i]
		if !isRealUser(m) || m.BackgroundWake != nil || m.GoalTurn != nil {
			continue
		}
		if strings.HasPrefix(m.Content, StopHookPrefix) || strings.TrimSpace(m.Content) == "" {
			continue
		}
		out = append(out, m.Content)
	}
	return out
}

// NewInTurnCompactionSummaryMessage builds the summary row of a fold that
// stayed inside the turn being answered. The row becomes the start of the
// replay window and the prompt it replaced must not vanish with the steps, so
// the row begins with the prompt itself, verbatim (prompt is already capped by
// the caller), then says what the text above it is - the preamble of source, the
// writer of the turn's opening message (TurnSourceOf) - then carries the summary.
// The prompt's pictures are not copied onto the row.
func NewInTurnCompactionSummaryMessage(source TurnSource, prompt, summary, model string) llm.Message {
	m := NewCompactionSummaryMessage(summary, model)
	m.Content = prompt + "\n\n" + source.inTurnPreamble() + strings.TrimSpace(summary)
	return m
}

// restampUILog renumbers the UI log entries for a user-role row about to be
// inserted at idx. An entry stamped t (UILogEntry.UserTurnIndex) was logged when
// t user-role rows existed and renders right before the t-th one (0-based), at
// the end of the turn it was logged in. A row inserted before that point moves
// the t-th row down by one, so the entries that must stay behind it are
// renumbered; the ones that must stay in front of the new row keep their number.
// r is the number of user-role rows before idx.
//
//   - At a turn boundary (a real user message follows idx, or the row is
//     appended) the row opens the turn after the one it ends. An entry with
//     exactly r ended the turn before it and stays in front of the row; the
//     entries of the turns after it, t > r, move down with their turns.
//   - Inside a turn (anything else follows idx) the row is the turn's second
//     user-role row. The entries logged in that turn carry r, and the ones of the
//     turns after it more; all of them stay behind the row, at the end of their
//     turn, so t >= r moves. Left alone, the entries logged earlier in the turn
//     would render in front of the row, in the middle of the turn.
//
// Callers hold s.mu.
func (s *State) restampUILog(idx int) {
	if len(s.UILog) == 0 {
		return
	}
	r := CountUserTurns(s.Messages[:idx])
	boundary := idx >= len(s.Messages) || isRealUser(s.Messages[idx])
	for i := range s.UILog {
		if t := s.UILog[i].UserTurnIndex; t > r || (!boundary && t >= r) {
			s.UILog[i].UserTurnIndex++
		}
	}
}

// SplitInTurnSummary splits the content of an in-turn summary row, as
// NewInTurnCompactionSummaryMessage wrote it, into what is in front of the
// summary - the opening message of the turn and the follow-ups it carries,
// verbatim - and the summary the worker's model wrote. ok is false for any other
// content, a plain summary row included. It is SplitInTurnRow for a reader that
// does not care who opened the turn.
func SplitInTurnSummary(content string) (prompt, summary string, ok bool) {
	prompt, summary, _, ok = SplitInTurnRow(content)
	return prompt, summary, ok
}

// SplitInTurnRow is SplitInTurnSummary that also says who wrote the opening
// message in front of the summary (TurnSource), read off the preamble the row
// carries.
//
// The split is at the first occurrence of a preamble line that separates the
// two, whichever source's it is, so every reader of a row (the supervisor's
// digest, the replay for ACP clients, the rule bookkeeping) finds the same
// boundary. A request that itself quotes a preamble line is the one input it can
// misread: the part of the request after the quote is taken for the summary. A
// plain row is recognised by its own leading preamble and is never split,
// whatever its summary quotes.
func SplitInTurnRow(content string) (prompt, summary string, source TurnSource, ok bool) {
	if strings.HasPrefix(content, compactionSummaryPreamble) {
		return "", "", TurnSourceUser, false
	}
	const sep = "\n\n"
	at := -1
	var preamble string
	for _, src := range allTurnSources {
		p := src.inTurnPreamble()
		if i := strings.Index(content, sep+p); i >= 0 && (at < 0 || i < at) {
			at, preamble, source = i, p, src
		}
	}
	if at < 0 {
		return "", "", TurnSourceUser, false
	}
	return content[:at], content[at+len(sep)+len(preamble):], source, true
}

// SummaryReplayText is the text of a summary row as a client that replays the
// transcript is shown it. A plain row is shown as stored. An in-turn row starts
// with the opening message, which the replay has already shown at its own place,
// so it is shown from its preamble on.
func SummaryReplayText(content string) string {
	if _, summary, source, ok := SplitInTurnRow(content); ok {
		return source.inTurnPreamble() + summary
	}
	return content
}

// SummaryBody is the summary a summary row carries: the text the summarizer
// wrote, without the preamble of either kind of row and without the request in
// front of an in-turn row. Content that is no summary row comes back unchanged.
func SummaryBody(content string) string {
	if _, summary, ok := SplitInTurnSummary(content); ok {
		return summary
	}
	return strings.TrimPrefix(content, compactionSummaryPreamble)
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
// and persists the session. The summary row is a user-role row, and the UI log
// numbers its entries by the user-role rows before them (CountUserTurns), so the
// entries are numbered again under the same lock (restampUILog).
func (s *State) InsertCompactionSummary(idx int, msg llm.Message) {
	s.mu.Lock()
	if idx < 0 || idx > len(s.Messages) {
		idx = len(s.Messages)
	}
	s.restampUILog(idx)
	// A row in front of the message the turn opened with, or in its place, moves
	// that message down by one; a row after it leaves it where it is.
	if s.turnOpeningSet && idx <= s.turnOpeningAt {
		s.turnOpeningAt++
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
