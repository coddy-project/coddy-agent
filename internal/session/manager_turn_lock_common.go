package session

import "sync"

// stubTurnMu serializes prompt turns per session when there is no persisted bundle directory
// or on platforms without flock (see manager_turn_lock_unix.go / manager_turn_lock_stub.go).
func (m *Manager) acquireStubTurnLock(sessionID string) (unlock func(), _ error) {
	v, _ := m.stubTurnMu.LoadOrStore(sessionID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, ErrSessionTurnBusy
	}
	return func() { mu.Unlock() }, nil
}
