package mcp

// The pool shares MCP server connections between the sessions of one
// process. Without it every session - and every subagent a session spawned -
// started a process of its own for each configured server, so a coddy serve
// with a dozen conversations ran a dozen browser-automation servers.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// DefaultDialTimeout bounds one start of a pooled server - the spawn (or the
// request) and the handshake - when nothing shorter is set (SetDialTimeout).
const DefaultDialTimeout = 20 * time.Second

// DefaultStopDelay is how long a server nothing holds and nothing keeps goes
// on running before it is stopped (SetStopDelay; mcp.idle_timeout_seconds in
// the configuration). A session that takes the server in the meantime - the
// console's /new, a chat's new conversation, the next run of a scheduled
// job, a session reopened in the same project - finds it running instead of
// waiting for a fresh start.
const DefaultStopDelay = config.MCPDefaultIdleTimeoutSeconds * time.Second

// ErrPoolClosed is what Acquire answers once the pool was closed.
var ErrPoolClosed = errors.New("mcp: the server pool is closed")

// poolConnect is how the pool starts a server; a test replaces it to hold a
// start at a chosen moment.
var poolConnect = Connect

// Pool shares MCP server connections between the sessions of one process: a
// server is started once for every session that runs the same declaration,
// and each session holds a lease on it (Acquire) instead of a process of its
// own. A server of the global configuration is one connection for the whole
// process. A project server, and a global one whose declaration names the
// workspace (${CWD}), is one connection per workspace (WorkspaceBound). A
// connection nothing holds is ended - a stdio server's process group is
// stopped, a remote connection closed - unless the pool keeps it (Keep): the
// global servers a long-running surface starts with the process stay up for
// its whole life, whether a session uses them or not.
type Pool struct {
	log         *slog.Logger
	dialTimeout atomic.Int64
	stopDelay   atomic.Int64
	// wanted answers whether a server that went unheld is still declared as
	// it runs (SetWanted).
	wanted atomic.Pointer[func(srv ManagedServer, workspace string) bool]

	// ctx is what every dial runs under; Close cancels it, so a start still in
	// flight when the process leaves gives up instead of finishing after it.
	// dials counts those starts, for Close to wait for.
	ctx    context.Context
	cancel context.CancelFunc
	dials  sync.WaitGroup

	mu      sync.Mutex
	entries map[string]*poolEntry
	kept    map[string]ManagedServer
	closed  bool
	// stopping holds the connections the pool ended whose server has not
	// finished stopping yet, so Close can wait for them too.
	stopping map[*conn]struct{}
}

// poolEntry is one server connection of the pool, being dialed or up.
type poolEntry struct {
	key       string
	server    ManagedServer
	workspace string

	// ready is closed once the dial settled: conn is set, or err says why
	// the server did not start.
	ready chan struct{}
	err   error
	conn  *conn

	// refs counts the leases handed out and not given back, and the callers
	// waiting for the dial. kept says the pool keeps the connection up with
	// no lease. ended says the connection was ended - by the pool, or by the
	// server going away - and is never handed out again.
	refs  int
	kept  bool
	ended bool
	// idle is the timer that stops the connection once it has gone
	// unheld and unkept for the stop delay; idleGen tells its callback from
	// that of a timer cancelled or replaced since, which may already be
	// waiting for the lock.
	idle    *time.Timer
	idleGen uint64
	// deadline is when the start runs out of time.
	deadline time.Time
}

// PoolServer describes one server connection of the pool.
type PoolServer struct {
	Name string
	// Workspace is the workspace a workspace-bound server runs for, empty
	// for a global one.
	Workspace string
	// Leases counts the handles sessions hold on it, and the callers still
	// waiting for it to start.
	Leases int
	// Kept says the pool keeps it up with no lease.
	Kept bool
	// Connected is false while the server is still starting.
	Connected bool
}

