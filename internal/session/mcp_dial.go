package session

// The configured MCP servers of a session are dialed here: the trust-gated
// target list, the concurrent dial with one timeout per server, the log
// lines and the warnings a settings reload reports. Session creation, the
// subagent spawn, the console's background connect and the hot reloads all
// reach a spawn through these helpers, so none of them can start a server
// the trust gate did not admit, and none of them waits on one server longer
// than its own bound, whatever its transport: a stdio command that never
// answers initialize and a remote URL that accepts the connection and stays
// silent cost the same.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// defaultMCPConnectTimeout bounds one server's spawn (or request) and
// handshake. It is what a server that starts and never answers initialize
// costs the session: the others keep connecting beside it, and a turn
// waiting for the tool list waits at most this long. It stays below
// mcpStartTimeout and mcpReloadTimeout, the deadlines a whole dial shares,
// so a hung server fails on its own bound instead of running the shared
// deadline out: a server the deadline cut short is parked and dialed again
// at the session's next turn, while one that failed on its own bound is not,
// and a server that never answers would otherwise cost every turn the full
// deadline.
const defaultMCPConnectTimeout = 20 * time.Second

// errMCPNoAnswer marks a dial the per-server bound gave up on: the server
// started, or its address took the connection, and initialize got no answer
// in time.
var errMCPNoAnswer = errors.New("no answer to initialize")

// mcpDialTarget is one server to connect: its declaration and the connect
// call, which goes through the trust gate for configured servers so nothing
// spawns that the gate did not admit right before the spawn.
type mcpDialTarget struct {
	Server  mcp.ManagedServer
	Connect func(ctx context.Context) (*mcp.Client, error)
}

// mcpDialResult is what one target's dial ended with. CutShort says the
// caller's ctx had ended by the time the dial returned with an error: the
// server was not given its chance, as opposed to failing on its own.
type mcpDialResult struct {
	Target   mcpDialTarget
	Client   *mcp.Client
	Err      error
	CutShort bool
}

// mcpHeldServer is a configured server the trust gate refused to start: a
// project declaration the operator has not approved for this workspace
// (Blocked), or one the gate could not decide on (Err).
type mcpHeldServer struct {
	Server  mcp.ManagedServer
	Blocked *mcp.BlockedError
	Err     error
}

// connectTimeout is the per-server dial budget; tests shorten it.
func (m *Manager) connectTimeout() time.Duration {
	if m.mcpConnectTimeout > 0 {
		return m.mcpConnectTimeout
	}
	return defaultMCPConnectTimeout
}

// configuredTarget is the dial of one configured server: through the gate,
// then a lease from the manager's pool, so the session shares the server with
// every other session that runs the same declaration (one process for a
// global server, one per workspace for a project server) instead of
// starting a copy of its own.
func (m *Manager) configuredTarget(gate *mcp.TrustGate, srv mcp.ManagedServer, cwd string) mcpDialTarget {
	return mcpDialTarget{
		Server: srv,
		Connect: func(ctx context.Context) (*mcp.Client, error) {
			return gate.Acquire(ctx, m.mcpPool, srv, cwd)
		},
	}
}

// configuredTargets lists the enabled configured servers (<home>/mcp.json
// merged with the project's .coddy/mcp.json) that the trust gate admits for cwd, and the
// ones it holds. The gate is evaluated here and again inside Connect, so a
// project-local .coddy/mcp.json stays cold until its exact declaration is
// approved, whichever path reaches the spawn.
func (m *Manager) configuredTargets(cfg *config.Config, cwd string) ([]mcpDialTarget, []mcpHeldServer) {
	gate := mcp.NewTrustGate(cfg)
	managed := mcp.ListManagedServersTolerant(cfg, cwd, m.log)
	targets := make([]mcpDialTarget, 0, len(managed))
	var held []mcpHeldServer
	for _, srv := range managed {
		if srv.Config.Disabled {
			continue
		}
		if err := gate.Check(cwd, srv); err != nil {
			var blocked *mcp.BlockedError
			if errors.As(err, &blocked) {
				held = append(held, mcpHeldServer{Server: srv, Blocked: blocked, Err: err})
				continue
			}
			held = append(held, mcpHeldServer{Server: srv, Err: err})
			continue
		}
		targets = append(targets, m.configuredTarget(gate, srv, cwd))
	}
	return targets, held
}

