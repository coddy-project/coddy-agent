//go:build http

package httpserver

// The two ends of a shared model, for the godog suites of
// features/remote_model_provider.feature and
// features/remote_model_provider_relay.feature (bdd_remote_model_test.go and
// bdd_remote_model_relay_test.go).
//
// The REMOTE is a real Server behind httptest whose provider factory
// (Server.makeLLMFromYAML, the one factory the direct path and the shared
// routes build their provider from) hands out a scripted, recording stub, with
// a capture in front of it that keeps every byte the remote sent. The LOCAL
// Coddy is real too: the coddy provider of internal/llm built the way the agent
// builds it, and a Server with a session manager and the real agent for the
// steps that ask for GET /v1/models or a whole turn.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// remoteModelLogger is the log of every server of the suites: nothing printed.
func remoteModelLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ---- the model behind the remote ----

// remoteScript is what the stub model does.
type remoteScript struct {
	// answer is the text of a plain answer, split into several chunks.
	answer string
	// usage is the input, output and cached tokens of the answer, when hasUsage.
	usage    [3]int
	hasUsage bool
	// toolName, toolArgs and toolID make the model call a tool until the history
	// holds a tool result; the name is matched against the tools it was offered.
	toolName, toolArgs, toolID string
	// answerAfterTool is the answer once the history holds a tool result.
	answerAfterTool string
	// reasoning and signature are what the model reasons, with the signature the
	// provider returns for it.
	reasoning, signature string
	// holdFirst is how many calls (the first ones) are held open until released;
	// a negative value holds every call.
	holdFirst int
}

// holdGate holds provider calls open until a step releases them.
type holdGate struct {
	mu      sync.Mutex
	entries []*holdEntry
	opened  bool
	running int
}

type holdEntry struct {
	ch       chan struct{}
	released bool
}

// enter blocks until the call is released, the gate opened or ctx ended.
func (g *holdGate) enter(ctx context.Context) error {
	g.mu.Lock()
	if g.opened {
		g.mu.Unlock()
		return nil
	}
	e := &holdEntry{ch: make(chan struct{})}
	g.entries = append(g.entries, e)
	g.running++
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		g.running--
		g.mu.Unlock()
	}()
	select {
	case <-e.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runningCount is how many calls are held now.
func (g *holdGate) runningCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

// releaseOne lets the oldest held call go and reports whether there was one.
func (g *holdGate) releaseOne() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.entries {
		if !e.released {
			e.released = true
			close(e.ch)
			return true
		}
	}
	return false
}

// releaseAll lets every held call go and keeps the gate open for the next ones.
func (g *holdGate) releaseAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.opened = true
	for _, e := range g.entries {
		if !e.released {
			e.released = true
			close(e.ch)
		}
	}
}

// ---- what the remote sent ----

// remoteExchange is one request the remote served and what it answered.
type remoteExchange struct {
	method, path, query string
	auth                string
	reqBody             []byte
	status              int
	header              http.Header
	body                []byte
	// providerCalls is how many calls the model had received when the remote
	// finished answering this request.
	providerCalls int
	done          bool
}

// remoteCapture keeps every request and every byte of every answer.
type remoteCapture struct {
	mu            sync.Mutex
	exchanges     []*remoteExchange
	providerCalls func() int
}

func (c *remoteCapture) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e := &remoteExchange{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization")}
		c.mu.Lock()
		c.exchanges = append(c.exchanges, e)
		c.mu.Unlock()
		// The body is kept through a copy of the request: the server decides what to
		// do with a request that asked to be continued (Expect: 100-continue) by the
		// type of the body of the request it holds itself, so that one is left as it
		// is, and the handler reads the original through the copy.
		if r.Method == http.MethodPost && r.Body != nil {
			r = r.WithContext(r.Context())
			r.Body = &captureBody{ReadCloser: r.Body, c: c, e: e}
		}
		defer func() {
			calls := 0
			if c.providerCalls != nil {
				calls = c.providerCalls()
			}
			c.mu.Lock()
			e.providerCalls, e.done = calls, true
			c.mu.Unlock()
		}()
		next.ServeHTTP(&captureWriter{ResponseWriter: w, c: c, e: e}, r)
	})
}

