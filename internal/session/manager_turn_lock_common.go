package session

// acquireStubTurnLock provides a non-blocking per-session prompt lock when there is no
// persisted bundle directory or on platforms without flock.
func (m *Manager) acquireStubTurnLock(sessionID string) (unlock func(), err error) {
	if _, held := m.stubTurnLocks.LoadOrStore(sessionID, struct{}{}); held {
		return nil, ErrSessionTurnBusy
	}
	return func() { m.stubTurnLocks.Delete(sessionID) }, nil
}

// PromptTurnLockHeld reports whether a session's prompt lock is held, including
// the in-process fallback on platforms without flock. It does not acquire the lock.
func (m *Manager) PromptTurnLockHeld(st *State) bool {
	_, held := m.stubTurnLocks.Load(st.GetID())
	return held || TurnLockHeld(st.GetPersistedSessionDir())
}