// dialOne connects one target under its own copy of the per-server timeout.
// A server that did not answer within it fails with errMCPNoAnswer and the
// bound in the message; the caller's ctx ending is reported as that ctx's
// error, so a caller can tell a dial it cut short from a server that failed.
func (m *Manager) dialOne(ctx context.Context, target mcpDialTarget) (*mcp.Client, error) {
	// An expired budget starts nothing: a process spawned now would only be
	// thrown away.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := m.connectTimeout()
	srvCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := target.Connect(srvCtx)
	if err != nil {
		// A dial that failed owns nothing: whatever it half opened is closed
		// here, so no caller can leave a process behind.
		if client != nil {
			_ = client.Close()
			client = nil
		}
		// The start this dial waited for may be another session's, whose
		// bound ran out before this one's: a server that started and never
		// answered is the same no answer either way.
		if ctx.Err() == nil && (errors.Is(srvCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)) {
			err = fmt.Errorf("%w within %s: %w", errMCPNoAnswer, timeout, err)
		}
	}
	return client, err
}

// noteConfiguredDial keeps the session's record of the configured servers
// that did not answer in time, after one of them was dialed. An answer
// clears the server. A server the per-server bound gave up on is kept for
// one more try at the start of the session's next turn - a first start that
// installs a package (npx, uvx) can outlast the bound, and the next try
// starts it from the package cache - but only once: a server that never
// answers must not cost every turn its bound. It reports whether the server
// was kept for that try.
func (m *Manager) noteConfiguredDial(st *State, name string, err error) bool {
	retry, gaveUp := st.recordMCPDial(name, err)
	m.logNoAnswer(st, name, retry, gaveUp)
	return retry
}

// logNoAnswer says what the record of servers that did not answer in time
// made of one dial.
func (m *Manager) logNoAnswer(st *State, name string, retry, gaveUp bool) {
	switch {
	case retry:
		m.log.Warn("MCP server did not answer in time; the session's next turn tries it once more",
			"server", name, "session", st.GetID())
	case gaveUp:
		m.log.Warn("MCP server did not answer in time again; it stays down until a reload, its switch or a new session",
			"server", name, "session", st.GetID())
	}
}