type captureBody struct {
	io.ReadCloser
	c *remoteCapture
	e *remoteExchange
}

func (b *captureBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.c.mu.Lock()
		b.e.reqBody = append(b.e.reqBody, p[:n]...)
		b.c.mu.Unlock()
	}
	return n, err
}

type captureWriter struct {
	http.ResponseWriter
	c     *remoteCapture
	e     *remoteExchange
	wrote bool
}

func (w *captureWriter) mark(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.c.mu.Lock()
	w.e.status, w.e.header = code, w.Header().Clone()
	w.c.mu.Unlock()
}

func (w *captureWriter) WriteHeader(code int) {
	w.mark(code)
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.mark(http.StatusOK)
	w.c.mu.Lock()
	w.e.body = append(w.e.body, b...)
	w.c.mu.Unlock()
	return w.ResponseWriter.Write(b)
}

func (w *captureWriter) Flush() { _ = http.NewResponseController(w.ResponseWriter).Flush() }

func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// snapshot is a copy of the exchanges so far.
func (c *remoteCapture) snapshot() []remoteExchange {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]remoteExchange, 0, len(c.exchanges))
	for _, e := range c.exchanges {
		out = append(out, *e)
	}
	return out
}

// completions is the exchanges of POST /coddy/llm/completions, in order.
func (c *remoteCapture) completions() []remoteExchange {
	var out []remoteExchange
	for _, e := range c.snapshot() {
		if e.method == http.MethodPost && e.path == llm.CoddyCompletionsPath {
			out = append(out, e)
		}
	}
	return out
}

// mentions reports whether any byte the remote sent - a header or a body, of
// any request - contains needle.
func (c *remoteCapture) mentions(needle string) bool {
	for _, e := range c.snapshot() {
		if bytes.Contains(e.body, []byte(needle)) {
			return true
		}
		for k, vs := range e.header {
			if strings.Contains(k, needle) {
				return true
			}
			for _, v := range vs {
				if strings.Contains(v, needle) {
					return true
				}
			}
		}
	}
	return false
}

// ---- the remote ----

// remoteAuth is how the remote guards its API.
type remoteAuth int

const (
	// authMainAndShared: a main token and a token made for shared models.
	authMainAndShared remoteAuth = iota
	// authMainOnly: the node's own token, nothing made for shared models.
	authMainOnly
	// authSharedOnly: only a token made for shared models.
	authSharedOnly
	// authNone: no credential of any kind.
	authNone
)

// remoteSpec describes the remote before it is built.
type remoteSpec struct {
	selector, alias string
	contextTokens   int
	levels          *[]string
	multimodal      bool
	privates        []string
	auth            remoteAuth
	mainToken       string
	sharedToken     string
	script          remoteScript
}

func newRemoteSpec(selector, alias string) remoteSpec {
	return remoteSpec{
		selector: selector, alias: alias, contextTokens: 200000,
		mainToken: sharedTestMainToken, sharedToken: sharedTestSharedTok,
		script: remoteScript{toolID: "call_1"},
	}
}

// remoteStand is one built remote.
type remoteStand struct {
	spec    remoteSpec
	home    string
	cfg     *config.Config
	mgr     *session.Manager
	srv     *Server
	ts      *httptest.Server
	handler http.Handler
	stub    *sharedStub
	gate    *holdGate
	capt    *remoteCapture

	runnerCalls atomic.Int32

	mu     sync.Mutex
	script remoteScript
	builds []remoteBuild
}

// remoteBuild is one provider the factory was asked for.
type remoteBuild struct {
	selector string
	opts     llm.RequestOptions
}