// NewPool returns an empty pool.
func NewPool(log *slog.Logger) *Pool {
	if log == nil {
		log = slog.Default()
	}
	p := &Pool{
		log:      log,
		entries:  make(map[string]*poolEntry),
		kept:     make(map[string]ManagedServer),
		stopping: make(map[*conn]struct{}),
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.dialTimeout.Store(int64(DefaultDialTimeout))
	p.stopDelay.Store(int64(DefaultStopDelay))
	return p
}

// SetStopDelay sets how long an unheld, unkept server runs on before it is
// stopped; d <= 0 stops it the moment the last lease comes back. A server
// waiting out the delay already keeps the delay it started with.
func (p *Pool) SetStopDelay(d time.Duration) {
	if d < 0 {
		d = 0
	}
	p.stopDelay.Store(int64(d))
}

// SetWanted installs the check the pool runs when a server goes unheld:
// whether its declaration is still in effect for the workspace it runs for.
// A server whose declaration is not - switched off, no longer approved,
// removed from the configuration or declared differently now - is stopped at
// once rather than after the stop delay, which is for a server the
// configuration still wants and no session happens to hold. Without a check
// every unheld server waits out the delay. The check runs outside the pool's
// lock and may read the configuration files.
func (p *Pool) SetWanted(fn func(srv ManagedServer, workspace string) bool) {
	if fn == nil {
		p.wanted.Store(nil)
		return
	}
	p.wanted.Store(&fn)
}

// SetDialTimeout bounds every later start of a server; d <= 0 restores
// DefaultDialTimeout.
func (p *Pool) SetDialTimeout(d time.Duration) {
	if d <= 0 {
		d = DefaultDialTimeout
	}
	p.dialTimeout.Store(int64(d))
}

// WorkspaceBound reports whether a declaration runs once per workspace rather
// than once per process: a project server, whatever it declares, and a
// global one whose command, arguments, environment, URL or headers name the
// workspace through ${CWD}, since those resolve to something else in every
// workspace.
func WorkspaceBound(srv ManagedServer) bool {
	if srv.Origin == OriginProject {
		return true
	}
	c := srv.Config
	values := append([]string{c.Command, c.URL}, c.Args...)
	for _, e := range c.Env {
		values = append(values, e.Value)
	}
	for _, h := range c.Headers {
		values = append(values, h.Value)
	}
	for _, v := range values {
		if strings.Contains(v, "${CWD}") {
			return true
		}
	}
	return false
}

// PoolKey identifies the connection a session in workspace gets for srv: the
// name, the transport and what the declaration resolves to there - the
// command line and the environment of a stdio server, the URL and the headers
// of a remote one - plus the canonical workspace for a workspace-bound
// server. Two sessions whose keys match share one connection.
func PoolKey(srv ManagedServer, workspace string) string {
	cfg := srv.Config
	payload := struct {
		Name      string      `json:"name"`
		Transport string      `json:"transport"`
		Command   string      `json:"command,omitempty"`
		Args      []string    `json:"args,omitempty"`
		Env       []string    `json:"env,omitempty"`
		URL       string      `json:"url,omitempty"`
		Headers   [][2]string `json:"headers,omitempty"`
		Workspace string      `json:"workspace,omitempty"`
	}{Name: cfg.Name, Transport: EffectiveTransport(cfg)}
	if payload.Transport == "stdio" {
		command, args, env := stdioSpec(cfg, workspace)
		payload.Command, payload.Args, payload.Env = command, args, effectiveEnv(env)
	} else {
		payload.URL = config.ExpandMCPValue(cfg.URL, workspace)
		for name, value := range expandHeaders(cfg, workspace) {
			payload.Headers = append(payload.Headers, [2]string{name, value})
		}
		sort.Slice(payload.Headers, func(i, j int) bool { return payload.Headers[i][0] < payload.Headers[j][0] })
	}
	if WorkspaceBound(srv) {
		payload.Workspace = CanonicalWorkspace(workspace)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "unmarshalable:" + cfg.Name
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// effectiveEnv is the environment a declaration's NAME=VALUE pairs give the
// process, sorted: a name set twice keeps its last value, as the process
// sees it, so reordering the pairs of one name is a different server while
// reordering different names is not.
func effectiveEnv(pairs []string) []string {
	last := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		name, value, _ := strings.Cut(pair, "=")
		last[name] = value
	}
	out := make([]string, 0, len(last))
	for name, value := range last {
		out = append(out, name+"="+value)
	}
	sort.Strings(out)
	return out
}

// Acquire returns a lease on the connection to srv as declared for
// workspace, starting the server when nothing runs it yet. Close on the lease
// gives it back.
//
// Callers that ask for a server while it starts wait for that one start. The
// start runs on the pool's time rather than on the first caller's: a caller
// whose context ends stops waiting and leaves the others to it, and a server
// nobody waits for any more is stopped as soon as it answers, unless the pool
// keeps it. A start is bounded by the dial timeout. A context that is already
// done starts nothing. A failed start is not remembered: the next Acquire
// starts the server again.
//
// Acquire does not consult the trust gate; TrustGate.Acquire does, and is
// what session code calls.
func (p *Pool) Acquire(ctx context.Context, srv ManagedServer, workspace string) (*Client, error) {
	client, retry, err := p.acquireOnce(ctx, srv, workspace)
	if retry {
		// The server went away as it answered: one fresh start, rather than
		// a lease on a dead connection for the session to find at its next
		// turn.
		client, _, err = p.acquireOnce(ctx, srv, workspace)
	}
	return client, err
}

// acquireOnce is one try of Acquire. retry says the connection it got was
// already gone; the entry is off the map by then, so the next try starts the
// server afresh.
func (p *Pool) acquireOnce(ctx context.Context, srv ManagedServer, workspace string) (*Client, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	key := PoolKey(srv, workspace)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, false, ErrPoolClosed
	}
	e := p.entries[key]
	if e == nil {
		_, kept := p.kept[key]
		// The start is bounded by the caller's own deadline when that comes
		// first: a caller that gives up on the server when its time runs out
		// leaves nothing behind for the next one - a turn's one more try -
		// to join instead of starting afresh.
		deadline := time.Now().Add(time.Duration(p.dialTimeout.Load()))
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		e = p.startLocked(key, srv, workspace, kept, deadline)
	}
	e.refs++
	e.cancelIdleLocked()
	p.mu.Unlock()

	select {
	case <-e.ready:
	case <-ctx.Done():
		p.mu.Lock()
		p.dropExpiredStartLocked(e)
		p.mu.Unlock()
		p.release(e)
		return nil, false, ctx.Err()
	}
	if e.err != nil {
		p.release(e)
		return nil, false, e.err
	}
	select {
	case <-e.conn.gone:
		// watch forgets the entry too, but it may not have yet.
		p.mu.Lock()
		if p.entries[e.key] == e {
			delete(p.entries, e.key)
		}
		p.mu.Unlock()
		p.release(e)
		return nil, true, fmt.Errorf("mcp %s: the server went away as it started", srv.Config.Name)
	default:
	}
	return &Client{
		name:     srv.Config.Name,
		conn:     e.conn,
		declared: Fingerprint(srv.Config),
		poolKey:  e.key,
		release:  func() { p.release(e) },
	}, false, nil
}

// Keep makes servers the set the pool keeps up with no lease. A server of
// the set that does not run yet is started in the background, and one the
// set held before and does not hold now is let go: it stops once no lease
// holds it. A kept server whose start failed, or whose connection ended, is
// started again by the next Keep or Acquire that names it. Keep is for the
// servers of the global configuration a long-running surface starts with the
// process; a workspace-bound server has no single workspace to run for and
// is not kept.
func (p *Pool) Keep(servers []ManagedServer) {
	want := make(map[string]ManagedServer, len(servers))
	for _, srv := range servers {
		if WorkspaceBound(srv) {
			continue
		}
		want[PoolKey(srv, "")] = srv
	}
	type letGo struct {
		e  *poolEntry
		cn *conn
	}
	var stopping []letGo
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	for key := range p.kept {
		if _, ok := want[key]; ok {
			continue
		}
		delete(p.kept, key)
		if e := p.entries[key]; e != nil {
			e.kept = false
			// The configuration let the server go: removed, switched off or
			// declared differently. It does not wait out the stop delay.
			if e.refs == 0 && e.conn != nil && !e.ended {
				stopping = append(stopping, letGo{e, p.endLocked(e)})
			}
		}
	}
	for key, srv := range want {
		p.kept[key] = srv
		if e := p.entries[key]; e != nil {
			e.kept = true
			e.cancelIdleLocked()
			continue
		}
		p.startLocked(key, srv, "", true, time.Now().Add(time.Duration(p.dialTimeout.Load())))
	}
	p.mu.Unlock()
	for _, s := range stopping {
		p.stop(s.e, s.cn, "the configuration no longer keeps it")
	}
}

// Recheck runs the wanted check (SetWanted) again for every server that is
// waiting out its stop delay, and stops the ones whose declaration is no
// longer in effect: a server switched off, no longer approved, removed or
// declared differently after its last session let it go does not wait out the
// rest of the delay. Management surfaces call it after such a change.
func (p *Pool) Recheck() {
	p.mu.Lock()
	var lingering []*poolEntry
	for _, e := range p.entries {
		if e.idle != nil && e.refs == 0 && !e.kept && e.conn != nil && !e.ended {
			lingering = append(lingering, e)
		}
	}
	p.mu.Unlock()
	for _, e := range lingering {
		p.stopIfUnwanted(e)
	}
}

// Running lists the server connections of the pool, the ones still starting
// included, ordered by name and workspace.
func (p *Pool) Running() []PoolServer {
	p.mu.Lock()
	out := make([]PoolServer, 0, len(p.entries))
	for _, e := range p.entries {
		out = append(out, PoolServer{
			Name:      e.server.Config.Name,
			Workspace: e.workspaceIfBound(),
			Leases:    e.refs,
			Kept:      e.kept,
			Connected: e.conn != nil,
		})
	}
	p.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Workspace < out[j].Workspace
	})
	return out
}

