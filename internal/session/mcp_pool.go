package session

// The configured MCP servers of every session of a manager come from one
// pool (mcp.Pool): a server of the global configuration is one connection
// for the whole process, a project server one per workspace, and a session
// holds a lease on it rather than a process of its own. Everything that
// dials a configured server for a session - its creation, a restored
// session's first turn, a subagent's spawn, the console's background
// connect, a reload, a switch - reaches the pool through configuredTarget.
// This file holds what the manager does with the pool as a whole.

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// StartGlobalMCPServers starts the enabled servers of <home>/mcp.json now,
// before any session asks for them, and keeps them up for the life of the
// manager: a session that opens finds them connected, and one that closes
// does not stop them. The set follows the file from then on - a switch, an
// edit through a management surface, and an edit from anywhere else, which
// a watcher of the file picks up (ReloadMCPDeclarations) - and CloseMCP ends
// it. A global server whose declaration names the workspace (${CWD}) is not
// started here: it runs once per workspace, like a project server, for as
// long as a session of that workspace holds it.
//
// Long-running surfaces call it once they have built the manager (coddy
// serve, the console, coddy acp). A manager that never does shares its
// servers all the same and stops each one with the last session that held it.
func (m *Manager) StartGlobalMCPServers() {
	m.keepGlobalMCP.Store(true)
	m.syncGlobalMCPServers()
	m.watchMCPDeclarations()
}

// watchMCPDeclarations polls <home>/mcp.json until CloseMCP and reconciles
// the sessions with it whenever it moves. Started once per manager.
func (m *Manager) watchMCPDeclarations() {
	m.mcpWatchMu.Lock()
	defer m.mcpWatchMu.Unlock()
	if m.mcpWatchStop != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mcpWatchStop = cancel
	watcher := &config.StampWatcher{
		Path:    config.GlobalMCPJSONPath(m.activeCfg().Paths.Home),
		Changed: func() { m.ReloadMCPDeclarations(ctx) },
	}
	go func() {
		// The first poll is the baseline; what the file changed between the
		// manager's start and that baseline is caught up with by comparing
		// declarations, which costs one read when nothing moved.
		watcher.Poll()
		m.ReloadMCPDeclarations(ctx)
		_ = watcher.Run(ctx)
	}()
}

// syncGlobalMCPServers hands the pool the global servers it keeps: every
// enabled declaration of the global configuration with a transport Coddy
// speaks, except one that names the workspace. Without StartGlobalMCPServers
// it does nothing.
func (m *Manager) syncGlobalMCPServers() {
	if !m.keepGlobalMCP.Load() {
		return
	}
	var keep []mcp.ManagedServer
	for _, srv := range mcp.GlobalServers(m.activeCfg(), m.log) {
		if srv.Config.Disabled || mcp.WorkspaceBound(srv) || !mcp.SupportedTransport(mcp.EffectiveTransport(srv.Config)) {
			continue
		}
		keep = append(keep, srv)
	}
	m.mcpPool.Keep(keep)
}

// CloseMCP stops every MCP server of the manager's pool, the kept global ones
// included, and waits a bounded time for the stdio ones to exit. It is for a
// process on its way out: the sessions keep their leases, and the calls they
// make through them fail from then on.
func (m *Manager) CloseMCP() {
	m.mcpWatchMu.Lock()
	if m.mcpWatchStop != nil {
		m.mcpWatchStop()
	}
	m.mcpWatchMu.Unlock()
	m.mcpPool.Close()
}

// MCPPoolServers lists the servers the manager's pool runs: its name, the
// workspace a workspace-bound one runs for, how many sessions hold it and
// whether the pool keeps it.
func (m *Manager) MCPPoolServers() []mcp.PoolServer {
	return m.mcpPool.Running()
}

// ProbeMCPServer lists the tools of one configured server for the workspace
// through the trust gate and the manager's pool: a server that runs already
// answers from its connection, and one that does not is started for the
// probe and stopped after it, unless the pool keeps it.
func (m *Manager) ProbeMCPServer(ctx context.Context, srv mcp.ManagedServer, cwd string) ([]mcp.ToolInfo, error) {
	return mcp.NewTrustGate(m.activeCfg()).ProbeShared(ctx, m.mcpPool, srv, cwd, m.log)
}

// mcpServerWanted answers the pool's question about a server no session
// holds any more (mcp.Pool.SetWanted): is it still declared, as it runs,
// switched on and admitted for the workspace it runs for? A server that is,
// waits out mcp.idle_timeout_seconds for the next session to take it; one
// that is not - switched off, its approval withdrawn, removed or declared
// differently - is stopped at once. A server an ACP client sent is wanted
// for as long as the delay lasts: no file declares it.
func (m *Manager) mcpServerWanted(srv mcp.ManagedServer, workspace string) bool {
	if srv.Origin == mcp.OriginClient {
		return true
	}
	cfg := m.activeCfg()
	var declared []mcp.ManagedServer
	if mcp.WorkspaceBound(srv) {
		declared = mcp.ListManagedServersTolerant(cfg, workspace, m.log)
	} else {
		declared = mcp.GlobalServers(cfg, m.log)
	}
	key := mcp.PoolKey(srv, workspace)
	for _, cur := range declared {
		if cur.Config.Name != srv.Config.Name {
			continue
		}
		return !cur.Config.Disabled && mcp.PoolKey(cur, workspace) == key &&
			mcp.NewTrustGate(cfg).Evaluate(workspace, cur) == mcp.TrustStateAllowed
	}
	return false
}

// parkEndedMCPServers gives back the session's leases on servers whose
// connection ended - the shared process exited or a remote server dropped
// the connection - and parks those servers, so the turn about to start dials
// them again (applyParkedMCPServers) and the pool starts one new copy for
// every session that needs it. Without it a server that died would stay dead
// in every session that held it until a reload.
func (m *Manager) parkEndedMCPServers(st *State) {
	for _, name := range st.endedConfiguredMCPServers() {
		st.closeConfiguredMCPClient(name)
		st.markMCPServerPending(name)
		m.log.Warn("MCP server connection had ended; the turn starts it again",
			"server", name, "session", st.GetID())
	}
}
