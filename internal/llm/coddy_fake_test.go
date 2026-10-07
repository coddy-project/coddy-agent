package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRemote is a remote Coddy for the client's tests: an httptest server
// that records every request it gets and answers each with a script.
type fakeRemote struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	requests []fakeRemoteRequest
}

type fakeRemoteRequest struct {
	method string
	path   string
	header http.Header
	raw    []byte
	req    WireRequest
}

// fakeRemoteHandler answers request number n (1-based).
type fakeRemoteHandler func(w http.ResponseWriter, r *http.Request, n int, req WireRequest)

func newFakeRemote(t *testing.T, h fakeRemoteHandler) *fakeRemote {
	t.Helper()
	f := &fakeRemote{t: t}
	f.srv = httptest.NewServer(f.serve(h))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRemote) serve(h fakeRemoteHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := fakeRemoteRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone()}
		if r.Method == http.MethodPost {
			raw := new(bytes.Buffer)
			_, _ = raw.ReadFrom(r.Body)
			rec.raw = raw.Bytes()
			_ = json.Unmarshal(rec.raw, &rec.req)
		}
		f.mu.Lock()
		f.requests = append(f.requests, rec)
		n := len(f.requests)
		f.mu.Unlock()
		h(w, r, n, rec.req)
	})
}

func (f *fakeRemote) url() string { return f.srv.URL }

func (f *fakeRemote) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeRemote) request(n int) fakeRemoteRequest {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if n < 1 || n > len(f.requests) {
		f.t.Fatalf("remote saw %d requests, asked for #%d", len(f.requests), n)
	}
	return f.requests[n-1]
}

// stream starts an event stream answer and returns the writer of its frames.
func startStream(w http.ResponseWriter) *frameWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fw := &frameWriter{w: w}
	fw.flush()
	return fw
}

type frameWriter struct{ w http.ResponseWriter }

func (fw *frameWriter) flush() {
	if f, ok := fw.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (fw *frameWriter) raw(s string) {
	_, _ = fw.w.Write([]byte(s))
	fw.flush()
}

func (fw *frameWriter) frame(v any) {
	b, err := EncodeWireFrame(v)
	if err != nil {
		panic(err)
	}
	_, _ = fw.w.Write(b)
	fw.flush()
}

func (fw *frameWriter) heartbeat() { fw.raw(CoddyHeartbeat) }

func (fw *frameWriter) text(s string) { fw.frame(WireChunkFromStream(StreamChunk{TextDelta: s})) }
func (fw *frameWriter) reasoning(s string) {
	fw.frame(WireChunkFromStream(StreamChunk{ReasoningDelta: s}))
}
func (fw *frameWriter) final(r *Response)   { fw.frame(WireFinalFromResponse(r)) }
func (fw *frameWriter) fail(e WireError)    { fw.frame(e) }
func (fw *frameWriter) toolCall(c ToolCall) { fw.frame(WireChunkFromStream(StreamChunk{ToolCall: &c})) }

// answerWire writes a refused request: the flat error object as JSON.
func answerWire(w http.ResponseWriter, status int, e WireError, extra ...string) {
	w.Header().Set("Content-Type", "application/json")
	for i := 0; i+1 < len(extra); i += 2 {
		w.Header().Set(extra[i], extra[i+1])
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

// simpleAnswer streams one text chunk and a final.
func simpleAnswer(text string) fakeRemoteHandler {
	return func(w http.ResponseWriter, _ *http.Request, _ int, _ WireRequest) {
		fw := startStream(w)
		fw.text(text)
		fw.final(&Response{Content: text, StopReason: "end_turn", InputTokens: 12, OutputTokens: 3})
	}
}

// coddyInput is a provider input for a test remote.
func coddyInput(f *fakeRemote) ProviderInput {
	return ProviderInput{Name: "remote", Type: "coddy", Model: "coder", BaseURL: f.url(), APIKey: "shared-token", ProxyURL: "none"}
}

// newTestCoddy builds the whole provider chain NewProvider builds, over the
// loopback test server (a loopback address is never proxied).
func newTestCoddy(t *testing.T, in ProviderInput) Provider {
	t.Helper()
	p, err := NewProvider(in)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return p
}

// collect runs a Stream and gathers what the caller sees.
type collected struct {
	chunks []StreamChunk
	resp   *Response
	err    error
}

func streamOnce(p Provider, msgs []Message, tools []ToolDefinition) collected {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return streamCtx(ctx, p, msgs, tools)
}

func streamCtx(ctx context.Context, p Provider, msgs []Message, tools []ToolDefinition) collected {
	var c collected
	c.resp, c.err = p.Stream(ctx, msgs, tools, func(ch StreamChunk) { c.chunks = append(c.chunks, ch) })
	return c
}

func userMsg(s string) []Message { return []Message{{Role: RoleUser, Content: s}} }

// countingListener counts the bytes the server side of every connection reads.
type countingListener struct {
	net.Listener
	read atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: c, n: &l.read}, nil
}

type countingConn struct {
	net.Conn
	n *atomic.Int64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func bigText(n int) string { return strings.Repeat("x", n) }

// newCountingServer is a test server whose listener counts the bytes it reads.
func newCountingServer(t *testing.T, h http.HandlerFunc, out **countingListener) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	cl := &countingListener{Listener: srv.Listener}
	srv.Listener = cl
	srv.Start()
	*out = cl
	t.Cleanup(srv.Close)
	return srv
}

// mustProviderClient is the client NewProvider builds for a direct row.
func mustProviderClient(t *testing.T) *http.Client {
	t.Helper()
	hc, err := providerHTTPClient("none", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return hc
}
