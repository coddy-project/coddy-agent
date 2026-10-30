package session

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// Levels of a UILogEntry.
const (
	// UILogLevelError is a failed request or turn; the SPA renders it with a
	// retry control.
	UILogLevelError = "error"
	// UILogLevelNotice is information the operator should see once, such as
	// a project hooks file that is held until approved; no retry.
	UILogLevelNotice = "notice"
)

// UILogEntry is a UI-facing transcript row (shown in HTTP UI after reload).
// It is never included in GetMessages / model prompts.
type UILogEntry struct {
	ID            string `json:"id"`
	Level         string `json:"level"`
	Message       string `json:"message"`
	UserTurnIndex int    `json:"userTurnIndex"`
	CreatedAt     string `json:"createdAt"`
	// Source is set on the notice of a settings change the agent made
	// itself: who made it (SettingsChange.Source). It is empty on every other
	// row, and on the settings notices of a session saved before only the
	// agent's changes were noted (VisibleUILog).
	Source     string             `json:"source,omitempty"`
	Generation *GenerationSummary `json:"generation,omitempty"`
}

// GenerationSummary is UI-only metadata, kept out of the model's history.
type GenerationSummary struct {
	OutputTokens    int     `json:"outputTokens"`
	TokensPerSecond float64 `json:"tokensPerSecond"`
	// ElapsedMs is how long the turn had run when it ended, the clock its live
	// line showed; absent on a row saved before it was kept.
	ElapsedMs int64 `json:"elapsedMs,omitempty"`
}

// GenerationProgress replays a saved average through the same event as live
// completion. Callers only use it for entries with a Generation summary.
func (row UILogEntry) GenerationProgress() acp.TurnProgressUpdate {
	return acp.TurnProgressUpdate{
		SessionUpdate:   acp.UpdateTypeTurnProgress,
		StartedAt:       strings.TrimPrefix(row.ID, "generation_"),
		ElapsedMs:       row.Generation.ElapsedMs,
		OutputTokens:    row.Generation.OutputTokens,
		TokensPerSecond: &row.Generation.TokensPerSecond,
		Completed:       true,
	}
}

// AppendGenerationSummary replaces a continued run's cumulative summary and
// places it after the latest answer. Several Agent.Run calls can share one
// admitted turn, identified by startedAt.
func (s *State) AppendGenerationSummary(startedAt time.Time, turn, tokens int, speed float64) {
	id := "generation_" + startedAt.UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	s.UILog = slices.DeleteFunc(s.UILog, func(row UILogEntry) bool { return row.ID == id })
	s.UILog = append(s.UILog, UILogEntry{
		ID: id, Level: UILogLevelNotice, Message: "Generation complete",
		UserTurnIndex: max(1, turn), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Generation: &GenerationSummary{
			OutputTokens:    tokens,
			TokensPerSecond: speed,
			ElapsedMs:       max(0, time.Since(startedAt).Milliseconds()),
		},
	})
	s.mu.Unlock()
	s.touchPersist()
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
	s.appendUILog(UILogLevelError, userTurnIndex, msg, "")
}

// AppendUILogNotice records a user-visible notice tied to the given 1-based
// user turn index. An empty message is dropped.
func (s *State) AppendUILogNotice(userTurnIndex int, message string) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return
	}
	s.appendUILog(UILogLevelNotice, userTurnIndex, msg, "")
}

// appendSettingsNotice records the notice of a settings change the agent made
// itself, with its source (noteSettingsChange).
func (s *State) appendSettingsNotice(userTurnIndex int, message, source string) {
	if msg := strings.TrimSpace(message); msg != "" {
		s.appendUILog(UILogLevelNotice, userTurnIndex, msg, source)
	}
}

func (s *State) appendUILog(level string, userTurnIndex int, msg, source string) {
	if userTurnIndex < 1 {
		userTurnIndex = 1
	}
	s.mu.Lock()
	s.UILog = append(s.UILog, UILogEntry{
		ID:            newUILogID(),
		Level:         level,
		Message:       msg,
		UserTurnIndex: userTurnIndex,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		Source:        source,
	})
	s.mu.Unlock()
	s.touchPersist()
}

// switchModelTool is the tool the model changes its own settings with
// (tools.ToolSwitchModel).
const switchModelTool = "switch_model"

// settingsNoticePart matches one part of a settings notice as settingsNotice
// writes it, "<setting>: <value> <scope>", in every scope a change is made
// for: the session, a number of turns (turnsScope), the rest of a turn
// (ApplyTurnSettings).
var settingsNoticePart = regexp.MustCompile(`^(Model|Reasoning|Mode|Permission mode): \S.* (for this session|for the next turn|for the next \d+ turns|for the rest of this turn)$`)

// VisibleUILog returns the rows of a session's log a transcript shows: every
// row but the notice of a settings change the operator made. Only the agent's
// own changes are noted now, with their source on the row; a session saved
// before holds a notice of every change, the model and the mode a console or
// `coddy -p` started it with included, and those rows are told apart by their
// text (operatorSettingsNotice).
func VisibleUILog(msgs []llm.Message, log []UILogEntry) []UILogEntry {
	if len(log) == 0 {
		return nil
	}
	out := make([]UILogEntry, 0, len(log))
	for _, e := range log {
		if !operatorSettingsNotice(msgs, e) {
			out = append(out, e)
		}
	}
	return out
}

// operatorSettingsNotice reports whether a row with no source is the notice of
// a settings change the operator made. The agent never changes the mode or the
// permission mode, nor anything for a number of turns; a change for the rest of
// a turn is its own (switch_model limited to the turn, a skill's frontmatter);
// the model or the reasoning set for the session is its own when the turn the
// row was logged in holds a switch_model call.
func operatorSettingsNotice(msgs []llm.Message, e UILogEntry) bool {
	if e.Source != "" || e.Level != UILogLevelNotice {
		return false
	}
	for _, part := range strings.Split(e.Message, "; ") {
		m := settingsNoticePart.FindStringSubmatch(part)
		switch {
		case m == nil:
			return false
		case m[2] == "for the rest of this turn":
			return false
		case m[2] != "for this session", m[1] == "Mode", m[1] == "Permission mode":
			return true
		}
	}
	return !turnCalledSwitchModel(msgs, e.UserTurnIndex)
}

// turnCalledSwitchModel reports whether the turn a row stamped t was logged in,
// from the t-th user-role message to the next one, holds a switch_model call.
func turnCalledSwitchModel(msgs []llm.Message, t int) bool {
	t = max(t, 1)
	users := 0
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			users++
			if users > t {
				return false
			}
			continue
		}
		if users < t || m.Role != llm.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.Name == switchModelTool {
				return true
			}
		}
	}
	return false
}

// MarkHookNoticeShown records that the notice named by key (a held or invalid
// hooks file) was shown in this live session and reports whether this call
// was the first, so the notice lands once rather than on every turn.
func (s *State) MarkHookNoticeShown(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hookNotices == nil {
		s.hookNotices = map[string]bool{}
	}
	if s.hookNotices[key] {
		return false
	}
	s.hookNotices[key] = true
	return true
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
