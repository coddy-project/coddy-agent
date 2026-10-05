//go:build http

package httpserver

import (
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// liveDiffTTL is how long one comparison of a running turn is reused. The
// review window asks once per file, a few at a time, so without it a window over
// forty files would walk the workspace forty times for one look; the card asks
// at most once per finished tool call, further apart than this.
const liveDiffTTL = 300 * time.Millisecond

func (s *Server) snapshotTurnWorkspace(st *session.State) *session.WorkspaceSnapshot {
	// A configured session store may live inside the workspace. Capturing its
	// own transcript and diffs recursively would record bookkeeping as edits.
	var storeRoot string
	if store := s.mgr.FileStore(); store != nil {
		storeRoot = store.Root
	}
	return session.TakeWorkspaceSnapshot(st.GetCWD(), storeRoot, st.GetPersistedSessionDir())
}

// liveTurn is a turn this process is running: the workspace it runs in and the
// snapshot taken before it started. It lets the changed-files card, opened
// mid-turn, report what the turn has already written - the turn's own diff is
// only stored once it ends.
type liveTurn struct {
	cwd    string
	before *session.WorkspaceSnapshot

	mu       sync.Mutex
	cached   *session.WorkspaceDiff
	cachedAt time.Time
}

// diff compares the workspace with the pre-turn snapshot, reusing a comparison
// younger than liveDiffTTL.
func (t *liveTurn) diff() *session.WorkspaceDiff {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.cachedAt.IsZero() && time.Since(t.cachedAt) < liveDiffTTL {
		return t.cached
	}
	d, err := session.LiveWorkspaceDiff(t.cwd, t.before)
	if err != nil {
		d = nil
	}
	t.cached, t.cachedAt = d, time.Now()
	return d
}

// beginLiveTurn registers a turn that is about to run. Both profile doors hold
// the session's turn lock before they get here, so there is one per session.
func (s *Server) beginLiveTurn(sessionID, cwd string, before *session.WorkspaceSnapshot) *liveTurn {
	t := &liveTurn{cwd: cwd, before: before}
	s.liveTurnMu.Lock()
	defer s.liveTurnMu.Unlock()
	if s.liveTurns == nil {
		s.liveTurns = make(map[string]*liveTurn)
	}
	s.liveTurns[sessionID] = t
	return t
}

// endLiveTurn retires a turn's entry. It only removes that very entry, so a
// late call can never retire the turn that came after it.
func (s *Server) endLiveTurn(sessionID string, t *liveTurn) {
	if t == nil {
		return
	}
	s.liveTurnMu.Lock()
	defer s.liveTurnMu.Unlock()
	if s.liveTurns[sessionID] == t {
		delete(s.liveTurns, sessionID)
	}
}

// liveTurnDiff reports what the running turn of a session has changed so far,
// and whether a turn is running at all.
func (s *Server) liveTurnDiff(sessionID string) (*session.WorkspaceDiff, bool) {
	s.liveTurnMu.Lock()
	t := s.liveTurns[sessionID]
	s.liveTurnMu.Unlock()
	if t == nil {
		return nil, false
	}
	return t.diff(), true
}

// settleTurnDiff records what a finished turn did to the workspace, retires its
// live entry and tells the clients the change set is settled.
//
// A turn that failed after it ran - the provider dropped the answer halfway -
// still wrote what it wrote, so it is captured like one that finished. A turn
// that never ran (the session was busy, a child transcript is read-only) is
// told apart by the user-turn count, which only a turn that reached the agent
// moves; a diff taken for it would file whatever else changed in the folder
// under the previous turn's number.
func (s *Server) settleTurnDiff(st *session.State, before *session.WorkspaceSnapshot, live *liveTurn, turnsBefore int, _ error) {
	if session.CountUserTurns(st.GetMessages()) <= turnsBefore {
		id := st.GetID()
		s.endLiveTurn(id, live)
		s.publishSessionChanges(id)
		return
	}
	s.captureAndStoreTurnDiff(st, before, live)
}

// captureAndStoreTurnDiff computes and stores the diff while the caller still
// holds the session turn lock. A later turn or rollback must not change either
// the workspace or the turn number before this capture has finished.
//
// The turn's live entry is retired only once the diff is on disk, and then the
// clients are told the change set settled: a card that reads in between still
// sees the turn through the live entry, and one that reads after the event sees
// it stored - there is no moment where the turn's edits are in neither place.
func (s *Server) captureAndStoreTurnDiff(st *session.State, before *session.WorkspaceSnapshot, live *liveTurn) {
	id := st.GetID()
	sd := strings.TrimSpace(st.GetPersistedSessionDir())
	cwd := strings.TrimSpace(st.GetCWD())
	if live != nil {
		cwd = live.cwd
	}
	if sd == "" || cwd == "" {
		s.endLiveTurn(id, live)
		s.publishSessionChanges(id)
		return
	}
	defer s.publishSessionChanges(id)
	defer s.endLiveTurn(id, live)
	turnN := session.CountUserTurns(st.GetMessages())
	diff, err := session.ComputeWorkspaceDiff(cwd, before)
	if err != nil {
		s.log.Warn("compute workspace diff", "turn", turnN, "error", err)
		return
	}
	if err := session.StoreWorkspaceDiff(sd, turnN, diff); err != nil {
		s.log.Warn("store workspace diff", "turn", turnN, "error", err)
	}
}
