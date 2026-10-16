//go:build http

package httpserver

// The stand of features/remote_model_provider.feature (the steps are in
// bdd_remote_model_provider_test.go): two Coddys in one process.
//
// The remote is this package's server over a session manager, its
// configuration written to a config.yaml and loaded from it (LoadWithPaths). The
// provider behind every shared row is a scripted stand-in (makeLLMFromYAML),
// which records what reached it, and a recorder in front of the handler keeps
// every exchange: what the client sent, what the remote answered and how many
// provider calls had been made when the answer ended.
//
// The local Coddy is a server, a session manager and the real agent, with a
// provider of type coddy whose api_base is the remote. Its provider for a row is
// the one the agent builds for a session (rowProvider): the revision, the
// reasoning level, the busy wait and the refresh are the agent's own decisions.

import (
	"bytes"
	"context"
	"encoding/json"
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

	"gopkg.in/yaml.v3"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	// rmpMainToken opens the whole API of the remote, rmpSharedToken only its
	// shared-model routes.
	rmpMainToken   = "rmp-main-token-4f1c9a0e7b2d"
	rmpSharedToken = "rmp-shared-token-8d3e6b5a1c4f"
	// rmpUpstreamKey is the remote's own key for the provider behind the row.
	rmpUpstreamKey = "sk-rmp-upstream-key"
	// rmpHoldMarker is the message of a stream the scripted provider holds open
	// until the stand releases it.
	rmpHoldMarker = "rmp: hold this stream open"
	// rmpWorkspaceMarker is written into the local workspace's AGENTS.md, so the
	// local system prompt can be recognised on the remote.
	rmpWorkspaceMarker = "rmp-local-workspace-marker-7f3a"
	// rmpUsageKeyName is the name of the remote's NeuralDeep key in the hub's
	// answer: it must not leave the remote.
	rmpUsageKeyName = "rmp-lender-key-name"
	// rmpWait bounds every wait of the stand.
	rmpWait = 20 * time.Second
)

// rmpWeatherSchema is the schema of the get_weather tool the local Coddy
// declares, offered as a tool of its own and served by its MCP server.
func rmpWeatherSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"city": map[string]any{"type": "string"}},
		"required":   []any{"city"},
	}
}

// rmpLogBuffer keeps the log of one scenario, both Coddys in one, printed only
// when the scenario fails.
type rmpLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *rmpLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *rmpLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *rmpLogBuffer) logger(side string) *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})).With("coddy", side)
}

// ---- the provider behind the shared row ----

// rmpUpstreamCall is one call of the provider behind the shared row: the row it
// was built for, the options the remote built it with, and what it was sent.
type rmpUpstreamCall struct {
	Selector string
	Options  llm.RequestOptions
	Messages []llm.Message
	Tools    []llm.ToolDefinition
}

// rmpScript is what the scripted provider answers to one call.
type rmpScript func(call rmpUpstreamCall, onChunk func(llm.StreamChunk)) (*llm.Response, error)

// rmpUpstream plays the provider of the remote's shared rows. A call whose last
// user message is rmpHoldMarker, or every call while holdAll is set, stays open
// until release names its message or the stand stops. Releasing by message, not
// to whichever call waits first, lets the stand end the very stream it reads:
// under load the calls start waiting in another order than their answers
// arrived in.
type rmpUpstream struct {
	mu      sync.Mutex
	calls   []rmpUpstreamCall
	script  rmpScript
	holdAll bool
	gates   map[string]chan struct{}

	running atomic.Int32
	stop    chan struct{}
}

func newRMPUpstream() *rmpUpstream {
	return &rmpUpstream{
		script: rmpAnswer("Done.", 0, 0, 0),
		gates:  map[string]chan struct{}{},
		stop:   make(chan struct{}),
	}
}

// gate is the channel the held call of message waits on.
func (u *rmpUpstream) gate(message string) chan struct{} {
	u.mu.Lock()
	defer u.mu.Unlock()
	g, ok := u.gates[message]
	if !ok {
		g = make(chan struct{})
		u.gates[message] = g
	}
	return g
}

