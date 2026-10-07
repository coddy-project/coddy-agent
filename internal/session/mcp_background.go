package session

// A session's configured MCP servers can connect in the background, after
// session/new or a load from disk has returned: the progress record
// (MCPConnectUpdate), the worker that dials them, the control update the
// console renders, and the wait a turn makes for its tool list
// (WaitMCPConnect on State). Only the interactive console turns this on;
// every other surface keeps connecting a new session's servers in
// session/new and a restored session's before its first turn.

import (
	"context"
	"errors"

	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// The states of one configured MCP server while a session connects them in
// the background (MCPServerConnect.State).
const (
	MCPConnectStateConnecting = "connecting"
	MCPConnectStateConnected  = "connected"
	MCPConnectStateFailed     = "failed"
	MCPConnectStateHeld       = "held"
	// MCPConnectStateCancelled is a server whose dial a settings reload or a
	// teardown ended before it settled: the reload dials it again, so there
	// is nothing to report about it.
	MCPConnectStateCancelled = "cancelled"
)

// MCPServerConnect is one configured server's place in a background connect.
type MCPServerConnect struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// Tools is how many tools the server offered once connected.
	Tools int `json:"tools,omitempty"`
	// Error says why a failed server did not connect.
	Error string `json:"error,omitempty"`
	// Hint is what comes next: how to approve a held server, or that a
	// server that did not answer in time is tried once more.
	Hint string `json:"hint,omitempty"`
}

// MCPConnectUpdate is the progress of a session's background MCP connect,
// sent to the surface that owns the session as a control update - never on
// the ACP wire. Done says the dial has settled for every server; the tool
// list is complete from then on.
type MCPConnectUpdate struct {
	Servers []MCPServerConnect `json:"servers"`
	Done    bool               `json:"done"`
	// Generation orders the snapshots of a session: it grows with every
	// change of the record - a server settling, the connect finishing, a
	// reload cancelling it. A surface drops a snapshot older than the last
	// it applied: two senders (the connect's notifier, the reload that
	// supersedes it) can each read a snapshot and deliver it in the other
	// order, and the footer would otherwise go back to counting.
	Generation uint64 `json:"generation"`
}

// Counts reports how many servers are connected out of the ones that could
// be: a server the trust gate holds is not counted, since nothing is dialing
// it, and neither is one whose dial was cancelled.
func (u MCPConnectUpdate) Counts() (connected, total int) {
	for _, s := range u.Servers {
		if s.State == MCPConnectStateHeld || s.State == MCPConnectStateCancelled {
			continue
		}
		total++
		if s.State == MCPConnectStateConnected {
			connected++
		}
	}
	return connected, total
}

func (u MCPConnectUpdate) clone() MCPConnectUpdate {
	out := MCPConnectUpdate{Done: u.Done, Generation: u.Generation}
	if u.Servers != nil {
		out.Servers = append([]MCPServerConnect(nil), u.Servers...)
	}
	return out
}

// mcpRetryHint is what a failed server's row says when the server is tried
// once more (noteConfiguredDial).
const mcpRetryHint = "The next prompt tries it once more"

// controlUpdateSender is the optional in-process surface boundary a background
// connect reports through. The console implements it; the ACP server and the
// HTTP relay do not, and they never connect in the background either.
type controlUpdateSender interface {
	SendControlUpdate(sessionID string, update any) error
}

// SetBackgroundMCPConnect makes the sessions this manager opens from now on -
// new ones and ones restored from disk - connect their configured MCP servers
// in the background: session/new or session/load returns as soon as the
// state exists, the dial runs in a worker owned by the state, and a turn
// waits for it at admission (WaitMCPConnect). Only the console turns it on:
// it draws its first frame while the servers come up, and it opens a stored
// session only to continue it. The surfaces that promise connected servers
// when session/new returns keep that promise, and the ones that load a stored
// session to read it start nothing until its first turn.
func (m *Manager) SetBackgroundMCPConnect(on bool) {
	m.backgroundMCP.Store(on)
}

// BackgroundMCPConnect reports whether the manager connects the configured
// MCP servers of the sessions it opens in the background.
func (m *Manager) BackgroundMCPConnect() bool {
	return m.backgroundMCP.Load()
}

// ActivateDeferredMCP explicitly warms the configured MCP servers of a
// restored ordinary session. Passive session reads leave the deferred marker
// untouched; this method is the one opt-in transition that consumes it. The
// turn lock makes consuming the marker and registering the background
// generation atomic with reload and workspace reconciliation.
func (m *Manager) ActivateDeferredMCP(ctx context.Context, sessionID string) error {
	state := m.SessionByID(sessionID)
	if state == nil {
		return errors.New("session not found")
	}
	if state.GetArchived() || state.Subagent() != nil || state.IsSchedulerJob() {
		return nil
	}
	unlock, err := m.acquireTurnLockWithReloadDrain(sessionID, state)
	if err != nil {
		return err
	}
	defer unlock()
	if !state.takeDeferredConfiguredMCP() {
		return nil
	}
	m.installMCPFilter(state)
	m.startBackgroundMCPConnect(state)
	return nil
}

// connectNewSessionMCPServers is the configured-server step of a new
// session: the tool filter, then the dial - in the background when the
// manager connects there, in the caller's context otherwise.
func (m *Manager) connectNewSessionMCPServers(ctx context.Context, state *State) {
	if m.backgroundMCP.Load() {
		m.installMCPFilter(state)
		m.startBackgroundMCPConnect(state)
		return
	}
	m.connectConfiguredMCPServers(ctx, state)
}

