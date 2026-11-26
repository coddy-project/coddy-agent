//go:build !unix && !windows

package session

import "strings"

func (m *Manager) acquirePromptTurnLock(sessionID string, st *State) (unlock func(), _ error) {
	_ = strings.TrimSpace(st.GetPersistedSessionDir())
	return m.acquireStubTurnLock(sessionID)
}

// TurnLockHeld is unsupported on this platform; always false.
func TurnLockHeld(sessionDir string) bool {
	_ = sessionDir
	return false
}

func (m *Manager) acquireDeleteTurnLock(id, _ string) (func(), error) {
	return m.acquireStubTurnLock(id)
}