// Close ends every connection of the pool, kept or leased, and refuses
// leases from then on; the leases sessions still hold fail their calls. A
// start still in flight is cancelled. It waits, up to a bound, for all of it
// to be over - the servers it ends, the starts it cancels and the servers the
// pool let go of before that are still on their way out. For a process on its
// way out.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	for key, e := range p.entries {
		if e.conn != nil && !e.ended {
			p.endLocked(e)
		}
		e.cancelIdleLocked()
		delete(p.entries, key)
	}
	p.kept = make(map[string]ManagedServer)
	conns := make([]*conn, 0, len(p.stopping))
	for cn := range p.stopping {
		conns = append(conns, cn)
	}
	p.mu.Unlock()
	p.cancel()
	for _, cn := range conns {
		_ = cn.close()
	}
	// One deadline for all of it, with room for the slowest stop a start
	// cancelled here can take on top of its handshake's end.
	deadline := time.After(stdioExitGrace + 2*stdioTermGrace + closeMargin)
	dialsDone := make(chan struct{})
	go func() {
		p.dials.Wait()
		close(dialsDone)
	}()
	select {
	case <-dialsDone:
	case <-deadline:
		p.log.Warn("MCP server starts still running as the pool closes")
	}
	var left []string
	for _, cn := range conns {
		select {
		case <-cn.stopped():
			continue
		default:
		}
		select {
		case <-cn.stopped():
		case <-deadline:
			left = append(left, cn.name)
		}
	}
	if len(left) > 0 {
		p.log.Warn("MCP servers still stopping as the pool closes", "servers", left)
	}
}

