package session

import "sync"

// stubTurnMu provides a non-blocking per-session prompt lock when there is no
// persisted bundle directory or on platforms without flock.
func (m *Manager) acquireStubTurnLock(sessionID string) (unlock func(), err error) {
	v, _ := m.stubTurnMu.LoadOrStore(sessionID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, ErrSessionTurnBusy
	}
	return func() { mu.Unlock() }, nil
}
