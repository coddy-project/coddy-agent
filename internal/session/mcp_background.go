package session

// A session's configured MCP servers can connect in the background, after
// session/new has returned: the progress record (MCPConnectUpdate), the
// worker that dials them, the control update the console renders, and the
// wait a turn makes for its tool list (WaitMCPConnect on State). Only the
// interactive console turns this on; every other surface keeps connecting
// in session/new.

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// The states of one configured MCP server while a session connects them in
// the background (MCPServerConnect.State).
const (
	MCPConnectStateConnecting = "connecting"
	MCPConnectStateConnected  = "connected"
	MCPConnectStateFailed     = "failed"
	MCPConnectStateHeld       = "held"
)

// MCPServerConnect is one configured server's place in a background connect.
type MCPServerConnect struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// Tools is how many tools the server offered once connected.
	Tools int `json:"tools,omitempty"`
	// Error says why a failed server did not connect.
	Error string `json:"error,omitempty"`
	// Hint is what the operator can do about a failed or held server.
	Hint string `json:"hint,omitempty"`
}

// MCPConnectUpdate is the progress of a session's background MCP connect,
// sent to the surface that owns the session as a control update - never on
// the ACP wire. Done says the dial has settled for every server; the tool
// list is complete from then on.
type MCPConnectUpdate struct {
	Servers []MCPServerConnect `json:"servers"`
	Done    bool               `json:"done"`
}

// Counts reports how many servers are connected out of the ones that could
// be: a server the trust gate holds is not counted, since nothing is dialing
// it.
func (u MCPConnectUpdate) Counts() (connected, total int) {
	for _, s := range u.Servers {
		if s.State == MCPConnectStateHeld {
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
	out := MCPConnectUpdate{Done: u.Done}
	if u.Servers != nil {
		out.Servers = append([]MCPServerConnect(nil), u.Servers...)
	}
	return out
}

// controlUpdateSender is the optional in-process surface boundary a background
// connect reports through. The console implements it; the ACP server and the
// HTTP relay do not, and they never defer connects either.
type controlUpdateSender interface {
	SendControlUpdate(sessionID string, update any) error
}

// SetBackgroundMCPConnect makes sessions created from now on connect their
// configured MCP servers in the background: session/new returns as soon as
// the state exists, the dial runs in a worker owned by the state, and a turn
// waits for it at admission (WaitMCPConnect). Only the console turns it on -
// it draws its first frame while the servers come up - so the surfaces that
// promise connected servers when session/new returns keep that promise.
func (m *Manager) SetBackgroundMCPConnect(on bool) {
	m.backgroundMCP.Store(on)
}

// BackgroundMCPConnect reports whether the manager defers configured MCP
// connects.
func (m *Manager) BackgroundMCPConnect() bool {
	return m.backgroundMCP.Load()
}

// installMCPFilterFactory gives the state its per-turn MCP tool filter. The
// factory reads the cwd lazily: a later workspace switch must not leave the
// filter evaluating the old workspace's merged list.
func (m *Manager) installMCPFilterFactory(state *State) {
	state.MCPFilterFactory = func() func(server, tool string) bool {
		return config.BuildMCPToolFilter(EffectiveMCPServers(m.activeCfg(), state.GetCWD(), m.log))
	}
}

// startBackgroundMCPConnect dials the session's configured servers in a
// worker the state owns and reports each server as it settles. The worker's
// context is the state's, not the request's: session/new has returned by the
// time most servers answer. A reload or a teardown cancels it, and a result
// that lands after either is closed rather than installed.
func (m *Manager) startBackgroundMCPConnect(state *State) {
	cwd := state.GetCWD()
	targets, held := m.configuredTargets(m.activeCfg(), cwd)
	servers := make([]MCPServerConnect, 0, len(targets)+len(held))
	targetAt := make([]int, len(targets))
	for i, t := range targets {
		targetAt[i] = len(servers)
		servers = append(servers, MCPServerConnect{Name: t.Server.Config.Name, State: MCPConnectStateConnecting})
	}
	for _, h := range held {
		m.logHeld(cwd, h)
		entry := MCPServerConnect{Name: h.Server.Config.Name, State: MCPConnectStateHeld}
		if h.Blocked != nil {
			entry.Error = h.Blocked.Error()
			entry.Hint = "approve it with: coddy mcp trust " + h.Server.Config.Name
		}
		servers = append(servers, entry)
	}
	gen, ctx := state.beginBackgroundMCP(servers)
	if len(targets) == 0 {
		state.finishBackgroundMCP(gen, nil)
		m.sendMCPConnectUpdate(state)
		return
	}
	m.sendMCPConnectUpdate(state)
	go func() {
		results := m.dialConcurrently(ctx, targets, func(i int, r mcpDialResult) {
			m.logDial(r)
			entry := MCPServerConnect{Name: r.Target.Server.Config.Name, State: MCPConnectStateConnected}
			if r.Err != nil {
				entry.State, entry.Error = MCPConnectStateFailed, r.Err.Error()
				entry.Hint = mcp.UnpinnedHint(r.Target.Server.Config)
			} else {
				entry.Tools = len(r.Client.Tools())
			}
			if state.settleBackgroundMCP(gen, targetAt[i], entry) {
				m.sendMCPConnectUpdate(state)
			}
		})
		clients := make([]*mcp.Client, 0, len(results))
		for _, r := range results {
			if r.Err == nil && r.Client != nil {
				clients = append(clients, r.Client)
			}
		}
		if state.finishBackgroundMCP(gen, clients) {
			m.sendMCPConnectUpdate(state)
		}
	}()
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

// connectConfiguredMCPServersOrDefer is the configured-server step of session
// creation and of a load from disk: the tool filter always, then the dial -
// in the background when the manager defers it, in the caller's context
// otherwise.
func (m *Manager) connectConfiguredMCPServersOrDefer(ctx context.Context, state *State) {
	if m.backgroundMCP.Load() {
		m.installMCPFilterFactory(state)
		m.startBackgroundMCPConnect(state)
		return
	}
	m.connectConfiguredMCPServers(ctx, state)
}
