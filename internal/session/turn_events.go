package session

import (
	"sync"
	"time"
)

// TurnPhase is the edge a TurnEvent reports.
type TurnPhase string

const (
	// TurnPhaseStarted is published when a session goes from no turn to one.
	TurnPhaseStarted TurnPhase = "started"
	// TurnPhaseEnded is published when a session's last running turn releases.
	TurnPhaseEnded TurnPhase = "ended"
)

// TurnEvent announces that a session started or finished running a turn.
type TurnEvent struct {
	SessionID string
	Phase     TurnPhase
	At        time.Time
}

// AddTurnObserver registers fn for the edges of the in-process active-turn registry and
// returns the function that removes it again.
//
// It exists so a surface that cannot poll - an HTTP client watching a session it did not
// start - can be told a turn began instead of discovering it on the next list refresh.
// Because the registry is the single place every prompt turn passes through, an observer
// covers HTTP, ACP, the messenger gateway and the background waker at once.
//
// fn runs on the goroutine that is running the turn and MUST NOT block: deliver the event
// to a buffered channel and return. A remove function is handed back rather than exposing
// a single setter, because several servers may observe one manager and each must be able
// to detach without disturbing the others.
func (m *Manager) AddTurnObserver(fn func(TurnEvent)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	m.turnObserverMu.Lock()
	if m.turnObservers == nil {
		m.turnObservers = make(map[int]func(TurnEvent))
	}
	m.turnObserverSeq++
	id := m.turnObserverSeq
	m.turnObservers[id] = fn
	m.turnObserverMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.turnObserverMu.Lock()
			delete(m.turnObservers, id)
			m.turnObserverMu.Unlock()
		})
	}
}

// publishTurnEvent fans a phase edge out to the registered observers.
func (m *Manager) publishTurnEvent(sessionID string, phase TurnPhase) {
	m.turnObserverMu.Lock()
	fns := make([]func(TurnEvent), 0, len(m.turnObservers))
	for _, fn := range m.turnObservers {
		fns = append(fns, fn)
	}
	m.turnObserverMu.Unlock()
	if len(fns) == 0 {
		return
	}
	ev := TurnEvent{SessionID: sessionID, Phase: phase, At: time.Now().UTC()}
	for _, fn := range fns {
		// Delivered on the calling goroutine, so a session's started edge always reaches an
		// observer before its ended edge. That ordering is the whole point of the event, so
		// it outweighs isolating the turn from an observer that ignores the no-blocking
		// contract; observers hand the event to a buffered channel, as composerStreamRelay
		// already does for its subscribers.
		fn(ev)
	}
}

// MarkTurnActive registers a running turn for sessionID and returns its release closure.
//
// Exported for the turn paths that do not go through HandleSessionPromptWithSender - the
// HTTP permission-resume turn is one - so they appear in the activity flags and publish
// turn events like any other turn.
func (m *Manager) MarkTurnActive(sessionID string) func() {
	return m.markTurnActive(sessionID)
}

// ActiveTurnSessionIDs lists the sessions running a turn in this process.
//
// A client attaching to the event stream needs the turns already in flight as well as the
// ones that start later; without that snapshot a viewer connecting mid-turn stays blind
// until the turn ends.
func (m *Manager) ActiveTurnSessionIDs() []string {
	m.activeTurnMu.Lock()
	defer m.activeTurnMu.Unlock()
	if len(m.activeTurns) == 0 {
		return nil
	}
	ids := make([]string, 0, len(m.activeTurns))
	for id, n := range m.activeTurns {
		if n > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}