// startBackgroundMCPConnect dials the session's configured servers in a
// worker the state owns and reports each server as it settles. The worker's
// context is the state's, not the request's: session/new has returned by the
// time most servers answer. A reload or a teardown cancels it, and a result
// that lands after either is closed rather than installed. Every server is
// held to the per-server timeout; one that did not answer within it is tried
// once more at the session's next turn (noteConfiguredDial), and one that
// failed otherwise is reported once and not dialed again.
//
// The surface hears about it from a goroutine of its own, never from inside
// the dial: a surface slow to take an update must not keep the dial from
// settling, since a turn waits for that (WaitMCPConnect).
func (m *Manager) startBackgroundMCPConnect(state *State) {
	cwd := state.GetCWD()
	targets, held := m.configuredTargets(m.activeCfg(), cwd)
	servers := make([]MCPServerConnect, 0, len(targets)+len(held))
	for _, t := range targets {
		servers = append(servers, MCPServerConnect{Name: t.Server.Config.Name, State: MCPConnectStateConnecting})
	}
	for _, h := range held {
		m.logHeld(cwd, h)
		entry := MCPServerConnect{Name: h.Server.Config.Name, State: MCPConnectStateHeld}
		if h.Blocked != nil {
			entry.Error = h.Blocked.Error()
			entry.Hint = "approve it with: coddy mcp trust " + h.Server.Config.Name
		} else if h.Err != nil {
			entry.Error = h.Err.Error()
		}
		servers = append(servers, entry)
	}
	gen, ctx, ok := state.beginBackgroundMCP(servers)
	if !ok {
		return
	}
	// One update in flight at a time, the newest state read when it is sent:
	// a signal that finds one pending is dropped, and the pending one reads
	// what is current by then. The first one says what is being dialed, so
	// not even session/new waits for the surface.
	notify := make(chan struct{}, 1)
	go func() {
		for range notify {
			m.sendMCPConnectUpdate(state)
		}
	}()
	signal := func() {
		select {
		case notify <- struct{}{}:
		default:
		}
	}
	signal()
	if len(targets) == 0 {
		state.finishBackgroundMCP(gen, nil)
		signal()
		close(notify)
		return
	}
	go func() {
		defer close(notify)
		results := m.dialConcurrently(ctx, targets, func(i int, r mcpDialResult) {
			if r.CutShort {
				// Superseded: the reload or the teardown that cancelled the
				// dial is what the session runs now.
				return
			}
			var blocked *mcp.BlockedError
			if errors.As(r.Err, &blocked) {
				// The gate checks again right before the spawn, and refused:
				// the approval was withdrawn after the dial was planned. That
				// is a server no longer admitted, as when the withdrawal lands
				// after the spawn (below), not one that failed.
				if state.dropBackgroundMCPEntry(gen, i) {
					m.log.Info("MCP server not started: not admitted any more when its dial began",
						"server", r.Target.Server.Config.Name, "session", state.GetID())
					signal()
				}
				return
			}
			entry := MCPServerConnect{Name: r.Target.Server.Config.Name, State: MCPConnectStateConnected}
			if r.Err != nil {
				entry.State, entry.Error = MCPConnectStateFailed, r.Err.Error()
			} else {
				entry.Tools = len(r.Client.Tools())
			}
			// The targets come first in servers, in the same order.
			taken, retry, gaveUp := state.settleBackgroundMCP(gen, i, entry, r.Err)
			if !taken {
				return
			}
			m.logDial(r)
			m.logNoAnswer(state, entry.Name, retry, gaveUp)
			signal()
		})
		// A switch or a trust change that landed while the servers were
		// dialing is applied now: a server no longer enabled, or no longer
		// admitted for the workspace, is not installed. One the gate could
		// not decide on this time (an error reading its approvals) is not
		// installed either - the gate fails closed - but is parked, so the
		// next turn decides again instead of the server staying down.
		admitted := make(map[string]bool, len(targets))
		undecided := map[string]bool{}
		now, nowHeld := m.configuredTargets(m.activeCfg(), state.GetCWD())
		for _, t := range now {
			admitted[t.Server.Config.Name] = true
		}
		for _, h := range nowHeld {
			if h.Blocked == nil && h.Err != nil {
				undecided[h.Server.Config.Name] = true
			}
		}
		clients := make([]*mcp.Client, 0, len(results))
		for i, r := range results {
			if r.Err != nil || r.Client == nil {
				continue
			}
			name := r.Target.Server.Config.Name
			if !admitted[name] {
				_ = r.Client.Close()
				state.dropBackgroundMCPEntry(gen, i)
				if undecided[name] {
					state.markMCPServerPending(name)
				}
				m.log.Info("MCP server not installed: switched off or not admitted any more while it was connecting",
					"server", name, "session", state.GetID(), "retried_next_turn", undecided[name])
				continue
			}
			clients = append(clients, r.Client)
		}
		if state.finishBackgroundMCP(gen, clients) {
			signal()
		}
	}()
}

// supersedeBackgroundMCP ends a background connect still running for the
// session before something else dials its configured servers - a settings
// reload - and tells the surface how the connect it was following ended,
// whose footer would otherwise count servers nobody dials any more. It tells
// it even when the connect had already finished: the reload clears the
// record next (replaceConfiguredMCPClients), and the connect's own last
// update may not have gone out by then.
func (m *Manager) supersedeBackgroundMCP(st *State) {
	st.cancelBackgroundMCPConnect()
	m.sendMCPConnectUpdate(st)
}

// sendMCPConnectUpdate hands the current snapshot to the surface, if it
// listens for control updates.
func (m *Manager) sendMCPConnectUpdate(state *State) {
	sender, ok := m.server.(controlUpdateSender)
	if !ok {
		return
	}
	snap, recorded := state.MCPConnectSnapshot()
	if !recorded {
		return
	}
	_ = sender.SendControlUpdate(state.GetID(), snap)
}
