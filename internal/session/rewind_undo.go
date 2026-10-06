package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// rewindUndoDirName is the bundle folder that keeps the tail the last rewind cut.
const rewindUndoDirName = "rewind_undo"

// ErrRewindUndoUnavailable reports an undo with no rewind to take back: none
// was made, it was already undone, a prompt followed the edited turn, or the
// kept prefix changed since.
var ErrRewindUndoUnavailable = errors.New("no rewind to undo")

// rewindUndoSnapshot is the record of one rewind: what it cut, and how to
// tell the kept prefix is still the history the cut continued.
type rewindUndoSnapshot struct {
	// UserMessageIndex is the index the rewind was called with (0-based over
	// non-summary user messages): the prompt that was edited.
	UserMessageIndex int `json:"userMessageIndex"`
	// BaseLen is len(Messages) right after the cut: the kept prefix.
	BaseLen int `json:"baseLen"`
	// PrefixDigest fingerprints the kept prefix (see prefixDigest), so a
	// compaction or another rewrite of the prefix ends the undo.
	PrefixDigest string `json:"prefixDigest"`
	CreatedAt    string `json:"createdAt"`
	// Messages is the tail the rewind cut, in order. Kept in tail.json, apart
	// from the record: every transcript read asks whether the undo is still on
	// offer and needs only the fields above.
	Messages []llm.Message `json:"-"`
	// UILog is the ui log rows the rewind dropped, in order (tail.json too).
	UILog []UILogEntry `json:"-"`
}

// rewindUndoTail is the content of tail.json.
type rewindUndoTail struct {
	Messages []llm.Message `json:"messages"`
	UILog    []UILogEntry  `json:"uiLog,omitempty"`
}

// prefixDigest fingerprints a message prefix: role, the compaction flag,
// content and the tool-call ids of each message, in order, separated so no
// two message lists collide on a shared concatenation. The full JSON is not
// hashed on purpose - fields set after the fact would change it.
func prefixDigest(msgs []llm.Message) string {
	h := sha256.New()
	for _, m := range msgs {
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		if m.CompactionSummary {
			h.Write([]byte("1"))
		} else {
			h.Write([]byte("0"))
		}
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0})
		ids := make([]string, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			ids = append(ids, tc.ID)
		}
		h.Write([]byte(strings.Join(ids, ",")))
		h.Write([]byte{0})
		h.Write([]byte(m.ToolCallID))
		h.Write([]byte{0x1e})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeRewindUndoSnapshot stores the snapshot under dir: the tail first, the
// record last, so a record on disk always has its tail.
func writeRewindUndoSnapshot(dir string, snap *rewindUndoSnapshot) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(dir, "tail.json"), &rewindUndoTail{Messages: snap.Messages, UILog: snap.UILog}); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(dir, "snapshot.json"), snap)
}

// readRewindUndoSnapshot loads the record of a session bundle without its
// tail; a missing or unreadable one reads as nil - it just means no undo is
// on offer.
func readRewindUndoSnapshot(sessionDir string) *rewindUndoSnapshot {
	data, err := os.ReadFile(filepath.Join(sessionDir, rewindUndoDirName, "snapshot.json"))
	if err != nil {
		return nil
	}
	var snap rewindUndoSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil
	}
	return &snap
}

// readRewindUndoTail fills in the tail of a record read by
// readRewindUndoSnapshot.
func readRewindUndoTail(sessionDir string, snap *rewindUndoSnapshot) error {
	data, err := os.ReadFile(filepath.Join(sessionDir, rewindUndoDirName, "tail.json"))
	if err != nil {
		return err
	}
	var tail rewindUndoTail
	if err := json.Unmarshal(data, &tail); err != nil {
		return err
	}
	snap.Messages, snap.UILog = tail.Messages, tail.UILog
	return nil
}

// retireStaleRewindUndo removes a snapshot that no longer continues the
// history - a prompt followed the edited turn, or the prefix was rewritten -
// so the tail the operator moved on from does not stay in the bundle until
// the next rewind. Called with the session's prompt turn lock held, which a
// rewind takes too, so it never removes a snapshot a rewind is writing.
func retireStaleRewindUndo(st *State) {
	dir := strings.TrimSpace(st.GetPersistedSessionDir())
	if dir == "" {
		return
	}
	snap := readRewindUndoSnapshot(dir)
	if snap == nil || rewindUndoValid(st.GetMessages(), snap) {
		return
	}
	_ = os.RemoveAll(filepath.Join(dir, rewindUndoDirName))
}

// rewindUndoValid reports whether the snapshot still continues the history:
// the kept prefix is intact (at least as long as the cut left it, same
// digest) and at most one user-role row follows it - the edited prompt
// itself. A second one (a prompt after the edited turn, a background wake,
// a compaction summary) ends the undo.
func rewindUndoValid(msgs []llm.Message, snap *rewindUndoSnapshot) bool {
	if len(msgs) < snap.BaseLen {
		return false
	}
	if prefixDigest(msgs[:snap.BaseLen]) != snap.PrefixDigest {
		return false
	}
	userRows := 0
	for _, m := range msgs[snap.BaseLen:] {
		if m.Role == llm.RoleUser {
			userRows++
			if userRows > 1 {
				return false
			}
		}
	}
	return true
}