// newRemoteStand builds the remote of spec. listen says whether it gets an
// address of its own (a node that only dials out has none).
func newRemoteStand(spec remoteSpec, muts []func(*config.Config), listen bool) (*remoteStand, error) {
	home, err := os.MkdirTemp("", "coddy-bdd-remote-model-*")
	if err != nil {
		return nil, err
	}
	provName, _, err := config.SplitModelRef(spec.selector)
	if err != nil {
		return nil, err
	}
	levels := spec.levels
	if levels == nil {
		def := []string{"low", "high"}
		levels = &def
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: home, ConfigPath: filepath.Join(home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: provName, Type: "openai", APIBase: sharedTestUpstreamAB, APIKey: "upstream-secret-key"}},
		Models: []config.ModelEntry{{
			Model: spec.selector, MaxTokens: 4096, MaxContextTokens: spec.contextTokens,
			ReasoningLevels: levels, ReasoningDefault: "low", Multimodal: spec.multimodal, SharedAs: spec.alias,
		}},
	}
	for _, p := range spec.privates {
		cfg.Models = append(cfg.Models, config.ModelEntry{Model: p, MaxTokens: 1024, MaxContextTokens: 1000})
	}
	switch spec.auth {
	case authMainAndShared:
		cfg.HTTPServer.AuthToken = spec.mainToken
		cfg.HTTPServer.SharedModels.Tokens = []string{spec.sharedToken}
	case authMainOnly:
		cfg.HTTPServer.AuthToken = spec.mainToken
	case authSharedOnly:
		cfg.HTTPServer.SharedModels.Tokens = []string{spec.sharedToken}
	case authNone:
	}
	for _, m := range muts {
		m(cfg)
	}

	r := &remoteStand{spec: spec, home: home, cfg: cfg, gate: &holdGate{}, script: spec.script}
	r.stub = &sharedStub{run: r.runScript}
	r.capt = &remoteCapture{providerCalls: r.stub.callCount}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		r.runnerCalls.Add(1)
		return "", errors.New("the remote ran an agent turn: a shared model call must not")
	}
	store := &session.FileStore{Root: filepath.Join(home, "sessions")}
	log := remoteModelLogger()
	r.mgr = session.NewManager(cfg, noopSender{}, runner, log, home, store)
	r.srv = New(cfg, r.mgr, log, home)
	r.srv.makeLLMFromYAML = func(_ *config.Config, sel string, o llm.RequestOptions) (llm.Provider, error) {
		r.mu.Lock()
		r.builds = append(r.builds, remoteBuild{selector: sel, opts: o})
		r.mu.Unlock()
		return r.stub, nil
	}
	r.handler = r.capt.wrap(r.srv.Handler())
	if listen {
		r.ts = httptest.NewServer(r.handler)
	}
	return r, nil
}

func (r *remoteStand) url() string {
	if r.ts == nil {
		return ""
	}
	return r.ts.URL
}

func (r *remoteStand) close() {
	r.gate.releaseAll()
	if r.ts != nil {
		r.ts.Close()
	}
	r.srv.Drain()
	_ = os.RemoveAll(r.home)
}

func (r *remoteStand) currentScript() remoteScript {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.script
}

func (r *remoteStand) lastBuild() (remoteBuild, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.builds) == 0 {
		return remoteBuild{}, false
	}
	return r.builds[len(r.builds)-1], true
}

// reload installs a changed copy of the configuration, the way a saved
// config.yaml reaches a running server.
func (r *remoteStand) reload(mut func(*config.Config)) {
	r.mu.Lock()
	next := *r.cfg
	next.Models = append([]config.ModelEntry(nil), r.cfg.Models...)
	next.HTTPServer.SharedModels.Tokens = append([]string(nil), r.cfg.HTTPServer.SharedModels.Tokens...)
	mut(&next)
	r.cfg = &next
	r.mu.Unlock()
	r.srv.ReplaceConfig(&next)
	r.mgr.ReplaceConfig(&next)
}

// sharedRowIndex is the index of the models[] row shared under alias.
func (r *remoteStand) sharedRowIndex(alias string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.cfg.Models {
		if r.cfg.Models[i].SharedAs == alias {
			return i
		}
	}
	return -1
}

