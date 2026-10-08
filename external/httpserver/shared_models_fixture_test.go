//go:build http

package httpserver

// The fixture the shared-model tests stand on: a remote Coddy built from a real
// Server behind httptest, with the provider factory replaced by a scripted
// stub, a clock the test advances by hand, and readers for the event stream.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// sharedTestClient bounds every call a test makes, so a handler that never
// answers fails the test at once instead of holding it until the package times out.
var sharedTestClient = &http.Client{Timeout: 30 * time.Second}

// The names the tests must never see on the wire.
const (
	sharedTestSelector   = "stub/qwen3-secret"
	sharedTestPrivate    = "stub/private-model"
	sharedTestUpstreamID = "qwen3-secret"
	sharedTestAlias      = "coder"
	sharedTestMainToken  = "main-token"
	sharedTestSharedTok  = "shared-token"
	sharedTestUpstreamAB = "http://stub-upstream.invalid/v1"
)

// sharedRunFunc is what a stub provider does for one call.
type sharedRunFunc func(ctx context.Context, call int, msgs []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error)

// sharedStub is the provider behind the factory: it records what it was given
// and runs a script.
type sharedStub struct {
	mu    sync.Mutex
	calls int
	msgs  [][]llm.Message
	tools [][]llm.ToolDefinition
	run   sharedRunFunc
}

func (p *sharedStub) Complete(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, msgs, tools, nil)
}

func (p *sharedStub) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.msgs = append(p.msgs, append([]llm.Message(nil), msgs...))
	p.tools = append(p.tools, append([]llm.ToolDefinition(nil), tools...))
	run := p.run
	p.mu.Unlock()
	if onChunk == nil {
		onChunk = func(llm.StreamChunk) {}
	}
	if run == nil {
		onChunk(llm.StreamChunk{TextDelta: "ok"})
		return &llm.Response{Content: "ok", InputTokens: 3, OutputTokens: 1}, nil
	}
	return run(ctx, n, msgs, tools, onChunk)
}

func (p *sharedStub) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *sharedStub) lastMessages() []llm.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.msgs) == 0 {
		return nil
	}
	return p.msgs[len(p.msgs)-1]
}

// fakeSharedClock is a clock the test moves by hand.
type fakeSharedClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeSharedTimer
}

func newFakeSharedClock() *fakeSharedClock {
	return &fakeSharedClock{now: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeSharedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeSharedClock) NewTimer(d time.Duration) sharedTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeSharedTimer{clk: c, when: c.now.Add(d), active: true, ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the clock and fires every armed timer that is due, then waits
// until the owner of each timer it fired has armed it again or stopped it. A
// real timer is re-armed relative to the moment of the Reset, so a test that
// advances again before the owner got to it would move the next deadline a whole
// interval away: the owner of the heartbeat writes its comment, the test reads
// it and advances, and only then does the owner re-arm the timer.
func (c *fakeSharedClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var fired []*fakeSharedTimer
	for _, t := range c.timers {
		if t.active && !t.when.After(c.now) {
			t.active, t.touched = false, false
			select {
			case t.ch <- c.now:
			default:
			}
			fired = append(fired, t)
		}
	}
	c.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for _, t := range fired {
		for time.Now().Before(deadline) {
			c.mu.Lock()
			settled := t.touched
			c.mu.Unlock()
			if settled {
				break
			}
			time.Sleep(50 * time.Microsecond)
		}
	}
}

// armed is the number of timers waiting to fire.
func (c *fakeSharedClock) armed() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if t.active {
			n++
		}
	}
	return n
}

type fakeSharedTimer struct {
	clk    *fakeSharedClock
	when   time.Time
	active bool
	// touched says the owner armed or stopped the timer since it last fired.
	touched bool
	ch      chan time.Time
}

func (t *fakeSharedTimer) C() <-chan time.Time { return t.ch }

func (t *fakeSharedTimer) Reset(d time.Duration) {
	t.clk.mu.Lock()
	defer t.clk.mu.Unlock()
	t.when = t.clk.now.Add(d)
	t.active, t.touched = true, true
	select {
	case <-t.ch:
	default:
	}
}

func (t *fakeSharedTimer) Stop() {
	t.clk.mu.Lock()
	defer t.clk.mu.Unlock()
	t.active, t.touched = false, true
}