// RewindUndoAvailable reports whether the last rewind of the session can be
// taken back, and the index of the user message it edited.
func (m *Manager) RewindUndoAvailable(sessionID string) (userMessageIndex int, ok bool) {
	st := m.getSession(sessionID)
	if st == nil {
		return 0, false
	}
	dir := strings.TrimSpace(st.GetPersistedSessionDir())
	if dir == "" {
		return 0, false
	}
	snap := readRewindUndoSnapshot(dir)
	if snap == nil {
		return 0, false
	}
	if !rewindUndoValid(st.GetMessages(), snap) {
		return 0, false
	}
	return snap.UserMessageIndex, true
}

// UndoRewind takes the last rewind back: whatever was appended after the cut
// (the edited prompt and its turn) is dropped, the cut tail and its ui log
// rows and tool call detail come back, and the snapshot is spent. Returns the
// new messages revision.
func (m *Manager) UndoRewind(sessionID string) (uint64, error) {
	if m.store == nil || m.store.Root == "" {
		return 0, fmt.Errorf("session store unavailable")
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
	// The same refusal as the rewind: a turn in flight would append onto a
	// history that no longer matches what it was started on.
	if m.SessionTurnActiveInProcess(sessionID) || TurnLockHeld(dir) {
		return 0, ErrSessionTurnActive
	}
	unlock, err := m.acquirePromptTurnLock(sessionID, st)
	if err != nil {
		if errors.Is(err, ErrSessionTurnBusy) {
			return 0, ErrSessionTurnActive
		}
		return 0, err
	}
	defer unlock()
	snap := readRewindUndoSnapshot(dir)
	if snap == nil {
		return 0, ErrRewindUndoUnavailable
	}
	undoDir := filepath.Join(dir, rewindUndoDirName)
	if !rewindUndoValid(st.GetMessages(), snap) || readRewindUndoTail(dir, snap) != nil {
		// A snapshot that no longer continues the history is retired rather
		// than kept around to refuse again on every read.
		_ = os.RemoveAll(undoDir)
		return 0, ErrRewindUndoUnavailable
	}
	if err := st.restoreRewoundTail(snap.BaseLen, snap.Messages, snap.UILog, func() bool {
		return m.SessionTurnActiveInProcess(sessionID)
	}); err != nil {
		return 0, err
	}
	// The restored history must reach disk before the parked tool-call
	// detail moves back: the same ordering the rewind itself keeps.
	if err := m.store.Save(st); err != nil {
		return 0, fmt.Errorf("persist restored history: %w", err)
	}
	restoreRewoundToolCalls(dir, st.GetMessages())
	// A permission prompt the cancelled edited turn was waiting on names a call
	// that left the history; kept, it would come back as a phantom prompt on
	// the next load (the rewind clears it the same way).
	if rec, err := ReadPendingPermission(dir); err == nil && rec != nil {
		if _, ok := toolCallIDsInMessages(st.GetMessages())[rec.ToolCall.ToolCallID]; !ok {
			_ = ClearPendingPermission(dir)
		}
	}
	_ = os.RemoveAll(undoDir)
	return st.MessagesRev(), nil
}

// restoreRewoundTail replaces everything appended after baseLen - the edited
// prompt and its turn - with the tail a rewind kept. turnActive is re-checked
// under the state lock, like TruncateMessagesBeforeUserN does, so a turn
// admitted after the caller's own check still refuses.
func (s *State) restoreRewoundTail(baseLen int, tail []llm.Message, tailLog []UILogEntry, turnActive func() bool) error {
	s.mu.Lock()
	if turnActive != nil && turnActive() {
		s.mu.Unlock()
		return ErrSessionTurnActive
	}
	if len(s.Messages) < baseLen {
		s.mu.Unlock()
		return ErrRewindUndoUnavailable
	}
	// The keep bound of the ui log is the turn count of the kept prefix; it
	// is counted before the tail is appended (the same numbering the cut
	// used), and the rows the edited turn earned go away with it.
	uilogBound := CountUserTurns(s.Messages[:baseLen])
	s.Messages = append(append([]llm.Message(nil), s.Messages[:baseLen]...), tail...)
	kept := s.UILog[:0]
	for _, e := range s.UILog {
		if e.UserTurnIndex <= uilogBound {
			kept = append(kept, e)
		}
	}
	s.UILog = append(kept, tailLog...)
	s.markMessagesEdited()
	s.mu.Unlock()
	s.touchPersist()
	return nil
}

// restoreRewoundToolCalls brings the detail of the calls the cut tail made
// back into tool_calls/ and drops the detail of calls only the edited turn
// made. Best-effort throughout, like the rewind's own cleanup: a leftover
// folder is harmless, a failed move must not fail the undo.
func restoreRewoundToolCalls(sessionDir string, msgs []llm.Message) {
	ids := toolCallIDsInMessages(msgs)
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
	parked, err := ListToolCalls(filepath.Join(sessionDir, rewindUndoDirName))
	if err != nil || len(parked) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Join(sessionDir, toolCallsDirName), 0o755); err != nil {
		return
	}
	for _, d := range parked {
		dst := filepath.Join(sessionDir, toolCallsDirName, d)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		_ = os.Rename(filepath.Join(sessionDir, rewindUndoDirName, toolCallsDirName, d), dst)
	}
}
