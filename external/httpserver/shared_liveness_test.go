//go:build http

package httpserver

// The liveness of a peer of a shared-model call on the direct listener
// (docs/plans/remote-model-provider-phase2.md, 5.2): the slot of a peer that
// vanished is freed at most B after it, by a per-call TCP user timeout
// U = B - H that the handler sets after it has read the request body and
// restores on every exit. Most of these tests prove the plumbing - the option is
// set where it must be and nowhere else, and a dead connection cancels the call;
// the kernel itself is proved by the opt-in test at the end of the file.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// ---------------------------------------------------------------------------
// Constants and seams
// ---------------------------------------------------------------------------

// The relay's mount cannot import this package, so B and H live in internal/httpx
// and the handler's own constants are held equal to them here.
func TestSharedLivenessConstantsEqualTheOnesTheMountShares(t *testing.T) {
	if sharedLivenessBound != httpx.LivenessBound {
		t.Fatalf("sharedLivenessBound %v, httpx.LivenessBound %v", sharedLivenessBound, httpx.LivenessBound)
	}
	if sharedHeartbeat != httpx.LivenessHeartbeat {
		t.Fatalf("sharedHeartbeat %v, httpx.LivenessHeartbeat %v", sharedHeartbeat, httpx.LivenessHeartbeat)
	}
	if sharedLivenessBound != 45*time.Second || sharedHeartbeat != 15*time.Second {
		t.Fatalf("B = %v and H = %v, want 45s and 15s", sharedLivenessBound, sharedHeartbeat)
	}
	// B = H + U, and the body deadline P stays within B: a peer that vanishes
	// during the upload is bounded by P, the option is set after it.
	if u := httpx.CallUserTimeout(sharedLivenessBound, sharedHeartbeat); u != 30*time.Second || sharedHeartbeat+u != sharedLivenessBound {
		t.Fatalf("U = %v, want 30s with H + U = B", u)
	}
	if sharedBodyDeadline > sharedLivenessBound {
		t.Fatalf("P = %v exceeds B = %v", sharedBodyDeadline, sharedLivenessBound)
	}
}

func TestSharedTimingsReturnTheBoundAndClampTheHeartbeat(t *testing.T) {
	fx := newSharedFixture(t)
	body, write, hb, bound := fx.srv.sharedTimings()
	if body != 30*time.Second || write != 60*time.Second || hb != 15*time.Second || bound != 45*time.Second {
		t.Fatalf("defaults: P=%v W=%v H=%v B=%v", body, write, hb, bound)
	}

	// A test shortens B, H and P together.
	fx.srv.sharedBound, fx.srv.sharedHB, fx.srv.sharedBodyP = 9*time.Second, 2*time.Second, 5*time.Second
	if body, _, hb, bound = fx.srv.sharedTimings(); body != 5*time.Second || hb != 2*time.Second || bound != 9*time.Second {
		t.Fatalf("scaled: P=%v H=%v B=%v", body, hb, bound)
	}

	// A heartbeat longer than a third of the bound would leave U below two thirds
	// of B and cannot be bounded at all, so it is clamped.
	fx.srv.sharedBound, fx.srv.sharedHB = 9*time.Second, 20*time.Second
	if _, _, hb, bound = fx.srv.sharedTimings(); hb != 3*time.Second || bound != 9*time.Second {
		t.Fatalf("clamp: H=%v B=%v, want H = B/3 = 3s", hb, bound)
	}
	// With the default heartbeat and a shorter bound the same rule applies.
	fx.srv.sharedBound, fx.srv.sharedHB = 6*time.Second, 0
	if _, _, hb, _ = fx.srv.sharedTimings(); hb != 2*time.Second {
		t.Fatalf("H=%v, want 2s", hb)
	}
}

// Every server of this package's production code is built through
// httpx.NewServer, which sets ConnContext: the handler reaches the socket under
// its request through it. A server built another way must set
// srv.ConnContext = httpx.ConnContext by hand, and this test is where a new one
// is noticed.
func TestEveryServerOfThePackageIsBuiltThroughHTTPX(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, banned := range []string{"http.Server{", "http.ListenAndServe(", "http.ListenAndServeTLS(", "http.Serve("} {
			if strings.Contains(text, banned) && !strings.Contains(text, "httpx.ConnContext") {
				t.Errorf("%s builds a server with %s and does not set httpx.ConnContext: build it with httpx.NewServer", name, banned)
			}
		}
	}
	if httpx.NewServer(":0", http.NotFoundHandler()).ConnContext == nil {
		t.Fatal("httpx.NewServer does not set ConnContext")
	}
}