// release lets the held call of message end; releasing it twice is a no-op.
func (u *rmpUpstream) release(message string) {
	g := u.gate(message)
	u.mu.Lock()
	defer u.mu.Unlock()
	select {
	case <-g:
	default:
		close(g)
	}
}

func (u *rmpUpstream) provider(selector string, opts llm.RequestOptions) llm.Provider {
	return &rmpUpstreamProvider{u: u, selector: selector, opts: opts}
}

func (u *rmpUpstream) setScript(s rmpScript) {
	u.mu.Lock()
	u.script = s
	u.mu.Unlock()
}

func (u *rmpUpstream) snapshot() []rmpUpstreamCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]rmpUpstreamCall(nil), u.calls...)
}

func (u *rmpUpstream) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.calls)
}

// last is the latest call, or an error when the provider was never called.
func (u *rmpUpstream) last() (rmpUpstreamCall, error) {
	calls := u.snapshot()
	if len(calls) == 0 {
		return rmpUpstreamCall{}, fmt.Errorf("the remote's provider was never called")
	}
	return calls[len(calls)-1], nil
}

func (u *rmpUpstream) holds(call rmpUpstreamCall) bool {
	u.mu.Lock()
	all := u.holdAll
	u.mu.Unlock()
	return all || rmpLastUserText(call.Messages) == rmpHoldMarker
}

type rmpUpstreamProvider struct {
	u        *rmpUpstream
	selector string
	opts     llm.RequestOptions
}

func (p *rmpUpstreamProvider) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, msgs, tools, nil)
}

func (p *rmpUpstreamProvider) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	call := rmpUpstreamCall{
		Selector: p.selector,
		Options:  p.opts,
		Messages: append([]llm.Message(nil), msgs...),
		Tools:    append([]llm.ToolDefinition(nil), tools...),
	}
	p.u.mu.Lock()
	p.u.calls = append(p.u.calls, call)
	script := p.u.script
	p.u.mu.Unlock()
	if onChunk == nil {
		onChunk = func(llm.StreamChunk) {}
	}
	if p.u.holds(call) {
		gate := p.u.gate(rmpLastUserText(msgs))
		p.u.running.Add(1)
		defer p.u.running.Add(-1)
		select {
		case <-gate:
		case <-p.u.stop:
			return nil, fmt.Errorf("the stand stopped")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return script(call, onChunk)
}

// rmpAnswer answers text with the given usage.
func rmpAnswer(text string, input, output, cached int) rmpScript {
	return func(_ rmpUpstreamCall, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
		onChunk(llm.StreamChunk{TextDelta: text})
		return &llm.Response{Content: text, StopReason: "end_turn",
			InputTokens: input, OutputTokens: output, CachedInputTokens: cached}, nil
	}
}

// rmpOfferedTool finds the tool a call offers under name: the name itself, or
// the namespaced name of an MCP server's tool (<server>__<name>).
func rmpOfferedTool(tools []llm.ToolDefinition, name string) (string, bool) {
	for _, t := range tools {
		if t.Name == name || strings.HasSuffix(t.Name, "__"+name) {
			return t.Name, true
		}
	}
	return "", false
}

// rmpToolResult returns the first tool result the history holds: once there is
// one, a scripted model answers instead of calling the tool again.
func rmpToolResult(msgs []llm.Message) (llm.Message, bool) {
	for _, m := range msgs {
		if m.Role == llm.RoleTool {
			return m, true
		}
	}
	return llm.Message{}, false
}

// rmpCallTool calls the offered tool name with args under id. A request that
// offers no tool at all (a title, a summary) is not the turn this model plays:
// it answers in words.
func rmpCallTool(name, args, id string) rmpScript {
	return func(call rmpUpstreamCall, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
		if len(call.Tools) == 0 {
			return rmpAnswer("Ready.", 0, 0, 0)(call, onChunk)
		}
		offered, ok := rmpOfferedTool(call.Tools, name)
		if !ok {
			return nil, fmt.Errorf("the request offered no tool %q", name)
		}
		tc := llm.ToolCall{ID: id, Name: offered, InputJSON: args}
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
}

// rmpAnswerAfterResult answers text once the history holds a tool result.
func rmpAnswerAfterResult(text string) rmpScript {
	return func(call rmpUpstreamCall, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
		if _, ok := rmpToolResult(call.Messages); !ok {
			return nil, fmt.Errorf("the request holds no tool result")
		}
		return rmpAnswer(text, 0, 0, 0)(call, onChunk)
	}
}

// rmpCallThenAnswer calls the tool until the history holds its result, then
// answers text.
func rmpCallThenAnswer(name, args, id, text string) rmpScript {
	return func(call rmpUpstreamCall, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
		if _, ok := rmpToolResult(call.Messages); ok {
			return rmpAnswer(text, 0, 0, 0)(call, onChunk)
		}
		return rmpCallTool(name, args, id)(call, onChunk)
	}
}

// rmpReason reasons text under the signature sig and answers briefly.
func rmpReason(text, sig string) rmpScript {
	return func(_ rmpUpstreamCall, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
		onChunk(llm.StreamChunk{ReasoningDelta: text})
		onChunk(llm.StreamChunk{TextDelta: "Checked."})
		return &llm.Response{Content: "Checked.", Reasoning: text, ReasoningSignature: sig, StopReason: "end_turn"}, nil
	}
}

func rmpLastUserText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			return msgs[i].Content
		}
	}
	return ""
}

