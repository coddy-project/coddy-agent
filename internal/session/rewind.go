package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// ErrRewindOutOfRange reports a userMessageIndex that names no user message
// in the session history.
var ErrRewindOutOfRange = errors.New("user message index is out of range")

// ErrSessionTurnActive reports a rewind attempt while the session has a turn
// in flight - in this process or in another one holding the turn lock.
var ErrSessionTurnActive = errors.New("session has a turn in flight")

// RewindSession truncates the session history in place: the Nth user message
// (0-based over llm.RoleUser rows, a background wake row counting as one) and
// everything after it are dropped, so the next prompt continues the same
// session from that point. Editing a sent message uses this instead of the
// removed branch flow, which copied the prefix into a new bundle.
//
// The caller is expected to have loaded the session already (the HTTP handler
// goes through coddyEnsureLoaded); a session that is not in memory is an
// error rather than a silent disk edit.
//
// Returns the new messages revision.
func (m *Manager) RewindSession(sessionID string, userMessageIndex int) (uint64, error) {
	if m.store == nil || m.store.Root == "" {
		return 0, fmt.Errorf("session store unavailable")
	}
	if userMessageIndex < 0 {
		return 0, ErrRewindOutOfRange
	}
	st := m.getSession(sessionID)
	if st == nil {
		return 0, fmt.Errorf("session %q is not loaded", sessionID)
	}
	if st.IsSchedulerJob() {
		return 0, fmt.Errorf("%w: %s is the session of scheduler job %q and cannot be rewound", ErrSchedulerSessionReadOnly, sessionID, st.GetSchedulerJobID())
	}
	if st.Subagent() != nil {
		return 0, fmt.Errorf("%w: %s cannot be rewound", ErrSubagentReadOnly, sessionID)
	}
	dir := strings.TrimSpace(st.GetPersistedSessionDir())
	if dir == "" {
		return 0, fmt.Errorf("session %q has no persisted bundle", sessionID)
	}
	turnActive := func() bool {
		return m.SessionTurnActiveInProcess(sessionID) || TurnLockHeld(dir)
	}
	if turnActive() {
		return 0, ErrSessionTurnActive
	}
	if err := st.TruncateMessagesBeforeUserN(userMessageIndex, turnActive); err != nil {
		return 0, err
	}
	rewindCleanupArtifacts(dir, st.GetMessages())
	return st.MessagesRev(), nil
}

// TruncateMessagesBeforeUserN drops the Nth (0-based) user message and every
// message after it, in place. UI log rows stamped with a turn number beyond
// the surviving prefix go with the dropped turns (appendUILog stamps a
// 1-based CountUserTurns, so rows belonging to surviving turns carry
// userTurnIndex <= n). turnActive, when set, is re-checked under the state
// lock right before the cut so a turn admitted after the caller's own check
// still refuses instead of appending onto a rewound history.
func (s *State) TruncateMessagesBeforeUserN(n int, turnActive func() bool) error {
	s.mu.Lock()
	idx := nthUserMessageIndex(s.Messages, n)
	if idx < 0 {
		s.mu.Unlock()
		return ErrRewindOutOfRange
	}
	if turnActive != nil && turnActive() {
		s.mu.Unlock()
		return ErrSessionTurnActive
	}
	s.Messages = s.Messages[:idx]
	kept := s.UILog[:0]
	for _, e := range s.UILog {
		if e.UserTurnIndex <= n {
			kept = append(kept, e)
		}
	}
	s.UILog = kept
	s.markMessagesEdited()
	s.mu.Unlock()
	s.touchPersist()
	return nil
}

// nthUserMessageIndex returns the index of the Nth (0-based) llm.RoleUser
// message, or -1 when fewer than n+1 user messages exist.
func nthUserMessageIndex(msgs []llm.Message, n int) int {
	count := 0
	for i, m := range msgs {
		if m.Role == llm.RoleUser {
			if count == n {
				return i
			}
			count++
		}
	}
	return -1
}

// rewindCleanupArtifacts drops files that only made sense while the truncated
// tail still existed. Everything here is best-effort: a leftover file is
// harmless, a failed removal must not fail the rewind.
func rewindCleanupArtifacts(sessionDir string, msgs []llm.Message) {
	// Legacy fork bookkeeping and per-turn file diffs have no readers left.
	_ = os.Remove(filepath.Join(sessionDir, "branches.json"))
	_ = os.RemoveAll(filepath.Join(sessionDir, "diffs"))

	ids := toolCallIDsInMessages(msgs)

	// A pending gate whose tool call left the transcript would resurrect a
	// phantom permission prompt on the next load.
	if rec, err := ReadPendingPermission(sessionDir); err == nil && rec != nil {
		if _, ok := ids[rec.ToolCall.ToolCallID]; !ok {
			_ = ClearPendingPermission(sessionDir)
		}
	}

	// Drop persisted tool-call detail for calls that left the transcript.
	keep := make(map[string]struct{}, len(ids))
	for id := range ids {
		keep[ToolCallDirName(id)] = struct{}{}
	}
	if dirs, err := ListToolCalls(sessionDir); err == nil {
		for _, d := range dirs {
			if _, ok := keep[d]; !ok {
				_ = os.RemoveAll(filepath.Join(sessionDir, toolCallsDirName, d))
			}
		}
	}
}

// toolCallIDsInMessages collects every tool call id the messages reference:
// the calls an assistant message requests and the results a tool message answers.
func toolCallIDsInMessages(msgs []llm.Message) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if tc.ID != "" {
				ids[tc.ID] = struct{}{}
			}
		}
		if m.ToolCallID != "" {
			ids[m.ToolCallID] = struct{}{}
		}
	}
	return ids
}