// runScript is the stub model: it records nothing itself (sharedStub does) and
// does what the script says.
func (r *remoteStand) runScript(ctx context.Context, n int, msgs []llm.Message, tools []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
	sc := r.currentScript()
	if sc.holdFirst < 0 || n <= sc.holdFirst {
		if err := r.gate.enter(ctx); err != nil {
			return nil, err
		}
	}
	withUsage := func(resp *llm.Response) *llm.Response {
		resp.InputTokens, resp.OutputTokens, resp.CachedInputTokens = 3, 1, 0
		if sc.hasUsage {
			resp.InputTokens, resp.OutputTokens, resp.CachedInputTokens = sc.usage[0], sc.usage[1], sc.usage[2]
		}
		return resp
	}
	sawResult := false
	for _, m := range msgs {
		if m.Role == llm.RoleTool {
			sawResult = true
		}
	}
	if sc.toolName != "" && !sawResult {
		tc := llm.ToolCall{ID: sc.toolID, Name: offeredToolName(tools, sc.toolName), InputJSON: sc.toolArgs}
		on(llm.StreamChunk{ToolCall: &tc})
		return withUsage(&llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}), nil
	}
	answer := sc.answer
	if sawResult && sc.answerAfterTool != "" {
		answer = sc.answerAfterTool
	}
	if answer == "" {
		answer = "ok"
	}
	if sc.reasoning != "" {
		on(llm.StreamChunk{ReasoningDelta: sc.reasoning})
	}
	for _, piece := range textPieces(answer) {
		on(llm.StreamChunk{TextDelta: piece})
	}
	return withUsage(&llm.Response{Content: answer, Reasoning: sc.reasoning, ReasoningSignature: sc.signature, StopReason: "end_turn"}), nil
}

// offeredToolName is the name the model calls a tool by: the tool it was
// offered under that name, or under that name behind a server prefix.
func offeredToolName(tools []llm.ToolDefinition, want string) string {
	for _, t := range tools {
		if t.Name == want {
			return t.Name
		}
	}
	for _, t := range tools {
		if strings.HasSuffix(t.Name, "__"+want) {
			return t.Name
		}
	}
	return want
}