// ---- what the remote sent ----

// rmpExchange is one request the remote served, as the recorder saw it.
type rmpExchange struct {
	Method, Path string
	Status       int
	Request      []byte
	Response     []byte
	Header       http.Header
	// CallsAtStart and CallsAtEnd are the provider calls made when the request
	// came in and when its answer ended.
	CallsAtStart, CallsAtEnd int
}

// rmpRecorder records every exchange of the handler it wraps. The request body
// is copied as the handler reads it, never ahead of it, so a refusal still reads
// no more of the body than it means to.
type rmpRecorder struct {
	next     http.Handler
	upstream *rmpUpstream
	// inflight counts the exchanges whose handler has not returned: one is in
	// the log only once it has, which can be after its client has read the
	// whole answer.
	inflight atomic.Int32

	mu        sync.Mutex
	exchanges []rmpExchange
}

func (rec *rmpRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec.inflight.Add(1)
	defer rec.inflight.Add(-1)
	ex := rmpExchange{Method: r.Method, Path: r.URL.Path, CallsAtStart: rec.upstream.callCount()}
	body := &rmpTeeBody{ReadCloser: r.Body}
	r.Body = body
	rw := &rmpRecordingWriter{ResponseWriter: w, status: http.StatusOK}
	rec.next.ServeHTTP(rw, r)
	ex.Request = body.bytes()
	ex.Status, ex.Response = rw.result()
	ex.Header = w.Header().Clone()
	ex.CallsAtEnd = rec.upstream.callCount()
	rec.mu.Lock()
	rec.exchanges = append(rec.exchanges, ex)
	rec.mu.Unlock()
}

func (rec *rmpRecorder) snapshot() []rmpExchange {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]rmpExchange(nil), rec.exchanges...)
}

type rmpTeeBody struct {
	io.ReadCloser
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *rmpTeeBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	b.buf.Write(p[:n])
	b.mu.Unlock()
	return n, err
}

func (b *rmpTeeBody) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// rmpRecordingWriter copies what the handler writes. The stream writes from
// its heartbeat goroutine too, hence the lock; Unwrap lets the handler's
// ResponseController reach the connection's deadlines.
type rmpRecordingWriter struct {
	http.ResponseWriter
	mu     sync.Mutex
	status int
	wrote  bool
	buf    bytes.Buffer
}

func (w *rmpRecordingWriter) WriteHeader(status int) {
	w.mu.Lock()
	if !w.wrote {
		w.status, w.wrote = status, true
	}
	w.mu.Unlock()
	w.ResponseWriter.WriteHeader(status)
}

