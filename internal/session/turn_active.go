package session

import (
	"strings"
	"time"
)

// SessionTurnActiveInProcess reports whether a prompt turn for sessionID is running in
// THIS process.
//
// It complements TurnLockHeld rather than replacing it: the flock probe answers for other
// processes but is a no-op stub off unix, and a session with no persisted bundle has no
// lock file at all. Callers that need "is there anything to watch" should accept either.
func (m *Manager) SessionTurnActiveInProcess(sessionID string) bool {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return false
	}
	m.activeTurnMu.Lock()
	defer m.activeTurnMu.Unlock()
	return m.activeTurns[id] > 0
}

// TurnStartedAt reports when sessionID went from no turn to the one it is running in
// THIS process; ok is false when it runs none.
//
// A client that attaches to a turn in flight - a reloaded tab, a console reconnecting -
// counts the turn's clock from here instead of from the moment it attached.
func (m *Manager) TurnStartedAt(sessionID string) (time.Time, bool) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return time.Time{}, false
	}
	m.activeTurnMu.Lock()
	defer m.activeTurnMu.Unlock()
	at, ok := m.turnStarted[id]
	return at, ok
}

// markTurnActive registers a running turn for sessionID and returns its release closure.
//
// The registry counts turns instead of holding a set: HandleSessionPromptWithSender
// delegates to RunPlan for _meta and @plan prompts, so one logical turn marks the session
// twice, and a set would report the session idle the moment the inner run returned. The
// returned closure is idempotent, so a caller that both defers it and calls it explicitly
// cannot drive the count negative.
func (m *Manager) markTurnActive(sessionID string) func() {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return func() {}
	}
	m.activeTurnMu.Lock()
	if m.activeTurns == nil {
		m.activeTurns = make(map[string]int)
	}
	m.activeTurns[id]++
	first := m.activeTurns[id] == 1
	startedAt := time.Now().UTC()
	if first {
		if m.turnStarted == nil {
			m.turnStarted = make(map[string]time.Time)
		}
		m.turnStarted[id] = startedAt
	}
	m.activeTurnMu.Unlock()
	if first {
		m.publishTurnEventAt(id, TurnPhaseStarted, startedAt)
	}

	released := false
	return func() {
		m.activeTurnMu.Lock()
		if released {
			m.activeTurnMu.Unlock()
			return
		}
		released = true
		last := m.activeTurns[id] <= 1
		if last {
			delete(m.activeTurns, id)
			delete(m.turnStarted, id)
		} else {
			m.activeTurns[id]--
		}
		m.activeTurnMu.Unlock()
		// Only the outer release ends the turn: a prompt that delegates to RunPlan marks
		// the same session twice, and watchers must not be told it finished in between.
		if last {
			m.publishTurnEvent(id, TurnPhaseEnded)
		}
	}
}