// ---------------------------------------------------------------------------
// A listener that can see and cut what the server accepted
// ---------------------------------------------------------------------------

// probeConn is an accepted connection the test can read the user timeout of and
// make fail the way a connection the kernel gave up on does.
type probeConn struct {
	*net.TCPConn
	dead atomic.Bool
}

// NetConn is what httpx.TCPConn follows, as it follows a *tls.Conn.
func (c *probeConn) NetConn() net.Conn { return c.TCPConn }

func (c *probeConn) Read(b []byte) (int, error) {
	n, err := c.TCPConn.Read(b)
	if c.dead.Load() {
		return 0, syscall.ETIMEDOUT
	}
	return n, err
}

// cut makes every read fail with ETIMEDOUT, the error the kernel's abort of a
// connection that ran out of its user timeout gives, and wakes a read in progress.
func (c *probeConn) cut() {
	c.dead.Store(true)
	_ = c.SetReadDeadline(time.Now())
}

func (c *probeConn) userTimeout(t *testing.T) time.Duration {
	t.Helper()
	d, err := platform.TCPUserTimeout(c.TCPConn)
	if err != nil {
		t.Fatalf("reading the user timeout: %v", err)
	}
	return d
}

// userTimeoutQuiet is userTimeout for a goroutine that is not the test's: an
// unreadable option is reported as -1 instead of failing from there.
func (c *probeConn) userTimeoutQuiet() time.Duration {
	d, err := platform.TCPUserTimeout(c.TCPConn)
	if err != nil {
		return -1
	}
	return d
}

type probeListener struct {
	net.Listener
	mu    sync.Mutex
	conns []*probeConn
}

func (l *probeListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return c, nil
	}
	pc := &probeConn{TCPConn: tc}
	l.mu.Lock()
	l.conns = append(l.conns, pc)
	l.mu.Unlock()
	return pc, nil
}

// count is the number of connections accepted so far.
func (l *probeListener) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.conns)
}

// conn is the n-th accepted connection, waited for.
func (l *probeListener) conn(t *testing.T, n int) *probeConn {
	t.Helper()
	var got *probeConn
	waitFor(t, "an accepted connection", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		if len(l.conns) > n {
			got = l.conns[n]
			return true
		}
		return false
	})
	return got
}

// serveWithProbe serves the fixture's handler on a listener of its own, built
// the way production builds it (httpx.NewServer), and returns the address.
func serveWithProbe(t *testing.T, fx *sharedFixture) (addr string, ln *probeListener) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln = &probeListener{Listener: raw}
	srv := httpx.NewServer("", fx.srv.Handler())
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return raw.Addr().String(), ln
}

// requireUserTimeout skips on a platform with no per-call option.
func requireUserTimeout(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := platform.TCPUserTimeout(c); err != nil {
		t.Skipf("no TCP_USER_TIMEOUT here: %v", err)
	}
}

// rawPeer talks HTTP/1.1 to the server over one connection it keeps, so a test
// can split a request across the moments it looks at the server's socket.
type rawPeer struct {
	t    *testing.T
	conn net.Conn
	br   *bufio.Reader
}

func dialRaw(t *testing.T, addr string) *rawPeer {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &rawPeer{t: t, conn: c, br: bufio.NewReader(c)}
}

func (p *rawPeer) write(s string) {
	p.t.Helper()
	if _, err := io.WriteString(p.conn, s); err != nil {
		p.t.Fatal(err)
	}
}