func (w *rmpRecordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.wrote = true
	w.buf.Write(p)
	w.mu.Unlock()
	return w.ResponseWriter.Write(p)
}

func (w *rmpRecordingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *rmpRecordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *rmpRecordingWriter) result() (int, []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status, append([]byte(nil), w.buf.Bytes()...)
}

// rmpFrozenClock stops the clock the rate window reads; the call's timers keep
// running on the real one.
type rmpFrozenClock struct{ at time.Time }

func (c rmpFrozenClock) Now() time.Time { return c.at }

func (rmpFrozenClock) NewTimer(d time.Duration) sharedTimer { return realSharedClock{}.NewTimer(d) }

// ---- the remote Coddy ----

// rmpRemote is the Coddy that shares its models. Its configuration is the YAML
// document doc, written to config.yaml and loaded from there at the start and
// at every change its operator makes.
type rmpRemote struct {
	root, home, cwd, cfgPath, sessions string

	doc      map[string]any
	upstream *rmpUpstream
	frozen   bool

	// usage is the stand-in of the NeuralDeep hub behind a provider that
	// reports its quota, with the environment it replaced.
	usage       *httptest.Server
	usedPercent int
	usageReads  atomic.Int64
	prevBase    *string

	log         *slog.Logger
	rec         *rmpRecorder
	mgr         *session.Manager
	srv         *Server
	ts          *httptest.Server
	runnerCalls atomic.Int64
}

func newRMPRemote(root string, logs *rmpLogBuffer) *rmpRemote {
	dir := filepath.Join(root, "remote")
	return &rmpRemote{
		log:      logs.logger("remote"),
		root:     dir,
		home:     filepath.Join(dir, "home"),
		cwd:      filepath.Join(dir, "work"),
		cfgPath:  filepath.Join(dir, "home", "config.yaml"),
		sessions: filepath.Join(dir, "sessions"),
		upstream: newRMPUpstream(),
		doc: map[string]any{
			"httpserver": map[string]any{
				"auth_token":    rmpMainToken,
				"shared_models": map[string]any{"tokens": []any{rmpSharedToken}},
			},
			"providers": []any{map[string]any{"name": "stub", "type": "openai", "api_key": rmpUpstreamKey}},
			"models":    []any{},
			"agent":     map[string]any{"model": ""},
		},
	}
}

func (r *rmpRemote) httpserver() map[string]any { return r.doc["httpserver"].(map[string]any) }

func (r *rmpRemote) sharedModels() map[string]any {
	h := r.httpserver()
	sm, ok := h["shared_models"].(map[string]any)
	if !ok {
		sm = map[string]any{}
		h["shared_models"] = sm
	}
	return sm
}

// setTokens sets the credentials of the remote: the main token, the
// shared-model token, either, both or none.
func (r *rmpRemote) setTokens(main, shared bool) {
	h := r.httpserver()
	delete(h, "auth_token")
	if main {
		h["auth_token"] = rmpMainToken
	}
	sm := r.sharedModels()
	delete(sm, "tokens")
	if shared {
		sm["tokens"] = []any{rmpSharedToken}
	}
}

func (r *rmpRemote) hasToken(key string) bool {
	switch key {
	case "main":
		_, ok := r.httpserver()["auth_token"]
		return ok
	default:
		_, ok := r.sharedModels()["tokens"]
		return ok
	}
}

// row returns the models[] row of selector, adding it when it is missing.
func (r *rmpRemote) row(selector string) map[string]any {
	models := r.doc["models"].([]any)
	for _, m := range models {
		if row := m.(map[string]any); row["model"] == selector {
			return row
		}
	}
	row := map[string]any{"model": selector}
	r.doc["models"] = append(models, row)
	if agentModel := r.doc["agent"].(map[string]any); agentModel["model"] == "" {
		agentModel["model"] = selector
	}
	return row
}

// share offers selector under alias.
func (r *rmpRemote) share(selector, alias string) map[string]any {
	row := r.row(selector)
	row["shared_as"] = alias
	return row
}