// closeMargin is what Close waits on top of the longest stop of a stdio
// server before it gives up on the ones still going.
const closeMargin = 2 * time.Second

// startLocked registers a new entry for key and starts its dial. The caller
// holds p.mu.
func (p *Pool) startLocked(key string, srv ManagedServer, workspace string, kept bool, deadline time.Time) *poolEntry {
	e := &poolEntry{
		key:       key,
		server:    srv,
		workspace: workspace,
		ready:     make(chan struct{}),
		kept:      kept,
		deadline:  deadline,
	}
	p.entries[key] = e
	p.dials.Add(1)
	ctx, cancel := context.WithDeadline(p.ctx, deadline)
	// A start whose time ran out is no longer one to wait for: it is on its
	// way to failing, and stopping what it spawned can take a while. The
	// next caller - a turn's one more try - starts the server afresh
	// instead of joining it. A waiter that gave up at that moment drops it
	// itself (dropExpiredStartLocked), before this has run.
	context.AfterFunc(ctx, func() {
		p.mu.Lock()
		if e.conn == nil && p.entries[e.key] == e {
			delete(p.entries, e.key)
		}
		p.mu.Unlock()
	})
	go p.dial(ctx, cancel, e)
	return e
}

// dropExpiredStartLocked takes a start whose time has run out off the map,
// so the next caller starts the server afresh. The caller holds p.mu.
func (p *Pool) dropExpiredStartLocked(e *poolEntry) {
	if e.conn == nil && !time.Now().Before(e.deadline) && p.entries[e.key] == e {
		delete(p.entries, e.key)
	}
}