// sharedFixture is one remote Coddy under test.
type sharedFixture struct {
	t    *testing.T
	home string
	cfg  *config.Config
	srv  *Server
	ts   *httptest.Server
	stub *sharedStub
	clk  *fakeSharedClock
	log  *slog.Logger

	mu       sync.Mutex
	built    []string
	buildOps []llm.RequestOptions
}

type sharedFixtureOption func(*sharedFixture, *config.Config)

// withSharedConfig mutates the configuration before the server is built.
func withSharedConfig(f func(*config.Config)) sharedFixtureOption {
	return func(_ *sharedFixture, c *config.Config) { f(c) }
}

// withSharedLogger gives the server a logger of the test's.
func withSharedLogger(l *slog.Logger) sharedFixtureOption {
	return func(fx *sharedFixture, _ *config.Config) { fx.log = l }
}

// withFakeClock gives the server a clock the test advances.
func withFakeClock() sharedFixtureOption {
	return func(fx *sharedFixture, _ *config.Config) { fx.clk = newFakeSharedClock() }
}

// sharedBaseConfig is a remote that shares "stub/qwen3-secret" as "coder" and
// keeps "stub/private-model" to itself, behind a main token and a shared one.
func sharedBaseConfig(home string) *config.Config {
	levels := []string{"low", "high"}
	return &config.Config{
		Paths: config.Paths{Home: home, CWD: home, ConfigPath: home + "/config.yaml"},
		Providers: []config.ProviderConfig{
			{Name: "stub", Type: "openai", APIBase: sharedTestUpstreamAB, APIKey: "upstream-secret-key"},
		},
		Models: []config.ModelEntry{
			{Model: sharedTestSelector, MaxTokens: 4096, MaxContextTokens: 200000, ReasoningLevels: &levels, ReasoningDefault: "low", SharedAs: sharedTestAlias},
			{Model: sharedTestPrivate, MaxTokens: 1024, MaxContextTokens: 1000},
		},
		HTTPServer: config.HTTPServerConfig{
			AuthToken:    sharedTestMainToken,
			SharedModels: config.SharedModelsConfig{Tokens: []string{sharedTestSharedTok}},
		},
	}
}

func newSharedFixture(t *testing.T, opts ...sharedFixtureOption) *sharedFixture {
	t.Helper()
	home := t.TempDir()
	fx := &sharedFixture{t: t, home: home, stub: &sharedStub{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	cfg := sharedBaseConfig(home)
	for _, o := range opts {
		o(fx, cfg)
	}
	fx.cfg = cfg
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, fx.log, home, nil)
	fx.srv = New(cfg, mgr, fx.log, home)
	if fx.clk != nil {
		fx.srv.sharedClk = fx.clk
	}
	fx.srv.makeLLMFromYAML = func(_ *config.Config, sel string, o llm.RequestOptions) (llm.Provider, error) {
		fx.recordBuild(sel, o)
		return fx.stub, nil
	}
	// Built the way production builds its listener (httpx.NewServer sets the
	// same hook), so a call reaches the socket under its request and the per-call
	// user timeout of the liveness bound is exercised by every test of the routes.
	fx.ts = httptest.NewUnstartedServer(fx.srv.Handler())
	fx.ts.Config.ConnContext = httpx.ConnContext
	fx.ts.Start()
	t.Cleanup(fx.ts.Close)
	return fx
}

func (fx *sharedFixture) recordBuild(sel string, o llm.RequestOptions) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.built = append(fx.built, sel)
	fx.buildOps = append(fx.buildOps, o)
}

func (fx *sharedFixture) buildCount() int {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return len(fx.built)
}

func (fx *sharedFixture) lastBuild() (string, llm.RequestOptions) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if len(fx.built) == 0 {
		fx.t.Fatal("the provider factory was never called")
	}
	i := len(fx.built) - 1
	return fx.built[i], fx.buildOps[i]
}

// wireReq is a minimal valid request for alias, changed by mut.
func wireReq(alias string, mut ...func(*llm.WireRequest)) llm.WireRequest {
	r := llm.WireRequest{
		Protocol: llm.CoddyProtocol,
		Model:    alias,
		Messages: []llm.WireMessage{{Role: "user", Content: "Hi"}},
	}
	for _, m := range mut {
		m(&r)
	}
	return r
}

