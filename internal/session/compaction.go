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

// CompactionSplitIndex returns the absolute index in msgs where the kept tail
// begins when compacting with keepRecentTurns: the boundary sits at the
// keepRecentTurns-th from last real user message inside the LLM-visible
// window (keepRecentTurns 0 keeps nothing verbatim). The head to summarize is
// msgs[llmWindowStart:idx], which includes the previous summary on chained
// compaction. ok is false when the window has no full user turn to fold away.
func CompactionSplitIndex(msgs []llm.Message, keepRecentTurns int) (idx int, ok bool) {
	if keepRecentTurns < 0 {
		keepRecentTurns = 0
	}
	start := llmWindowStart(msgs)
	var userIdx []int
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
	s.mu.Unlock()
	s.touchPersist()
}
