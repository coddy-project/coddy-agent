package session

import (
	"context"
	"strings"
	"time"
)

func (m *Manager) runCrossProcessCancelPoll(turnCtx context.Context, st *State, sessionDir string) {
	dir := strings.TrimSpace(sessionDir)
	if dir == "" {
		return
	}
	ticker := time.NewTicker(280 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-turnCtx.Done():
			return
		case <-ticker.C:
			ok, err := CancelRequestExists(dir)
			if err != nil || !ok {
				continue
			}
			st.SetUserCancelledTurn()
			st.Cancel()
			_ = ClearCancelRequest(dir)
		}
	}
}