// dialConcurrently connects every target at once, each under its own copy of
// the per-server timeout, and returns the results in input order. settled is
// called once per target as it finishes, never two at a time. A ctx that is
// already done spawns nothing: an expired reload budget must not start
// processes it will only throw away.
func (m *Manager) dialConcurrently(ctx context.Context, targets []mcpDialTarget, settled func(i int, r mcpDialResult)) []mcpDialResult {
	results := make([]mcpDialResult, len(targets))
	if len(targets) == 0 {
		return results
	}
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for i, target := range targets {
		results[i].Target = target
		if err := ctx.Err(); err != nil {
			mu.Lock()
			results[i].Err, results[i].CutShort = err, true
			if settled != nil {
				settled(i, results[i])
			}
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(i int, target mcpDialTarget) {
			defer wg.Done()
			client, err := m.dialOne(ctx, target)
			// Read now, not after every dial is back: a server that failed
			// on its own before the caller's ctx ended did not run out of
			// the caller's time.
			cutShort := err != nil && ctx.Err() != nil
			mu.Lock()
			results[i].Client, results[i].Err, results[i].CutShort = client, err, cutShort
			r := results[i]
			if settled != nil {
				settled(i, r)
			}
			mu.Unlock()
		}(i, target)
	}
	wg.Wait()
	return results
}

// logHeld says why a configured server was not started.
func (m *Manager) logHeld(cwd string, h mcpHeldServer) {
	if h.Blocked != nil {
		m.log.Warn("MCP server not started: project declaration is not approved for this workspace",
			"server", h.Server.Config.Name, "workspace", cwd, "state", string(h.Blocked.State),
			"digest", h.Blocked.Digest, "approve_with", "coddy mcp trust "+h.Server.Config.Name)
		return
	}
	m.log.Warn("MCP server not started", "server", h.Server.Config.Name, "workspace", cwd, "error", h.Err)
}

// logDial says how one configured server's dial ended.
func (m *Manager) logDial(r mcpDialResult) {
	name := r.Target.Server.Config.Name
	if r.Err != nil {
		m.log.Warn("failed to connect MCP server", "server", name, "error", r.Err)
		return
	}
	m.log.Info("connected MCP server", "name", name,
		"transport", mcp.EffectiveTransport(r.Target.Server.Config), "tools", len(r.Client.Tools()))
}

// dialConfigured connects the enabled configured servers of cfg the trust
// gate admits for cwd, all at once, and logs why a held one was not started
// and how every dial ended - except the ones ctx cut short, which the caller
// reports as a whole. Session creation, the subagent spawn and the reloads
// all go through here, so none of them can reach a spawn without
// TrustGate.Connect: a project-local .coddy/mcp.json stays cold until its
// exact declaration is approved. The dial costs the slowest server, not the
// sum of them, and never more than the per-server timeout. The results keep
// configuration order - the tool list a model is handed keeps its order from
// one session start to the next, which the provider's prompt cache needs.
// cfg is a parameter because ReloadConfigForSession dials the configuration
// it is about to install, which the manager does not hold yet.
func (m *Manager) dialConfigured(ctx context.Context, cfg *config.Config, cwd string) ([]mcpDialResult, []mcpHeldServer) {
	targets, held := m.configuredTargets(cfg, cwd)
	for _, h := range held {
		m.logHeld(cwd, h)
	}
	results := m.dialConcurrently(ctx, targets, nil)
	for _, r := range results {
		if r.CutShort {
			continue
		}
		m.logDial(r)
	}
	return results, held
}

// connectedClients returns the clients of the dials that answered, in order.
func connectedClients(results []mcpDialResult) []*mcp.Client {
	clients := make([]*mcp.Client, 0, len(results))
	for _, r := range results {
		if r.Err == nil && r.Client != nil {
			clients = append(clients, r.Client)
		}
	}
	return clients
}

// dialWarnings says, one line per server, why a configured server did not
// start: held by the trust gate, or failed.
func dialWarnings(results []mcpDialResult, held []mcpHeldServer) []string {
	var warnings []string
	for _, h := range held {
		if h.Err != nil {
			warnings = append(warnings, fmt.Sprintf("connect MCP %s: %v", h.Server.Config.Name, h.Err))
		}
	}
	for _, r := range results {
		if r.Err != nil {
			warnings = append(warnings, fmt.Sprintf("connect MCP %s: %v", r.Target.Server.Config.Name, r.Err))
		}
	}
	return warnings
}

// connectSessionMCPServers connects the servers an ACP client sent with
// session/new or session/load, concurrently and each under the per-server
// timeout, and attaches the ones that answered to the session in the order
// the client listed them. Unlike the configured servers these survive a
// settings reload, because only that client can recreate them.
func (m *Manager) connectSessionMCPServers(ctx context.Context, st *State, decls []config.MCPServerConfig) {
	if len(decls) == 0 {
		return
	}
	targets := make([]mcpDialTarget, 0, len(decls))
	for _, srv := range decls {
		targets = append(targets, mcpDialTarget{
			Server: mcp.ManagedServer{Config: srv},
			Connect: func(ctx context.Context) (*mcp.Client, error) {
				return m.connectMCPServer(ctx, st, srv)
			},
		})
	}
	for _, r := range m.dialConcurrently(ctx, targets, nil) {
		if r.Err != nil {
			m.log.Warn("failed to connect client MCP server", "server", r.Target.Server.Config.Name, "error", r.Err)
			continue
		}
		st.AddSessionMCPClient(r.Client)
		st.RememberSessionMCPDeclaration(r.Target.Server.Config)
	}
}
