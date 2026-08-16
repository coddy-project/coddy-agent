package session

import "strings"

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
	m.activeTurnMu.Unlock()
	if first {
		m.publishTurnEvent(id, TurnPhaseStarted)
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