// dial starts the entry's server under ctx and settles the entry. A server
// that answers when nobody waits for it and nothing keeps it waits out the
// stop delay, or stops at once without one.
func (p *Pool) dial(ctx context.Context, cancel context.CancelFunc, e *poolEntry) {
	defer p.dials.Done()
	defer cancel()
	client, err := poolConnect(ctx, e.server.Config, e.workspace, p.log)
	p.mu.Lock()
	if err == nil && (p.closed || p.entries[e.key] != e) {
		// Close cancelled the start, or its time ran out and the entry was
		// dropped for the next caller to start afresh (startLocked): an
		// answer that raced in is not installed where nothing would find
		// it. Close is waiting for this start, so the server goes before
		// the start counts as over.
		p.mu.Unlock()
		client.conn.closeAndWait()
		p.mu.Lock()
		client, err = nil, fmt.Errorf("mcp %s: the start ran out of time: %w", e.server.Config.Name, context.DeadlineExceeded)
	}
	if err != nil && p.closed {
		err = ErrPoolClosed
	}
	if err != nil {
		e.err = err
		e.ended = true
		if p.entries[e.key] == e {
			delete(p.entries, e.key)
		}
		close(e.ready)
		kept := e.kept
		p.mu.Unlock()
		if kept && !errors.Is(err, ErrPoolClosed) {
			// A kept server has no session to report its failure; the next
			// Keep or Acquire tries it again.
			p.log.Warn("MCP server did not start", "server", e.server.Config.Name, "error", err)
		}
		return
	}
	e.conn = client.conn
	idle := p.idleLocked(e)
	lingers := e.idle != nil
	kept := e.kept
	close(e.ready)
	p.mu.Unlock()
	if idle != nil {
		p.stop(e, idle, "nobody waits for it any more")
		return
	}
	p.log.Info("MCP server started", e.logAttrs(
		"transport", EffectiveTransport(e.server.Config), "tools", len(client.conn.tools), "kept", kept)...)
	go p.watch(e, client.conn)
	if lingers {
		p.stopIfUnwanted(e)
	}
}

// release gives back one lease on e, or one caller's wait for its dial. A
// server this left unheld stops now when its declaration is no longer in
// effect, and after the stop delay otherwise.
func (p *Pool) release(e *poolEntry) {
	p.mu.Lock()
	e.refs--
	idle := p.idleLocked(e)
	lingers := idle == nil && e.idle != nil && e.refs == 0
	p.mu.Unlock()
	if idle != nil {
		p.stop(e, idle, "no session uses it any more")
		return
	}
	if lingers {
		p.stopIfUnwanted(e)
	}
}

// stopIfUnwanted stops an unheld server at once when the wanted check says
// its declaration is no longer in effect, instead of letting it wait out the
// stop delay.
func (p *Pool) stopIfUnwanted(e *poolEntry) {
	fn := p.wanted.Load()
	if fn == nil || (*fn)(e.server, e.workspace) {
		return
	}
	p.mu.Lock()
	var cn *conn
	if e.refs == 0 && !e.kept && e.conn != nil && !e.ended {
		cn = p.endLocked(e)
	}
	p.mu.Unlock()
	if cn != nil {
		p.stop(e, cn, "its declaration is no longer in effect")
	}
}