// textPieces splits an answer at its spaces, so that it arrives in several
// chunks and the client has to assemble it.
func textPieces(s string) []string {
	var out []string
	for len(s) > 0 {
		i := strings.IndexByte(s, ' ')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// assertNoSession checks that the remote kept nothing of the calls it served:
// no session in its store or in memory, no agent turn run, nothing written to
// disk but the key of its revisions.
func (r *remoteStand) assertNoSession() error {
	listed, err := r.mgr.HandleSessionList(context.Background(), acp.SessionListParams{})
	if err != nil {
		return err
	}
	if len(listed.Sessions) != 0 {
		return fmt.Errorf("the remote holds %d session(s): %+v", len(listed.Sessions), listed.Sessions)
	}
	if n := r.runnerCalls.Load(); n != 0 {
		return fmt.Errorf("the remote ran %d agent turn(s)", n)
	}
	if entries, err := os.ReadDir(filepath.Join(r.home, "sessions")); err == nil && len(entries) != 0 {
		return fmt.Errorf("the remote wrote %d entries into its session store", len(entries))
	}
	entries, err := os.ReadDir(r.home)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != sharedModelsKeyFile && e.Name() != "sessions" {
			return fmt.Errorf("the remote wrote %q into its home", e.Name())
		}
	}
	return nil
}

// ---- plain HTTP clients ----

// httpResult is one answer, read whole.
type httpResult struct {
	status int
	header http.Header
	body   []byte
}

var remoteModelClient = &http.Client{Timeout: 30 * time.Second}

func doHTTP(method, url, token string, body []byte) (httpResult, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return httpResult{}, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := remoteModelClient.Do(req)
	if err != nil {
		return httpResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return httpResult{status: resp.StatusCode, header: resp.Header, body: raw}, err
}

// openStream posts a completion request and returns once the answer's headers
// are in: a stream that is running, or the refusal.
func openStream(base, token, alias string) (*http.Response, error) {
	raw, err := json.Marshal(llm.WireRequest{
		Protocol: llm.CoddyProtocol, Model: alias,
		Messages: []llm.WireMessage{{Role: "user", Content: "Hi"}},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, base+llm.CoddyCompletionsPath, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return remoteModelClient.Do(req)
}

// remoteModelWaitUntil polls cond for up to five seconds.
func remoteModelWaitUntil(what string, cond func() bool) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", what)
}

// ---- the local tool of a whole turn ----

// weatherMCP is a streamable-HTTP MCP server with one tool, the local tool of a
// turn: it answers a fixed text and records the arguments of every call.
type weatherMCP struct {
	name, result string
	srv          *httptest.Server

	mu    sync.Mutex
	calls []string
}

func newWeatherMCP(name, result string) *weatherMCP {
	w := &weatherMCP{name: name, result: result}
	w.srv = httptest.NewServer(w)
	return w
}

func (w *weatherMCP) recordedCalls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

func (w *weatherMCP) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		ID     any             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}
	if req.ID == nil {
		rw.WriteHeader(http.StatusAccepted)
		return
	}
	respond := func(result any) {
		msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write(msg)
	}
	switch req.Method {
	case "initialize":
		respond(map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "weather", "version": "0.0.1"},
		})
	case "tools/list":
		respond(map[string]any{"tools": []map[string]any{{
			"name":        w.name,
			"description": "Weather of a city",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
		}}})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		w.mu.Lock()
		w.calls = append(w.calls, string(p.Arguments))
		w.mu.Unlock()
		respond(map[string]any{"content": []map[string]any{{"type": "text", "text": w.result}}})
	default:
		respond(nil)
	}
}

// ---- the local Coddy ----

// localMarker is a line of the local workspace's AGENTS.md: the local harness's
// own system prompt, which has to reach the remote model.
const localMarker = "LOCAL-HARNESS-MARKER-7"

// localStand is a local Coddy: a Server with a session manager and the real
// agent, over a configuration whose provider "remote" is of type coddy.
type localStand struct {
	root, home, cwd string
	mgr             *session.Manager
	srv             *Server
	ts              *httptest.Server
	weather         *weatherMCP
}

// newLocalStand builds the local Coddy over cfg. Its home and workspace are
// made here; a weather server, when asked for, is declared in <home>/mcp.json.
func newLocalStand(cfg *config.Config, tool *weatherMCP) (*localStand, error) {
	root, err := os.MkdirTemp("", "coddy-bdd-remote-model-local-*")
	if err != nil {
		return nil, err
	}
	l := &localStand{root: root, home: filepath.Join(root, "home"), cwd: filepath.Join(root, "workspace"), weather: tool}
	for _, dir := range []string{l.home, l.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(l.cwd, "AGENTS.md"), []byte("# Local harness\n\n"+localMarker+"\n"), 0o644); err != nil {
		return nil, err
	}
	cfg.Paths = config.Paths{Home: l.home, CWD: l.cwd, ConfigPath: filepath.Join(l.home, "config.yaml")}
	if tool != nil {
		if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(l.home), "weather", config.MCPJSONServer{Type: "http", URL: tool.srv.URL}); err != nil {
			return nil, err
		}
	}
	log := remoteModelLogger()
	var mgr *session.Manager
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(mgr.Cfg(), st, snd, log).Run(ctx, prompt)
	}
	mgr = session.NewManager(cfg, noopSender{}, runner, log, l.cwd, &session.FileStore{Root: filepath.Join(root, "sessions")})
	l.mgr = mgr
	l.srv = New(cfg, mgr, log, l.cwd)
	l.ts = httptest.NewServer(l.srv.Handler())
	return l, nil
}

