//go:build cli

package cli

import (
	"context"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// latestBackendSessionID resolves -c/--continue: the newest session recorded
// for this folder in the local store, or the newest one on the remote server
// (whose sessions live in its own workspace, so no folder filter applies).
// includePrintRuns counts the runs of one-shot print mode: `coddy -c -p`
// continues the previous print run, while the interactive console never opens
// one, the way it never lists one in its picker.
func latestBackendSessionID(ctx context.Context, mgr backend, cwd string, includePrintRuns bool) (string, error) {
	if store := mgr.FileStore(); store != nil {
		return latestSessionID(store, cwd, includePrintRuns)
	}
	if r, ok := mgr.(interface {
		LatestSessionID(ctx context.Context, includePrint bool) (string, error)
	}); ok {
		return r.LatestSessionID(ctx, includePrintRuns)
	}
	return "", fmt.Errorf("this backend cannot name its latest session")
}

// latestSessionID returns the most recently updated persisted session whose
// recorded cwd matches this folder (ListSnapshotsWith sorts newest first).
func latestSessionID(store *session.FileStore, cwd string, includePrintRuns bool) (string, error) {
	if store == nil {
		return "", fmt.Errorf("no session store")
	}
	entries, err := store.ListSnapshotsWith(session.ListOptions{CWD: cwd, IncludePrintRuns: includePrintRuns})
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no previous session in %s (start one with `coddy` or pick any with --resume)", cwd)
	}
	return entries[0].SessionID, nil
}