// rmpSetLevels gives row its reasoning levels. A row that offers low names it as
// its default, so a row narrowed to low keeps a default to fall back to.
func rmpSetLevels(row map[string]any, levels []any) {
	row["reasoning_levels"] = levels
	delete(row, "reasoning_default")
	for _, lv := range levels {
		if lv == "low" {
			row["reasoning_default"] = "low"
		}
	}
}

// sharedRow is the row offered under alias.
func (r *rmpRemote) sharedRow(alias string) (map[string]any, error) {
	for _, m := range r.doc["models"].([]any) {
		if row := m.(map[string]any); row["shared_as"] == alias {
			return row, nil
		}
	}
	return nil, fmt.Errorf("the remote shares nothing as %q", alias)
}

// reportUsage makes the provider behind the rows a NeuralDeep row whose hub
// says percent of every window is used.
func (r *rmpRemote) reportUsage(percent int) {
	r.usedPercent = percent
	r.doc["providers"] = []any{map[string]any{"name": "stub", "type": "neuraldeep", "api_key": rmpUpstreamKey}}
}

func (r *rmpRemote) started() bool { return r.ts != nil }

func (r *rmpRemote) writeConfig() (*config.Config, error) {
	raw, err := yaml.Marshal(r.doc)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(r.cfgPath, raw, 0o600); err != nil {
		return nil, err
	}
	cfg, err := config.LoadWithPaths(config.Paths{Home: r.home, CWD: r.cwd, ConfigPath: r.cfgPath})
	if err != nil {
		return nil, fmt.Errorf("the remote's config.yaml does not load: %w\n%s", err, raw)
	}
	return cfg, nil
}

func (r *rmpRemote) start() error {
	if r.started() {
		return nil
	}
	for _, dir := range []string{r.home, r.cwd, r.sessions} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if r.usedPercent > 0 {
		r.startUsageHub()
	}
	cfg, err := r.writeConfig()
	if err != nil {
		return err
	}
	// A turn on the remote would be a session and a tool loop of its own: the
	// shared routes must never start one.
	runner := func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		r.runnerCalls.Add(1)
		return string(acp.StopReasonEndTurn), nil
	}
	r.mgr = session.NewManager(cfg, noopSender{}, runner, r.log, r.cwd, &session.FileStore{Root: r.sessions})
	r.srv = New(cfg, r.mgr, r.log, r.cwd)
	r.srv.makeLLMFromYAML = func(_ *config.Config, selector string, opts llm.RequestOptions) (llm.Provider, error) {
		return r.upstream.provider(selector, opts), nil
	}
	if r.frozen {
		r.srv.sharedClk = rmpFrozenClock{at: time.Now()}
	}
	r.rec = &rmpRecorder{next: r.srv.Handler(), upstream: r.upstream}
	r.ts = httptest.NewUnstartedServer(r.rec)
	// The listener of `coddy serve` keeps the accepted connection in the
	// request, which bounds a vanished peer (httpx.ProbeCall).
	r.ts.Config.ConnContext = httpx.ConnContext
	r.ts.Start()
	return nil
}

// reconfigure applies an edit of the remote's operator: the file is rewritten,
// loaded and installed in the running server, as a hot reload does.
func (r *rmpRemote) reconfigure(edit func() error) error {
	if err := edit(); err != nil {
		return err
	}
	if !r.started() {
		return nil
	}
	cfg, err := r.writeConfig()
	if err != nil {
		return err
	}
	r.srv.ReplaceConfig(cfg)
	r.mgr.ReplaceConfig(cfg)
	return nil
}