func (l *localStand) close() {
	l.ts.Close()
	l.srv.Drain()
	if l.weather != nil {
		l.weather.srv.Close()
	}
	_ = os.RemoveAll(l.root)
}

// reload installs a changed copy of the local configuration.
func (l *localStand) reload(mut func(*config.Config)) {
	next := *l.mgr.Cfg()
	next.Models = append([]config.ModelEntry(nil), next.Models...)
	next.Providers = append([]config.ProviderConfig(nil), next.Providers...)
	mut(&next)
	l.mgr.ReplaceConfig(&next)
	l.srv.ReplaceConfig(&next)
}

// provider builds the provider of a model row the way a turn of the agent
// does (internal/agent/coddy_provider.go): the row's wait budget, the revision
// of the listing record the manager's cache holds as the expected one, and the
// refresh that reads the listing again after a stale answer. The listing is
// read first, as a turn's admission reads it.
func (l *localStand) provider(ctx context.Context, model, effort string) (llm.Provider, error) {
	cfg := l.mgr.Cfg()
	l.mgr.AwaitContextWindows(ctx, cfg, []string{model}, session.ContextWindowWait)
	rm, err := cfg.ResolveLLM(model)
	if err != nil {
		return nil, err
	}
	in := llm.ProviderInput{
		Name:              rm.ProviderName,
		Type:              rm.ProviderType,
		Model:             rm.Model,
		APIKey:            rm.APIKey,
		BaseURL:           rm.BaseURL,
		ProxyURL:          rm.ProxyURL,
		MaxTokens:         rm.MaxTokens,
		Temperature:       rm.Temperature,
		DisableStream:     !rm.Stream,
		Timeout:           time.Duration(rm.TimeoutMS) * time.Millisecond,
		StreamIdleTimeout: cfg.Agent.EffectiveLLMStreamIdleTimeout(),
		ReasoningEffort:   effort,
		BusyWait:          cfg.EffectiveBusyWait(rm.ProviderName),
	}
	if e, ok := l.mgr.ProviderModelEntry(cfg, rm.ProviderName, rm.Model); ok {
		in.ExpectedRevision = e.Revision
	}
	in.RefreshCapabilities = func(ctx context.Context) (*llm.ModelEntry, error) {
		stale := ""
		if e, ok := l.mgr.ProviderModelEntry(cfg, rm.ProviderName, rm.Model); ok {
			stale = e.Revision
		}
		return l.mgr.RefreshProviderModelEntry(ctx, cfg, rm.ProviderName, rm.Model, stale, session.ContextWindowWait)
	}
	return llm.NewProvider(llm.WithAgentResilience(in, cfg.Agent.EffectiveLLMRetryMax(), cfg.Agent.LLMRetryBaseMS, cfg.Agent.LLMMinIntervalMS))
}

// runTurn runs one whole turn of the real agent over POST /v1/responses and
// returns the session's final assistant message.
func (l *localStand) runTurn(prompt string) (string, error) {
	const sid = "sess_remote_model_turn"
	raw, err := json.Marshal(map[string]any{"model": "agent", "input": prompt, "stream": false})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, l.ts.URL+"/v1/responses", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", sid)
	res, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("POST /v1/responses: status %d: %s", res.StatusCode, body)
	}
	msgs, err := doHTTP(http.MethodGet, l.ts.URL+"/coddy/sessions/"+sid+"/messages", "", nil)
	if err != nil {
		return "", err
	}
	if msgs.status != http.StatusOK {
		return "", fmt.Errorf("GET messages: status %d: %s", msgs.status, msgs.body)
	}
	var parsed struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(msgs.body, &parsed); err != nil {
		return "", err
	}
	for i := len(parsed.Messages) - 1; i >= 0; i-- {
		if parsed.Messages[i].Role == "assistant" && strings.TrimSpace(parsed.Messages[i].Content) != "" {
			return parsed.Messages[i].Content, nil
		}
	}
	return "", fmt.Errorf("the session has no assistant message: %s", msgs.body)
}