// idleLocked handles an entry that may have gone unheld and unkept: with no
// stop delay it is detached and its connection returned for the caller to end
// outside the lock, otherwise a timer ends it once the delay passes with
// nothing taking it again (expire). It returns nil when the entry is still
// needed, still dialing, already ended or left to its timer. The caller holds
// p.mu.
func (p *Pool) idleLocked(e *poolEntry) *conn {
	if e.refs > 0 || e.kept || e.conn == nil || e.ended {
		return nil
	}
	delay := time.Duration(p.stopDelay.Load())
	if delay <= 0 || p.closed {
		return p.endLocked(e)
	}
	if e.idle == nil {
		e.idleGen++
		gen := e.idleGen
		e.idle = time.AfterFunc(delay, func() { p.expire(e, gen) })
	}
	return nil
}

// endLocked detaches an entry for good and returns its connection, which it
// counts among the ones stopping from this moment, so Close waits for it
// whoever ends it. The caller holds p.mu.
func (p *Pool) endLocked(e *poolEntry) *conn {
	e.cancelIdleLocked()
	e.ended = true
	if p.entries[e.key] == e {
		delete(p.entries, e.key)
	}
	if e.conn != nil {
		p.stopping[e.conn] = struct{}{}
	}
	return e.conn
}

// expire stops an entry whose stop delay passed, unless something took or
// kept it in the meantime. gen is the timer's: a timer cancelled or replaced
// after it fired does nothing.
func (p *Pool) expire(e *poolEntry, gen uint64) {
	p.mu.Lock()
	if e.idleGen != gen || e.idle == nil {
		p.mu.Unlock()
		return
	}
	e.idle = nil
	var cn *conn
	if e.refs == 0 && !e.kept && e.conn != nil && !e.ended {
		cn = p.endLocked(e)
	}
	p.mu.Unlock()
	if cn != nil {
		p.stop(e, cn, "no session uses it any more")
	}
}

// cancelIdleLocked stops the entry's stop timer, if one runs. The caller
// holds p.mu.
func (e *poolEntry) cancelIdleLocked() {
	if e.idle != nil {
		e.idle.Stop()
		e.idle = nil
		e.idleGen++
	}
}

// stop ends a connection the pool let go of (endLocked counted it as
// stopping) and forgets it once its server is gone.
func (p *Pool) stop(e *poolEntry, cn *conn, reason string) {
	_ = cn.close()
	go func() {
		<-cn.stopped()
		p.mu.Lock()
		delete(p.stopping, cn)
		p.mu.Unlock()
	}()
	p.log.Info("MCP server stopped", e.logAttrs("reason", reason)...)
}

// watch forgets an entry whose connection ended without the pool ending it -
// the server exited or dropped the connection - so the next Acquire starts
// the server again, and closes what is left of it: a stdio server's process
// group can outlive the process that died.
func (p *Pool) watch(e *poolEntry, cn *conn) {
	<-cn.gone
	p.mu.Lock()
	if e.ended {
		p.mu.Unlock()
		return
	}
	e.cancelIdleLocked()
	e.ended = true
	if p.entries[e.key] == e {
		delete(p.entries, e.key)
	}
	leases := e.refs
	p.stopping[cn] = struct{}{}
	p.mu.Unlock()
	_ = cn.close()
	<-cn.stopped()
	p.mu.Lock()
	delete(p.stopping, cn)
	p.mu.Unlock()
	p.log.Warn("MCP server connection ended; the next session or turn that needs it starts it again",
		e.logAttrs("leases", leases)...)
}

// workspaceIfBound is the workspace the entry runs for, empty for a server
// shared by the whole process.
func (e *poolEntry) workspaceIfBound() string {
	if !WorkspaceBound(e.server) {
		return ""
	}
	return CanonicalWorkspace(e.workspace)
}

// logAttrs names the entry in a log line, followed by extra.
func (e *poolEntry) logAttrs(extra ...any) []any {
	attrs := []any{"server", e.server.Config.Name}
	if ws := e.workspaceIfBound(); ws != "" {
		attrs = append(attrs, "workspace", ws)
	}
	return append(attrs, extra...)
}