// startUsageHub serves the hub's GET /limits with every window at usedPercent,
// and points the NeuralDeep provider at it: a NeuralDeep row ignores an
// api_base that is no NeuralDeep deployment, and the environment override is
// the stand's way in.
func (r *rmpRemote) startUsageHub() {
	sessUsed, weekUsed := 15000*r.usedPercent/100, 150000*r.usedPercent/100
	payload := fmt.Sprintf(`{"schema":1,"blocked_models":[],"observed_at":"2026-10-16T10:00:00Z","tier":"pro","tier_expires_at":null,
 "unlimited_volume":false,"options":[],"bypass":false,"fair_use":true,
 "key":{"name":%q,"status":"ok","billing_mode":"wallet","cap":null},
 "decision":{"scope":"chat","can_request":true,"blockers":[],"retry_after_sec":null},
 "chat":{"session":{"used":%d,"limit":15000,"remaining":%d,"reset_in_sec":777,"resets_at":"2026-10-16T12:59:59Z","window":"3h"},
         "week":{"used":%d,"limit":150000,"remaining":%d,"reset_in_sec":22378,"resets_at":"2026-10-19T00:00:00Z","window":"iso-week"},
         "rpm":{"used":0,"limit":120,"remaining":120,"reset_in_sec":58},"cooldown_sec":0,"scope":"account"},
 "vector":{"session":{"used":0,"limit":300000},"rpm_limit":120,"inflight_limit":8},
 "parallel_limit":16,"abuse_cooldown_sec":0,
 "daily_capacity":{"pct_used":%d,"exhausted":false,"resets_at":"2026-10-17T00:00:00+00:00"},
 "night":{"enabled":true,"active":false,"capacity_factor":2,"window_start_msk":0,"window_end_msk":6},
 "wallet":{"balance_rub":-1229.244167,"spent_rub_30d":2000.73518},"kimi":null}`,
		rmpUsageKeyName, sessUsed, 15000-sessUsed, weekUsed, 150000-weekUsed, r.usedPercent)
	r.usage = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/limits":
			r.usageReads.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, payload)
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
		default:
			http.NotFound(w, req)
		}
	}))
	if prev, ok := os.LookupEnv(llm.EnvNeuralDeepBaseURL); ok {
		r.prevBase = &prev
	}
	_ = os.Setenv(llm.EnvNeuralDeepBaseURL, r.usage.URL)
}

// sessionCount is the number of sessions the remote's store holds.
func (r *rmpRemote) sessionCount() (int, error) {
	entries, err := os.ReadDir(r.sessions)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sess_") {
			n++
		}
	}
	return n, nil
}

func (r *rmpRemote) close() {
	select {
	case <-r.upstream.stop:
	default:
		close(r.upstream.stop)
	}
	if r.ts != nil {
		r.ts.CloseClientConnections()
		r.ts.Close()
		r.ts = nil
	}
	if r.srv != nil {
		r.srv.Drain()
		r.srv = nil
	}
	if r.usage != nil {
		r.usage.Close()
		r.usage = nil
		if r.prevBase != nil {
			_ = os.Setenv(llm.EnvNeuralDeepBaseURL, *r.prevBase)
		} else {
			_ = os.Unsetenv(llm.EnvNeuralDeepBaseURL)
		}
	}
}

// ---- the local Coddy ----

// rmpLocal is the Coddy whose harness uses the shared model. Its configuration
// is a config.yaml as well, so a key written in a row can be read back from
// the file.
type rmpLocal struct {
	root, home, cwd, cfgPath, sessions string

	providerName string
	busyWaitMS   int
	rows         []map[string]any

	weather *rmpWeatherMCP
	// noRows starts the local Coddy with the provider and no model of it.
	noRows bool

	log *slog.Logger
	mgr *session.Manager
	srv *Server
	ts  *httptest.Server
	sid string

	// built is the provider rowProvider built last, with the settings revision
	// and the configuration it was built under.
	built      llm.Provider
	builtRev   uint64
	builtUnder *config.Config
}

func newRMPLocal(root, providerName string, logs *rmpLogBuffer) *rmpLocal {
	dir := filepath.Join(root, "local")
	return &rmpLocal{
		log:          logs.logger("local"),
		root:         dir,
		home:         filepath.Join(dir, "home"),
		cwd:          filepath.Join(dir, "work"),
		cfgPath:      filepath.Join(dir, "home", "config.yaml"),
		sessions:     filepath.Join(dir, "sessions"),
		providerName: providerName,
	}
}

