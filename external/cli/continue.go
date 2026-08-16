//go:build cli

package cli

import (
	"context"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// latestBackendSessionID resolves -c/--continue: the newest snapshot recorded
// for this folder locally, or the remote server's newest session when the
// backend keeps no local store.
func latestBackendSessionID(ctx context.Context, mgr backend, cwd string) (string, error) {
	if store := mgr.FileStore(); store != nil {
		return latestSessionID(store, cwd)
	}
	res, err := mgr.HandleSessionList(ctx, acp.SessionListParams{})
	if err != nil {
		return "", fmt.Errorf("list remote sessions: %w", err)
	}
	if res == nil || len(res.Sessions) == 0 {
		return "", fmt.Errorf("no previous session on the remote server (run one first, e.g. coddy --remote <server> -p \"...\")")
	}
	return res.Sessions[0].SessionID, nil
}

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
