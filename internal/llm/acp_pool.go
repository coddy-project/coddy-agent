package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
)

// ACPDepthEnv names the variable an acp provider sets in the environment of
// the agent it starts: one more than its own value. A Coddy started that way
// (coddy acp behind an acp row) reads it, and refuses to start an agent of
// its own at ACPMaxDepth, so a chain of agents that start each other ends
// instead of forking forever.
const ACPDepthEnv = "CODDY_ACP_DEPTH"

// ACPMaxDepth is the depth at which a Coddy no longer starts an ACP agent.
const ACPMaxDepth = 3

// acpDefaultIdle is how long an agent nobody calls stays up. Its sessions,
// and the agent's own context in them, go with it: the next call replays
// the transcript into a new session.
const acpDefaultIdle = 60 * time.Minute

// acpMaxSessions is how many sessions an agent that cannot close one opens
// before it is restarted once nothing uses it.
const acpMaxSessions = 64

// acpCWDPlaceholder is the session workspace in an acp block value.
const acpCWDPlaceholder = "${CWD}"

// proxyEnvNames are the proxy variables a "none" or URL proxy setting
// replaces in the agent's environment.
var proxyEnvNames = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"}

// acpSpec is everything that decides which agent process serves a call.
type acpSpec struct {
	name       string
	command    string
	args       []string
	env        []string
	dir        string
	authMethod string
	idle       time.Duration
	// sessionCWD is the folder ACP sessions are opened in when the call names
	// none: acp.cwd, else the process's own working directory.
	sessionCWD string
	key        string
}

// label names the agent in errors and logs: the command's base name.
func (s acpSpec) label() string {
	if s.command == "" {
		return "acp agent"
	}
	return filepath.Base(s.command)
}

// resolveACPSpec turns a row's acp block into the spec of the process that
// serves a call of the Coddy session sessionID in workspace (both "" when
// the call belongs to no session).
func resolveACPSpec(in ProviderInput, sessionID, workspace string) (acpSpec, error) {
	c := in.ACP
	if c == nil || strings.TrimSpace(c.Command) == "" {
		return acpSpec{}, &ACPError{Kind: ACPErrorConfig, Agent: in.Name, Message: "acp.command is not set"}
	}
	if d := acpSelfDepth(); d >= ACPMaxDepth {
		return acpSpec{}, &ACPError{Kind: ACPErrorConfig, Agent: in.Name, Message: fmt.Sprintf("refusing to start an ACP agent: %s is %d, the limit is %d (agents starting each other)", ACPDepthEnv, d, ACPMaxDepth)}
	}
	ownWD, _ := os.Getwd()
	if workspace == "" {
		workspace = ownWD
	}
	bound := c.NamesWorkspace()
	expand := func(s string) string {
		if bound {
			s = strings.ReplaceAll(s, acpCWDPlaceholder, workspace)
		}
		return expandLeadingHome(s)
	}
	spec := acpSpec{
		name:       in.Name,
		command:    expand(strings.TrimSpace(c.Command)),
		authMethod: strings.TrimSpace(c.AuthMethod),
		idle:       time.Duration(c.IdleTimeoutMS) * time.Millisecond,
	}
	if spec.idle <= 0 {
		spec.idle = acpDefaultIdle
	}
	for _, a := range c.Args {
		spec.args = append(spec.args, expand(a))
	}
	if dir := strings.TrimSpace(c.CWD); dir != "" {
		spec.dir = expand(dir)
		spec.sessionCWD = spec.dir
	} else {
		spec.sessionCWD = ownWD
	}

	// The environment: Coddy's own, the proxy the row names, the block's env,
	// and the depth of the chain.
	mode, proxyURL, err := config.ParseProxySetting(in.ProxyURL)
	if err != nil {
		return acpSpec{}, &ACPError{Kind: ACPErrorConfig, Agent: in.Name, Message: err.Error()}
	}
	overrides := map[string]string{}
	var removed []string
	switch mode {
	case config.ProxyModeNone:
		removed = proxyEnvNames
	case config.ProxyModeURL:
		// HTTP(S)_PROXY for the agent's requests; NO_PROXY replaced by the
		// loopback addresses, since an inherited "*" would bypass the proxy
		// and the agent's own local traffic (zcode-acp-server to its app
		// server) must stay direct; ALL_PROXY dropped for the same traffic.
		removed = []string{"ALL_PROXY", "all_proxy"}
		for _, n := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
			overrides[n] = proxyURL.String()
		}
		for _, n := range []string{"NO_PROXY", "no_proxy"} {
			overrides[n] = "localhost,127.0.0.1,::1"
		}
	}
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		overrides[k] = expand(c.Env[k])
	}
	overrides[ACPDepthEnv] = strconv.Itoa(acpSelfDepth() + 1)
	spec.env = mergeEnv(os.Environ(), overrides, removed)

	h := sha256.New()
	write := func(s string) { _, _ = h.Write([]byte(s)); _, _ = h.Write([]byte{0}) }
	write(spec.name)
	write(spec.command)
	for _, a := range spec.args {
		write(a)
	}
	write(spec.dir)
	write(spec.authMethod)
	write(in.ProxyURL)
	for _, k := range keys {
		write(k + "=" + overrides[k])
	}
	if bound {
		write(workspace)
	}
	if c.PerSession() {
		write("session:" + sessionID)
	}
	spec.key = hex.EncodeToString(h.Sum(nil))[:24]
	return spec, nil
}