// row returns the models[] row of selector, adding it when it is missing.
func (l *rmpLocal) row(selector string) map[string]any {
	for _, row := range l.rows {
		if row["model"] == selector {
			return row
		}
	}
	row := map[string]any{"model": selector}
	l.rows = append(l.rows, row)
	return row
}

func (l *rmpLocal) started() bool { return l.ts != nil }

func (l *rmpLocal) start(remote *rmpRemote) error {
	if l.started() {
		return nil
	}
	if err := remote.start(); err != nil {
		return err
	}
	for _, dir := range []string{l.home, l.cwd, l.sessions} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if len(l.rows) == 0 && !l.noRows {
		l.row(l.providerName + "/coder")
	}
	if err := os.WriteFile(filepath.Join(l.cwd, "AGENTS.md"),
		[]byte("# Workspace\n\nThis workspace is marked "+rmpWorkspaceMarker+".\n"), 0o644); err != nil {
		return err
	}
	if l.weather != nil {
		if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(l.home), "weather",
			config.MCPJSONServer{Type: "http", URL: l.weather.URL}); err != nil {
			return err
		}
	}
	provider := map[string]any{
		"name": l.providerName, "type": "coddy",
		"api_base": remote.ts.URL, "api_key": rmpSharedToken,
	}
	if l.busyWaitMS > 0 {
		provider["busy_wait_ms"] = l.busyWaitMS
	}
	rows := make([]any, 0, len(l.rows))
	for _, row := range l.rows {
		rows = append(rows, row)
	}
	doc := map[string]any{
		"providers": []any{provider},
		"models":    rows,
	}
	if len(l.rows) > 0 {
		doc["agent"] = map[string]any{"model": l.rows[0]["model"]}
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(l.cfgPath, raw, 0o600); err != nil {
		return err
	}
	return l.boot()
}

// boot builds the local Coddy from its config.yaml as it is on disk.
func (l *rmpLocal) boot() error {
	cfg, err := config.LoadWithPaths(config.Paths{Home: l.home, CWD: l.cwd, ConfigPath: l.cfgPath})
	if err != nil {
		raw, _ := os.ReadFile(l.cfgPath)
		return fmt.Errorf("the local config.yaml does not load: %w\n%s", err, raw)
	}
	// The mode a new session starts in when no operator chose one: the turn
	// runs its tools without asking.
	cfg.Tools.PermissionMode = config.PermModeBypass
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(l.mgr.Cfg(), st, snd, l.log).Run(ctx, prompt)
	}
	l.mgr = session.NewManager(cfg, noopSender{}, runner, l.log, l.cwd, &session.FileStore{Root: l.sessions})
	l.srv = New(cfg, l.mgr, l.log, l.cwd)
	l.ts = httptest.NewServer(l.srv.Handler())
	return nil
}

// session is the local session the scenario streams in, made on first use.
func (l *rmpLocal) session(ctx context.Context) (*session.State, error) {
	if l.sid == "" {
		res, err := l.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: l.cwd})
		if err != nil {
			return nil, err
		}
		l.sid = res.SessionID
	}
	st := l.mgr.SessionByID(l.sid)
	if st == nil {
		return nil, fmt.Errorf("the local session %s is not registered", l.sid)
	}
	return st, nil
}

// readListing waits for the local manager to read the listing of the remote.
func (l *rmpLocal) readListing(ctx context.Context) {
	cfg := l.mgr.Cfg()
	refs := make([]string, 0, len(cfg.Models))
	for i := range cfg.Models {
		refs = append(refs, cfg.Models[i].Model)
	}
	l.mgr.AwaitContextWindows(ctx, cfg, refs, session.ContextWindowWait)
}

// rmpEndTurnProvider ends the turn the agent runs to build its provider.
type rmpEndTurnProvider struct{}

func (rmpEndTurnProvider) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return rmpEndTurnProvider{}.Stream(ctx, msgs, tools, nil)
}

func (rmpEndTurnProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	if onChunk != nil {
		onChunk(llm.StreamChunk{TextDelta: "Ready."})
	}
	return &llm.Response{Content: "Ready.", StopReason: "end_turn"}, nil
}