// readResponse reads one whole response and returns its status and body.
func (p *rawPeer) readResponse() (int, string) {
	p.t.Helper()
	_ = p.conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	resp, err := http.ReadResponse(p.br, nil)
	if err != nil {
		p.t.Fatalf("reading the response: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		p.t.Fatalf("reading the body: %v", err)
	}
	return resp.StatusCode, string(raw)
}

// callBody is the JSON body of a call of the shared alias.
func callBody(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(wireReq(sharedTestAlias))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// slotsInUse is the number of calls the shared token holds.
func (fx *sharedFixture) slotsInUse() int {
	return fx.srv.sharedLimit.inUse(sharedKeyFor(sharedTestSharedTok))
}

// ---------------------------------------------------------------------------
// The option: where it is set and when it goes back
// ---------------------------------------------------------------------------

// The user timeout is the system default while the body is read, U = B - H for
// the call, and the system default again after it, whether the call ends with a
// final frame or an error frame: a connection that is kept alive must not carry
// the option into its next request.
func TestSharedCallSetsTheUserTimeoutAfterTheBodyAndRestoresItOnEveryExit(t *testing.T) {
	requireUserTimeout(t)
	for _, tc := range []struct {
		name string
		run  sharedRunFunc
		want string
	}{
		{"final frame", func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
			on(llm.StreamChunk{TextDelta: "ok"})
			return &llm.Response{Content: "ok"}, nil
		}, `"type":"final"`},
		{"error frame", func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
			return nil, errors.New("the provider failed")
		}, `"type":"error"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newSharedFixture(t)
			// B = 12 s and H = 2 s, so U = 10 s.
			fx.srv.sharedBound, fx.srv.sharedHB = 12*time.Second, 2*time.Second
			wantU := 10 * time.Second

			entered := make(chan time.Duration, 4)
			release := make(chan struct{})
			addr, ln := serveWithProbe(t, fx)
			var conn *probeConn
			fx.stub.run = func(ctx context.Context, n int, msgs []llm.Message, tools []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
				entered <- conn.userTimeoutQuiet()
				<-release
				return tc.run(ctx, n, msgs, tools, on)
			}
			peer := dialRaw(t, addr)
			conn = ln.conn(t, 0)
			body := callBody(t)

			// The body is half sent: the handler holds the slot and waits for the rest.
			peer.write(rawHeaders(llm.CoddyCompletionsPath, sharedTestSharedTok, len(body)))
			peer.write(body[:len(body)/2])
			waitFor(t, "the handler to hold its slot", func() bool { return fx.slotsInUse() == 1 })
			time.Sleep(50 * time.Millisecond)
			if got := conn.userTimeout(t); got != 0 {
				t.Fatalf("the user timeout while the body is read: %v, want the system default", got)
			}

			peer.write(body[len(body)/2:])
			select {
			case got := <-entered:
				if got != wantU {
					t.Fatalf("the user timeout during the call: %v, want B - H = %v", got, wantU)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the provider was never called")
			}
			if got := conn.userTimeout(t); got != wantU {
				t.Fatalf("the user timeout during the call, read again: %v, want %v", got, wantU)
			}

			close(release)
			status, stream := peer.readResponse()
			if status != http.StatusOK || !strings.Contains(stream, tc.want) {
				t.Fatalf("status %d: %s", status, stream)
			}
			waitFor(t, "the slot to be released", func() bool { return fx.slotsInUse() == 0 })
			if got := conn.userTimeout(t); got != 0 {
				t.Fatalf("the user timeout after the call: %v, want the system default", got)
			}

			// The next request on the same connection starts with the default.
			peer.write(rawHeaders(llm.CoddyCompletionsPath, sharedTestSharedTok, len(body)))
			peer.write(body[:len(body)/2])
			waitFor(t, "the second call to hold its slot", func() bool { return fx.slotsInUse() == 1 })
			time.Sleep(50 * time.Millisecond)
			if got := conn.userTimeout(t); got != 0 {
				t.Fatalf("the user timeout at the start of the next request on the connection: %v, want the system default", got)
			}
			if n := ln.count(); n != 1 {
				t.Fatalf("the test did not reuse the connection: %d accepted", n)
			}
		})
	}
}

// A request that is refused before the stream starts never sets the option.
func TestSharedCallRefusedBeforeTheStreamNeverSetsTheUserTimeout(t *testing.T) {
	requireUserTimeout(t)
	fx := newSharedFixture(t)
	addr, ln := serveWithProbe(t, fx)
	peer := dialRaw(t, addr)
	conn := ln.conn(t, 0)
	body, _ := json.Marshal(wireReq("no-such-alias"))
	peer.write(rawHeaders(llm.CoddyCompletionsPath, sharedTestSharedTok, len(body)) + string(body))
	if status, raw := peer.readResponse(); status != http.StatusNotFound {
		t.Fatalf("status %d: %s", status, raw)
	}
	waitFor(t, "the slot to be released", func() bool { return fx.slotsInUse() == 0 })
	if got := conn.userTimeout(t); got != 0 {
		t.Fatalf("the user timeout after a refusal: %v", got)
	}
}

// No other route sets the option: the listing, the usage route and the browser's
// own streams keep the system default.
func TestSharedUserTimeoutIsSetOnTheCompletionsRouteOnly(t *testing.T) {
	requireUserTimeout(t)
	fx := newSharedFixture(t)
	addr, ln := serveWithProbe(t, fx)
	peer := dialRaw(t, addr)
	conn := ln.conn(t, 0)
	for _, path := range []string{llm.CoddyModelsPath, "/coddy/llm/models/" + sharedTestAlias + "/usage", "/v1/models"} {
		peer.write("GET " + path + " HTTP/1.1\r\nHost: remote\r\nAuthorization: Bearer " + sharedTestMainToken + "\r\n\r\n")
		if status, _ := peer.readResponse(); status != http.StatusOK {
			t.Fatalf("GET %s: %d", path, status)
		}
		if got := conn.userTimeout(t); got != 0 {
			t.Fatalf("after GET %s the user timeout is %v", path, got)
		}
	}
}

// ---------------------------------------------------------------------------
// A dead connection frees the slot
// ---------------------------------------------------------------------------

// A connection that fails with ETIMEDOUT while the call streams heartbeats, the
// error the kernel's abort gives, cancels the provider's context and frees the
// slot at once, and the call is logged as client_gone. (Whether the kernel does
// that is the opt-in test below; this is the plumbing from the error on.)
func TestSharedVanishedPeerFreesTheSlotWhenItsConnectionFails(t *testing.T) {
	logs := &syncBuffer{}
	fx := newSharedFixture(t, withSharedLogger(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	fx.srv.sharedHB = 20 * time.Millisecond // heartbeats keep being written while the peer is gone
	addr, ln := serveWithProbe(t, fx)

	entered := make(chan struct{})
	cancelled := make(chan time.Time, 1)
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
		close(entered)
		<-ctx.Done()
		cancelled <- time.Now()
		return nil, ctx.Err()
	}
	peer := dialRaw(t, addr)
	conn := ln.conn(t, 0)
	body := callBody(t)
	peer.write(rawHeaders(llm.CoddyCompletionsPath, sharedTestSharedTok, len(body)) + body)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the provider was never called")
	}
	if fx.slotsInUse() != 1 {
		t.Fatalf("slots in use: %d", fx.slotsInUse())
	}

	cutAt := time.Now()
	conn.cut()
	select {
	case at := <-cancelled:
		if d := at.Sub(cutAt); d > 100*time.Millisecond {
			t.Fatalf("the provider's context ended %v after the connection failed, want within 100ms", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the provider's context was never cancelled")
	}
	waitFor(t, "the slot to be released", func() bool { return fx.slotsInUse() == 0 })
	if d := time.Since(cutAt); d > 500*time.Millisecond {
		t.Fatalf("the slot was freed %v after the connection failed", d)
	}
	waitFor(t, "the call to be logged", func() bool { return strings.Contains(logs.String(), "kind=client_gone") })
}

// ---------------------------------------------------------------------------
// The leg the option cannot scope: HTTP/2, and a listener behind TLS
// ---------------------------------------------------------------------------

func startTLSProbe(t *testing.T, fx *sharedFixture, http2 bool) (*httptest.Server, *probeListener) {
	t.Helper()
	ts := httptest.NewUnstartedServer(fx.srv.Handler())
	ts.EnableHTTP2 = http2
	ts.Config.ConnContext = httpx.ConnContext
	ln := &probeListener{Listener: ts.Listener}
	ts.Listener = ln
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts, ln
}

// An HTTP/2 stream shares its connection with others, so the handler gets the
// option refused and carries on: the call completes, and the socket is never
// touched.
func TestSharedCallOverHTTP2IsServedWithoutTheOption(t *testing.T) {
	requireUserTimeout(t)
	fx := newSharedFixture(t)
	ts, ln := startTLSProbe(t, fx, true)

	var during atomic.Int64
	during.Store(-2)
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		ln.mu.Lock()
		c := ln.conns[0]
		ln.mu.Unlock()
		during.Store(int64(c.userTimeoutQuiet()))
		on(llm.StreamChunk{TextDelta: "ok"})
		return &llm.Response{Content: "ok"}, nil
	}
	client := ts.Client()
	body, _ := json.Marshal(wireReq(sharedTestAlias))
	req, _ := http.NewRequest(http.MethodPost, ts.URL+llm.CoddyCompletionsPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+sharedTestSharedTok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("the test client spoke HTTP/%d, want 2", resp.ProtoMajor)
	}
	raw := bodyString(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(raw, `"type":"final"`) {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if got := during.Load(); got != 0 {
		t.Fatalf("the user timeout during an HTTP/2 call: %v, want the system default", time.Duration(got))
	}
	waitFor(t, "the slot to be released", func() bool { return fx.slotsInUse() == 0 })
	if got := ln.conn(t, 0).userTimeout(t); got != 0 {
		t.Fatalf("the user timeout after an HTTP/2 call: %v", got)
	}
}

// Behind TLS an HTTP/1.1 client is probed like a plain one: the handler finds the
// socket under the *tls.Conn.
func TestSharedCallOverTLSWithHTTP11SetsTheOptionOnTheUnderlyingSocket(t *testing.T) {
	requireUserTimeout(t)
	fx := newSharedFixture(t)
	fx.srv.sharedBound, fx.srv.sharedHB = 12*time.Second, 2*time.Second
	ts, ln := startTLSProbe(t, fx, false) // no h2 offered: the client falls back to HTTP/1.1

	var during atomic.Int64
	fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, on func(llm.StreamChunk)) (*llm.Response, error) {
		ln.mu.Lock()
		c := ln.conns[0]
		ln.mu.Unlock()
		during.Store(int64(c.userTimeoutQuiet()))
		return &llm.Response{Content: "ok"}, nil
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // the test server's own certificate
		TLSNextProto:    map[string]func(string, *tls.Conn) http.RoundTripper{},
	}}
	t.Cleanup(client.CloseIdleConnections)
	body, _ := json.Marshal(wireReq(sharedTestAlias))
	req, _ := http.NewRequest(http.MethodPost, ts.URL+llm.CoddyCompletionsPath, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+sharedTestSharedTok)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProtoMajor != 1 {
		t.Fatalf("the test client spoke HTTP/%d, want 1", resp.ProtoMajor)
	}
	if raw := bodyString(t, resp); !strings.Contains(raw, `"type":"final"`) {
		t.Fatalf("the call did not complete: %s", raw)
	}
	if got := time.Duration(during.Load()); got != 10*time.Second {
		t.Fatalf("the user timeout during the call behind TLS: %v, want 10s", got)
	}
	waitFor(t, "the slot to be released", func() bool { return fx.slotsInUse() == 0 })
	if got := ln.conn(t, 0).userTimeout(t); got != 0 {
		t.Fatalf("the user timeout after the call behind TLS: %v", got)
	}
}

// ---------------------------------------------------------------------------
// The kernel (opt-in)
// ---------------------------------------------------------------------------

// What the per-call user timeout does to a peer that really stops answering is
// the kernel's doing, and only the kernel can prove it. This test is opt-in and
// stays out of `make test`: set CODDY_TEST_NETNS=1 on Linux with unshare, ip and
// iptables installed and unprivileged user namespaces allowed. It re-executes its
// own binary under `unshare -Urn` (root in a fresh user and network namespace),
// brings loopback up and makes the peer vanish with an iptables DROP on the
// segments sent to its port, so the handler's bytes are never acknowledged and
// nothing ever resets the connection.
//
//	CODDY_TEST_NETNS=1 go test -tags=http ./external/httpserver -run VanishedPeer -v
//
// The bound is scaled to B = 12 s and H = 2 s, so U = 10 s (the experiment of
// docs/plans/remote-model-provider-phase2.md, 5.1). It takes about 35 seconds.
const (
	kernelParentEnv = "CODDY_TEST_NETNS"
	kernelChildEnv  = "CODDY_TEST_NETNS_CHILD"

	kernelBound     = 12 * time.Second
	kernelHeartbeat = 2 * time.Second
	kernelUserTO    = kernelBound - kernelHeartbeat
)

func TestSharedKernelVanishedPeer(t *testing.T) {
	if os.Getenv(kernelParentEnv) == "" {
		t.Skip("opt-in kernel test: set CODDY_TEST_NETNS=1 (Linux, unshare, ip and iptables, user namespaces)")
	}
	if os.Getenv(kernelChildEnv) == "" {
		runKernelVanishedPeerInNetns(t)
		return
	}
	runKernelVanishedPeerRows(t)
}

// runKernelVanishedPeerInNetns re-executes this test under unshare and relays its verdict.
func runKernelVanishedPeerInNetns(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the kernel test needs Linux")
	}
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("unshare is not installed")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(unshare, "-Urn", exe, "-test.run=^TestSharedKernelVanishedPeer$", "-test.v", "-test.timeout=5m")
	cmd.Env = append(os.Environ(), kernelChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	text := string(out)
	if strings.Contains(text, "--- SKIP: TestSharedKernelVanishedPeer (") {
		t.Skipf("the namespace is not usable here:\n%s", text)
	}
	if err != nil {
		if strings.Contains(text, "unshare failed") {
			t.Skipf("user namespaces are not allowed here: %v\n%s", err, text)
		}
		t.Fatalf("the kernel test failed in its namespace: %v\n%s", err, text)
	}
	t.Logf("in the namespace:\n%s", text)
}

func kernelIptables(t *testing.T, args ...string) error {
	t.Helper()
	out, err := exec.Command("iptables", args...).CombinedOutput()
	if err != nil {
		return errors.New("iptables " + strings.Join(args, " ") + ": " + err.Error() + ": " + strings.TrimSpace(string(out)))
	}
	return nil
}

// kernelPeer is a client of the handler on a real loopback connection whose
// local port is known, so segments sent to it can be dropped.
type kernelPeer struct {
	t     *testing.T
	conn  net.Conn
	port  int
	rule  []string
	lines chan string
}

func (k *kernelPeer) vanish() {
	k.t.Helper()
	k.rule = []string{"INPUT", "-p", "tcp", "--dport", strconv.Itoa(k.port), "-j", "DROP"}
	if err := kernelIptables(k.t, append([]string{"-I"}, k.rule...)...); err != nil {
		k.t.Fatal(err)
	}
}

func (k *kernelPeer) lift() {
	if k.rule == nil {
		return
	}
	if err := kernelIptables(k.t, append([]string{"-D"}, k.rule...)...); err != nil {
		k.t.Logf("lifting the rule: %v", err)
	}
	k.rule = nil
}

// runKernelVanishedPeerRows runs inside the namespace.
func runKernelVanishedPeerRows(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip is not installed")
	}
	if out, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Skipf("cannot bring loopback up: %v: %s", err, out)
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		t.Skip("iptables is not installed")
	}
	if err := kernelIptables(t, "-L", "-n"); err != nil {
		t.Skipf("iptables is not usable here: %v", err)
	}
	requireUserTimeout(t)

	// start opens a call on the real handler and waits for its first heartbeat.
	start := func(t *testing.T) (fx *sharedFixture, peer *kernelPeer, ended chan time.Time) {
		fx = newSharedFixture(t)
		fx.srv.sharedBound, fx.srv.sharedHB = kernelBound, kernelHeartbeat
		ended = make(chan time.Time, 1)
		entered := make(chan struct{})
		fx.stub.run = func(ctx context.Context, _ int, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamChunk)) (*llm.Response, error) {
			close(entered)
			<-ctx.Done()
			ended <- time.Now()
			return nil, ctx.Err()
		}
		addr, _ := serveWithProbe(t, fx)
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		peer = &kernelPeer{t: t, conn: c, port: c.LocalAddr().(*net.TCPAddr).Port, lines: make(chan string, 1024)}
		t.Cleanup(peer.lift)
		body := callBody(t)
		if _, err := io.WriteString(c, rawHeaders(llm.CoddyCompletionsPath, sharedTestSharedTok, len(body))+body); err != nil {
			t.Fatal(err)
		}
		// The peer reads and answers its half of the connection as a live client does.
		go func() {
			br := bufio.NewReader(c)
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					close(peer.lines)
					return
				}
				peer.lines <- strings.TrimRight(line, "\r\n")
			}
		}()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the provider was never called")
		}
		return fx, peer, ended
	}

	t.Run("a peer that vanishes frees the slot at U after its first unacknowledged byte", func(t *testing.T) {
		fx, peer, ended := start(t)
		waitFor(t, "the call to hold its slot", func() bool { return fx.slotsInUse() == 1 })
		peer.vanish()
		vanishedAt := time.Now()

		select {
		case at := <-ended:
			elapsed := at.Sub(vanishedAt)
			t.Logf("the call ended %v after the peer vanished (U = %v, B = %v)", elapsed.Round(10*time.Millisecond), kernelUserTO, kernelBound)
			// The first heartbeat written after the vanish is the first unacknowledged
			// byte, at most H later, and the kernel aborts U after that.
			if elapsed < kernelUserTO {
				t.Fatalf("the call ended after %v, before U = %v could have run out", elapsed, kernelUserTO)
			}
			if limit := kernelBound + 1500*time.Millisecond; elapsed > limit {
				t.Fatalf("the call ended after %v, later than B = %v (plus 1.5s of timer slack)", elapsed, kernelBound)
			}
		case <-time.After(kernelBound + 8*time.Second):
			t.Fatalf("the call still ran %v after the peer vanished", time.Since(vanishedAt).Round(time.Millisecond))
		}
		waitFor(t, "the slot to be released", func() bool { return fx.slotsInUse() == 0 })
	})

	t.Run("an outage shorter than U that outlasts the last retransmission before it still cuts the call", func(t *testing.T) {
		fx, peer, ended := start(t)
		waitFor(t, "the call to hold its slot", func() bool { return fx.slotsInUse() == 1 })
		peer.vanish()
		vanishedAt := time.Now()
		time.Sleep(kernelUserTO - 500*time.Millisecond) // 9.5 s of 10: the retransmissions stop at about 6.2 s
		peer.lift()

		// The next retransmission would come at about 12.6 s, after the kernel gave
		// up at U (+0.4 s) counted from the first unacknowledged byte: the peer is
		// back and the call is cut all the same.
		select {
		case at := <-ended:
			elapsed := at.Sub(vanishedAt)
			t.Logf("the call ended %v after the peer vanished; the peer was back after %v (U = %v)", elapsed.Round(10*time.Millisecond), kernelUserTO-500*time.Millisecond, kernelUserTO)
			if limit := kernelBound + 1500*time.Millisecond; elapsed > limit {
				t.Fatalf("the call ended after %v, later than B = %v (plus 1.5s of timer slack)", elapsed, kernelBound)
			}
		case <-time.After(kernelBound + 8*time.Second):
			t.Fatal("the call survived an outage that outlasted the last retransmission before U")
		}
		waitFor(t, "the slot to be released", func() bool { return fx.slotsInUse() == 0 })
	})

	t.Run("a live peer whose link is silent for less than the last retransmission before U is not cut", func(t *testing.T) {
		fx, peer, ended := start(t)
		peer.vanish()
		time.Sleep(kernelUserTO / 2) // the retransmissions go out at about 0.2, 0.6, 1.4, 3.0 and 6.2 s
		peer.lift()

		// Heartbeats keep coming after the outage, and the call was never cut.
		got := 0
		deadline := time.After(kernelBound)
		for got < 3 {
			select {
			case _, ok := <-peer.lines:
				if !ok {
					t.Fatalf("the stream broke after a %v outage", kernelUserTO/2)
				}
				got++
			case <-ended:
				t.Fatalf("the call ended though the peer was back after %v", kernelUserTO/2)
			case <-deadline:
				t.Fatalf("no heartbeat arrived after a %v outage", kernelUserTO/2)
			}
		}
		if fx.slotsInUse() != 1 {
			t.Fatalf("slots in use: %d, want the call still held", fx.slotsInUse())
		}
	})
}