// mergeEnv returns base with every name of removed dropped and overrides set.
func mergeEnv(base []string, overrides map[string]string, removed []string) []string {
	drop := map[string]bool{}
	for _, n := range removed {
		drop[n] = true
	}
	for n := range overrides {
		drop[n] = true
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if drop[name] {
			continue
		}
		out = append(out, kv)
	}
	names := make([]string, 0, len(overrides))
	for n := range overrides {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, n+"="+overrides[n])
	}
	return out
}

func expandLeadingHome(s string) string {
	if s == "~" || strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(s, "~"))
		}
	}
	return s
}

// acpPool keeps one agent process per spec key for the whole Coddy process:
// a provider is built per turn, the agent outlives it.
type acpPool struct {
	mu      sync.Mutex
	entries map[string]*acpPoolEntry
	log     *slog.Logger
	// uncertain remembers, by row and conversation, the digest of the
	// history whose prompt ended without a clear outcome after its binding
	// was lost (an abandoned prompt, an agent that died after acting), so
	// the same history is not replayed into a new session as a new task.
	uncertain map[string]string
	// starts bounds the starts in flight: stopAll cancels it, so a shutdown
	// does not wait out a handshake that hangs.
	starts      context.Context
	cancelStart context.CancelFunc
}

var acpAgents = newACPPool()

func newACPPool() *acpPool {
	pl := &acpPool{entries: map[string]*acpPoolEntry{}, uncertain: map[string]string{}}
	pl.starts, pl.cancelStart = context.WithCancel(context.Background())
	return pl
}

// historyDigest identifies a whole history for the uncertain marker.
func historyDigest(history []Message) string {
	var b strings.Builder
	for _, m := range history {
		b.WriteString(acpMessageDigest(m))
	}
	return b.String()
}

func (pl *acpPool) markUncertain(key string, history []Message) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.uncertain[key] = historyDigest(history)
}

func (pl *acpPool) clearUncertain(key string) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	delete(pl.uncertain, key)
}

// lostUncertain reports whether history is exactly the one whose prompt
// ended without a clear outcome; any other history forgets the mark.
func (pl *acpPool) lostUncertain(key string, history []Message) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	d, ok := pl.uncertain[key]
	if !ok {
		return false
	}
	if d == historyDigest(history) {
		return true
	}
	delete(pl.uncertain, key)
	return false
}

// acpPoolEntry is one agent process and what is bound to it.
type acpPoolEntry struct {
	spec  acpSpec
	proc  *acpProcess
	ready chan struct{}
	err   error
	users int
	idle  *time.Timer
	// dying: kill is stopping the agent. It stays in the pool until it is
	// gone, and a caller waits for that instead of starting another agent
	// next to a prompt that may still be at work.
	dying bool

	// conversations binds Coddy sessions to ACP sessions of this process
	// (acp_conversation.go); guarded by convMu.
	convMu        sync.Mutex
	conversations map[string]*acpConversation
}