// rowProvider is the provider the local agent builds for the model of the
// session: the agent runs a turn whose provider factory keeps the real coddy
// provider built from the agent's own input and answers that turn itself, so
// nothing reaches the remote until the scenario sends its own messages. Like
// the agent's loop, which keeps one transport while neither the session's
// settings nor the configuration move, it builds again only when one of them
// did: two streams of a scenario are two requests of one provider.
func (l *rmpLocal) rowProvider(ctx context.Context) (llm.Provider, error) {
	st, err := l.session(ctx)
	if err != nil {
		return nil, err
	}
	cfg := l.mgr.Cfg()
	if l.built != nil && l.builtUnder == cfg && l.builtRev == st.SettingsRevision() {
		return l.built, nil
	}
	loop := agent.NewAgent(cfg, st, noopSender{}, l.log)
	var built llm.Provider
	var buildErr error
	loop.SetProviderFactory(func(in llm.ProviderInput) (llm.Provider, error) {
		if built == nil && buildErr == nil && in.Type == "coddy" {
			built, buildErr = llm.NewProvider(in)
		}
		return rmpEndTurnProvider{}, nil
	})
	if _, err := loop.Run(ctx, []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Get ready."}}); err != nil {
		return nil, fmt.Errorf("the local agent's turn failed: %w", err)
	}
	if buildErr != nil {
		return nil, fmt.Errorf("the local agent could not build the provider: %w", buildErr)
	}
	if built == nil {
		return nil, fmt.Errorf("the local agent never built a provider of type coddy")
	}
	l.built, l.builtUnder, l.builtRev = built, cfg, st.SettingsRevision()
	return built, nil
}

// configRow reads the models[] row of selector back from the local config file.
func (l *rmpLocal) configRow(selector string) (map[string]any, error) {
	raw, err := os.ReadFile(l.cfgPath)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Models []map[string]any `yaml:"models"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	for _, row := range doc.Models {
		if row["model"] == selector {
			return row, nil
		}
	}
	return nil, fmt.Errorf("the local config file has no row %q:\n%s", selector, raw)
}

// restart stops the local Coddy and boots it again from the file, as a new
// process does: nothing it held in memory survives.
func (l *rmpLocal) restart() error {
	l.stop()
	return l.boot()
}

func (l *rmpLocal) stop() {
	if l.ts != nil {
		l.ts.CloseClientConnections()
		l.ts.Close()
		l.ts = nil
	}
	if l.srv != nil {
		l.srv.Drain()
		l.srv = nil
	}
	if l.mgr != nil {
		l.mgr.CloseMCP()
		l.mgr = nil
	}
	l.sid, l.built, l.builtUnder, l.builtRev = "", nil, nil, 0
}

func (l *rmpLocal) close() {
	l.stop()
	if l.weather != nil {
		l.weather.Close()
		l.weather = nil
	}
}

// ---- the local tool ----

// rmpWeatherMCP is a streamable HTTP MCP server on the local side with one
// tool, get_weather, answering result to every call; it records the arguments
// of each call.
type rmpWeatherMCP struct {
	*httptest.Server
	URL    string
	result string

	mu    sync.Mutex
	calls []string
}

func newRMPWeatherMCP(result string) *rmpWeatherMCP {
	m := &rmpWeatherMCP{result: result}
	m.Server = httptest.NewServer(http.HandlerFunc(m.serve))
	m.URL = m.Server.URL + "/mcp"
	return m
}

func (m *rmpWeatherMCP) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.ID) == 0 || string(req.ID) == "null" {
		// A notification has nothing to answer.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any = map[string]any{}
	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "weather", "version": "1"},
		}
	case "tools/list":
		result = map[string]any{"tools": []any{map[string]any{
			"name":        "get_weather",
			"description": "The weather in a city.",
			"inputSchema": rmpWeatherSchema(),
		}}}
	case "tools/call":
		m.mu.Lock()
		m.calls = append(m.calls, req.Params.Name+" "+string(req.Params.Arguments))
		m.mu.Unlock()
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": m.result}}}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func (m *rmpWeatherMCP) snapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}
