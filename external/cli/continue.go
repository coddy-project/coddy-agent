//go:build cli

package cli

import (
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// latestSessionID returns the most recently updated persisted session whose
// recorded cwd matches this folder (ListSnapshots sorts newest first).
func latestSessionID(store *session.FileStore, cwd string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("no session store")
	}
	entries, err := store.ListSnapshots(cwd, false)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no previous session in %s (start one with `coddy` or pick any with --resume)", cwd)
	}
	return entries[0].SessionID, nil
}