// acquire returns the running agent of spec, starting it when there is none,
// and a release the caller runs when its call is over. The start belongs to
// the pool, not to the call that happened to ask first: it runs under no
// caller's cancellation (acpStartTimeout bounds it), and every caller waits
// for it under its own context.
func (pl *acpPool) acquire(ctx context.Context, spec acpSpec) (*acpPoolEntry, func(), error) {
	pl.mu.Lock()
	e := pl.entries[spec.key]
	for e != nil && e.dying && e.proc.alive() {
		pl.mu.Unlock()
		select {
		case <-e.proc.done:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		pl.mu.Lock()
		e = pl.entries[spec.key]
	}
	if e != nil && e.proc != nil && !e.proc.alive() {
		// The agent died: forget it and everything bound to it.
		delete(pl.entries, spec.key)
		e = nil
	}
	if e == nil {
		e = &acpPoolEntry{spec: spec, ready: make(chan struct{}), conversations: map[string]*acpConversation{}}
		pl.entries[spec.key] = e
		go pl.start(e, pl.starts)
	}
	e.users++
	if e.idle != nil {
		e.idle.Stop()
		e.idle = nil
	}
	pl.mu.Unlock()
	select {
	case <-e.ready:
	case <-ctx.Done():
		pl.release(e)
		return nil, nil, ctx.Err()
	}
	if e.err != nil {
		// The start failed: the same spec fails the same way, and the next
		// call tries again.
		pl.release(e)
		return nil, nil, e.err
	}
	return e, pl.releaser(e), nil
}

// start starts the agent of e and publishes the outcome.
func (pl *acpPool) start(e *acpPoolEntry, ctx context.Context) {
	proc, err := startACPProcess(ctx, e.spec, pl.logger())
	pl.mu.Lock()
	e.proc, e.err = proc, err
	if err != nil && pl.entries[e.spec.key] == e {
		delete(pl.entries, e.spec.key)
	}
	close(e.ready)
	if err == nil && e.users == 0 {
		// Every caller gave up while it started: it idles out like any other.
		e.idle = time.AfterFunc(e.spec.idle, func() { pl.expire(e) })
	}
	pl.mu.Unlock()
}

func (pl *acpPool) releaser(e *acpPoolEntry) func() {
	var once sync.Once
	return func() { once.Do(func() { pl.release(e) }) }
}

// release ends one use of e; the last one arms the idle timer.
func (pl *acpPool) release(e *acpPoolEntry) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	e.users--
	if e.users > 0 || e.proc == nil || e.dying {
		return
	}
	if e.idle != nil {
		e.idle.Stop()
	}
	if !e.proc.canClose() && e.proc.opened.Load() >= acpMaxSessions {
		// Its sessions pile up in the agent: restart it while nobody uses it.
		e.idle = time.AfterFunc(0, func() { pl.expire(e) })
		return
	}
	e.idle = time.AfterFunc(e.spec.idle, func() { pl.expire(e) })
	go e.sweep()
}

// kill stops the agent of e at once and forgets it: the calls that still
// wait on it fail as exited. It is how a prompt that ignored its
// cancellation is stopped for good.
func (pl *acpPool) kill(e *acpPoolEntry) {
	// The entry stays while the agent stops, marked: a caller that comes
	// meanwhile waits for it to be gone rather than starting a new agent
	// that would run next to the prompt still at work, or sending a prompt
	// to the one that is stopping.
	pl.mu.Lock()
	e.dying = true
	if e.idle != nil {
		e.idle.Stop()
		e.idle = nil
	}
	pl.mu.Unlock()
	if e.proc != nil {
		e.proc.stop()
	}
	pl.mu.Lock()
	if pl.entries[e.spec.key] == e {
		delete(pl.entries, e.spec.key)
	}
	pl.mu.Unlock()
}

// expire stops an agent that stayed unused for its idle period.
func (pl *acpPool) expire(e *acpPoolEntry) {
	pl.mu.Lock()
	if e.users > 0 || pl.entries[e.spec.key] != e {
		pl.mu.Unlock()
		return
	}
	delete(pl.entries, e.spec.key)
	pl.mu.Unlock()
	pl.logger().Debug("stopping idle acp agent", "agent", e.spec.label(), "provider", e.spec.name)
	e.proc.stop()
}

// stopAll stops every agent and forgets it, an agent still starting
// included once its start is over.
func (pl *acpPool) stopAll() {
	pl.mu.Lock()
	entries := pl.entries
	pl.entries = map[string]*acpPoolEntry{}
	pl.uncertain = map[string]string{}
	pl.cancelStart()
	pl.starts, pl.cancelStart = context.WithCancel(context.Background())
	for _, e := range entries {
		if e.idle != nil {
			e.idle.Stop()
		}
	}
	pl.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Add(1)
		go func(e *acpPoolEntry) {
			defer wg.Done()
			<-e.ready
			pl.mu.Lock()
			proc := e.proc
			pl.mu.Unlock()
			if proc != nil {
				proc.stop()
			}
		}(e)
	}
	wg.Wait()
}

func (pl *acpPool) logger() *slog.Logger {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.log != nil {
		return pl.log
	}
	return slog.Default()
}

// StopACPAgents stops every ACP agent this process started for an acp
// provider. The surfaces call it on shutdown, next to stopping the MCP
// servers; an agent left behind would otherwise outlive Coddy in its own
// process group.
func StopACPAgents() { acpAgents.stopAll() }

// SetACPLogger routes the acp provider's diagnostics, tagged as the
// provider.acp component.
func SetACPLogger(log *slog.Logger) {
	acpAgents.mu.Lock()
	acpAgents.log = logger.Component(log, logger.ComponentProviderACP)
	acpAgents.mu.Unlock()
}
