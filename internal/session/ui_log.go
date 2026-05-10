package session

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// UILogEntry is a UI-facing transcript row (shown in HTTP UI after reload).
// It is never included in GetMessages / model prompts.
type UILogEntry struct {
	ID            string `json:"id"`
	Level         string `json:"level"`
	Message       string `json:"message"`
	UserTurnIndex int    `json:"userTurnIndex"`
	CreatedAt     string `json:"createdAt"`
}

// CountUserTurns counts llm.RoleUser messages in order (matches memory copilot turn index).
func CountUserTurns(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			n++
		}
	}
	return n
}

func newUILogID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "ulog_" + hex.EncodeToString(b[:])
}

// GetUILog returns a copy of persisted UI log entries.
func (s *State) GetUILog() []UILogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.UILog) == 0 {
		return nil
	}
	out := make([]UILogEntry, len(s.UILog))
	copy(out, s.UILog)
	return out
}

// AppendUILogError records a user-visible error tied to the given 1-based user turn index.
func (s *State) AppendUILogError(userTurnIndex int, message string) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "Request failed"
	}
	if userTurnIndex < 1 {
		userTurnIndex = 1
	}
	s.mu.Lock()
	s.UILog = append(s.UILog, UILogEntry{
		ID:            newUILogID(),
		Level:         "error",
		Message:       msg,
		UserTurnIndex: userTurnIndex,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	})
	s.mu.Unlock()
	s.touchPersist()
}

// RestoreUILogWithoutPersist replaces the UI log from disk (session load).
func (s *State) RestoreUILogWithoutPersist(entries []UILogEntry) {
	s.mu.Lock()
	if len(entries) == 0 {
		s.UILog = nil
	} else {
		s.UILog = append([]UILogEntry(nil), entries...)
	}
	s.mu.Unlock()
}