func (fx *sharedFixture) request(method, path, token string, body io.Reader) *http.Request {
	fx.t.Helper()
	req, err := http.NewRequest(method, fx.ts.URL+path, body)
	if err != nil {
		fx.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func (fx *sharedFixture) get(path, token string) *http.Response {
	fx.t.Helper()
	resp, err := sharedTestClient.Do(fx.request(http.MethodGet, path, token, nil))
	if err != nil {
		fx.t.Fatal(err)
	}
	return resp
}

func (fx *sharedFixture) post(path, token string, v any) *http.Response {
	fx.t.Helper()
	raw, ok := v.([]byte)
	if !ok {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			fx.t.Fatal(err)
		}
	}
	resp, err := sharedTestClient.Do(fx.request(http.MethodPost, path, token, bytes.NewReader(raw)))
	if err != nil {
		fx.t.Fatal(err)
	}
	return resp
}

// complete posts a completion request with the shared token.
func (fx *sharedFixture) complete(req llm.WireRequest) *http.Response {
	fx.t.Helper()
	return fx.post(llm.CoddyCompletionsPath, sharedTestSharedTok, req)
}

// sseEvent is one event of a response: a comment (the heartbeat) or a frame.
type sseEvent struct {
	comment string
	typ     string
	raw     []byte
}

// sseReader reads the events of a stream as they arrive.
type sseReader struct {
	t  *testing.T
	br *bufio.Reader
	ch chan sseEvent
}

func newSSEReader(t *testing.T, resp *http.Response) *sseReader {
	t.Helper()
	r := &sseReader{t: t, br: bufio.NewReader(resp.Body), ch: make(chan sseEvent, 4096)}
	go func() {
		defer close(r.ch)
		var data []byte
		var comment string
		for {
			line, err := r.br.ReadString('\n')
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, ":"):
				comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
			case strings.HasPrefix(line, "data: "):
				data = append(data, []byte(strings.TrimPrefix(line, "data: "))...)
			case line == "":
				switch {
				case comment != "":
					r.ch <- sseEvent{comment: comment}
				case len(data) > 0:
					var head struct {
						Type string `json:"type"`
					}
					_ = json.Unmarshal(data, &head)
					r.ch <- sseEvent{typ: head.Type, raw: data}
				}
				data, comment = nil, ""
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

// next waits for the next event.
func (r *sseReader) next(d time.Duration) (sseEvent, bool) {
	r.t.Helper()
	select {
	case ev, ok := <-r.ch:
		return ev, ok
	case <-time.After(d):
		r.t.Fatalf("no event within %v", d)
		return sseEvent{}, false
	}
}

// all reads until the stream ends.
func (r *sseReader) all() []sseEvent {
	r.t.Helper()
	var out []sseEvent
	for {
		ev, ok := r.next(10 * time.Second)
		if !ok {
			return out
		}
		out = append(out, ev)
	}
}

func terminalEvents(evs []sseEvent) []sseEvent {
	var out []sseEvent
	for _, e := range evs {
		if e.typ == llm.WireTypeFinal || e.typ == llm.WireTypeError {
			out = append(out, e)
		}
	}
	return out
}

// readError reads the error object of a refused request.
func readError(t *testing.T, resp *http.Response) llm.WireError {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var e llm.WireError
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("not an error object (%d): %s", resp.StatusCode, raw)
	}
	return e
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// waitFor polls cond for up to five seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// rawCall opens a TCP connection to the server and returns it with a reader of
// the response, for the cases a Go client cannot produce (a header with no
// body, a body that stalls, a peer that never reads).
func (fx *sharedFixture) rawConn(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(fx.ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, bufio.NewReader(conn)
}

func rawHeaders(path, token string, contentLength int) string {
	var b strings.Builder
	b.WriteString("POST " + path + " HTTP/1.1\r\nHost: remote\r\nContent-Type: application/json\r\n")
	if token != "" {
		b.WriteString("Authorization: Bearer " + token + "\r\n")
	}
	b.WriteString("Content-Length: " + strconv.Itoa(contentLength) + "\r\n\r\n")
	return b.String()
}
