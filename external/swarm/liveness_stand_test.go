//go:build swarm

package swarm

// The stand behind the mount's liveness tests: a relay (plain or TLS, with or
// without a faulty accepted connection), a node behind it (dialled directly or
// reaching the relay through the reverse tunnel) and a stand-in for the node's
// completions handler that holds a call open with heartbeats and counts the
// calls it holds - the slot of the real handler.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

const (
	standClientToken = "client-secret"
	standNodeName    = "inner"
	completionsPath  = "/coddy/llm/completions"
)

// recordLog keeps the message of every record, whatever its level.
type recordLog struct {
	mu   sync.Mutex
	msgs []string
}

func (l *recordLog) Enabled(context.Context, slog.Level) bool { return true }
func (l *recordLog) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	l.msgs = append(l.msgs, r.Message)
	l.mu.Unlock()
	return nil
}
func (l *recordLog) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *recordLog) WithGroup(string) slog.Handler      { return l }

func (l *recordLog) count(msg string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

// faultConn is a downstream connection that can start failing the way a socket
// the kernel aborted does: every write answers ETIMEDOUT, and a read blocked
// on it is woken to answer ETIMEDOUT. The real socket stays open and silent.
type faultConn struct {
	net.Conn
	failW, failR atomic.Bool
}

func (c *faultConn) Write(p []byte) (int, error) {
	if c.failW.Load() {
		return 0, syscall.ETIMEDOUT
	}
	return c.Conn.Write(p)
}

func (c *faultConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if c.failR.Load() {
		return 0, syscall.ETIMEDOUT
	}
	return n, err
}

func (c *faultConn) NetConn() net.Conn { return c.Conn }

func (c *faultConn) abortReads() {
	c.failR.Store(true)
	_ = c.SetReadDeadline(time.Unix(1, 0))
}

type faultListener struct {
	net.Listener
	stand *liveStand
}

func (l *faultListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	fc := &faultConn{Conn: c}
	l.stand.mu.Lock()
	l.stand.faults = append(l.stand.faults, fc)
	l.stand.mu.Unlock()
	return fc, nil
}

// callEnd is how a call of the node ended.
type callEnd struct {
	path string
	// cancelled is true when the node's request context ended, which is how a
	// downstream that died reaches the node; false when the call ran to its
	// final frame or to an error answer.
	cancelled bool
	at        time.Time
}

// probeCall is what the node saw when a call arrived.
type probeCall struct {
	method, path string
	// relayOption is the user timeout of the relay's client connection at the
	// moment the node started the call (what the relay set before it
	// forwarded the request).
	relayOption    time.Duration
	relayOptionErr error
}

// probeNode stands in for the node's completions handler.
type probeNode struct {
	heartbeat time.Duration
	// inUse counts the calls being held: the node's stream slots.
	inUse   atomic.Int32
	release chan struct{} // closed: every call finishes with its final frame
	started chan probeCall
	ended   chan callEnd
	observe func() (time.Duration, error)

	releaseOnce sync.Once
}

// releaseAll lets every held call, and every later one, finish.
func (n *probeNode) releaseAll() { n.releaseOnce.Do(func() { close(n.release) }) }

func newProbeNode(heartbeat time.Duration) *probeNode {
	return &probeNode{
		heartbeat: heartbeat,
		release:   make(chan struct{}),
		started:   make(chan probeCall, 64),
		ended:     make(chan callEnd, 64),
	}
}

func (n *probeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A node reads the request body before it answers.
	_, _ = io.Copy(io.Discard, r.Body)
	n.inUse.Add(1)
	defer n.inUse.Add(-1)

	call := probeCall{method: r.Method, path: r.URL.Path}
	if n.observe != nil {
		call.relayOption, call.relayOptionErr = n.observe()
	}
	n.started <- call

	if r.Header.Get("X-Probe-Fail") != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error":"boom"}`)
		n.ended <- callEnd{path: r.URL.Path, at: time.Now()}
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	tick := time.NewTicker(n.heartbeat)
	defer tick.Stop()
	for {
		_, _ = fmt.Fprint(w, "data: hb\n\n")
		if fl != nil {
			fl.Flush()
		}
		select {
		case <-n.release:
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			if fl != nil {
				fl.Flush()
			}
			n.ended <- callEnd{path: r.URL.Path, at: time.Now()}
			return
		case <-r.Context().Done():
			n.ended <- callEnd{path: r.URL.Path, cancelled: true, at: time.Now()}
			return
		case <-tick.C:
		}
	}
}

// waitIdle waits until the node holds no call, and reports whether it did.
func (n *probeNode) waitIdle(within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if n.inUse.Load() == 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return n.inUse.Load() == 0
}

type standOptions struct {
	// tls makes the relay speak TLS and offer HTTP/2 in ALPN.
	tls bool
	// tunnel makes the node reach the relay by dialling out.
	tunnel bool
	// fault wraps every connection the relay accepts in a faultConn.
	fault bool
	// noHook builds the relay's server without httpx.ConnContext, as a server
	// nobody gave the hook.
	noHook bool
	// heartbeat is the node's gap between frames (default 20ms).
	heartbeat time.Duration
	// nodePingAfter and nodePingTimeout scale the health check of a tunnel
	// node (zero: the decided 15 s and 30 s).
	nodePingAfter, nodePingTimeout time.Duration
}

type liveStand struct {
	t     *testing.T
	opts  standOptions
	log   *recordLog
	srv   *Server
	relay *httptest.Server
	node  *probeNode
	// nodeServer is the node's own listener of a direct node; closing it makes
	// the relay's dial fail.
	nodeServer *httptest.Server

	mu     sync.Mutex
	conns  []net.Conn // every connection the relay accepted, as the server sees it
	faults []*faultConn
}

func newLiveStand(t *testing.T, o standOptions) *liveStand {
	t.Helper()
	if o.heartbeat <= 0 {
		o.heartbeat = 20 * time.Millisecond
	}
	s := &liveStand{t: t, opts: o, log: &recordLog{}, node: newProbeNode(o.heartbeat)}
	s.node.observe = s.relayOption

	cfg := &config.Config{}
	cfg.Swarm.Host = "127.0.0.1"
	cfg.Swarm.AuthToken = standClientToken
	cfg.Swarm.PairingTokens = []string{"pair-secret"}
	srv, err := New(cfg, slog.New(s.log))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	s.srv = srv

	// The relay's listener as production builds it: httpx's ConnContext on
	// every connection.
	relay := httptest.NewUnstartedServer(srv.Handler())
	if !o.noHook {
		relay.Config.ConnContext = httpx.ConnContext
	}
	relay.Config.ConnState = func(c net.Conn, st http.ConnState) {
		if st != http.StateNew {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
	}
	if o.fault {
		relay.Listener = &faultListener{Listener: relay.Listener, stand: s}
	}
	if o.tls {
		relay.EnableHTTP2 = true
		relay.StartTLS()
	} else {
		relay.Start()
	}
	t.Cleanup(relay.Close)
	s.relay = relay

	if o.tunnel {
		s.startTunnelNode()
	} else {
		s.nodeServer = httptest.NewServer(s.node)
		t.Cleanup(s.nodeServer.Close)
		if _, err := srv.registry.Register(swarmdto.RegisterRequest{
			Name:         standNodeName,
			Kind:         swarmdto.KindAgent,
			Transport:    swarmdto.TransportDirect,
			AdvertiseURL: s.nodeServer.URL,
			InstanceUUID: "uuid-" + standNodeName,
			Token:        "node-secret",
			Version:      "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Registered last, so it runs first on cleanup: a call still held would keep
	// the relay's Close waiting.
	t.Cleanup(s.node.releaseAll)
	return s
}

// startTunnelNode registers the node without an address and has it dial the
// relay, through a plain listener of its own (a node dials the relay's plain
// address; the clients under test use the relay's own, possibly TLS, one).
func (s *liveStand) startTunnelNode() {
	t := s.t
	dial := httptest.NewServer(s.srv.Handler())
	t.Cleanup(dial.Close)
	res, err := s.srv.registry.Register(swarmdto.RegisterRequest{
		Name:         standNodeName,
		Kind:         swarmdto.KindAgent,
		Transport:    swarmdto.TransportTunnel,
		InstanceUUID: "uuid-" + standNodeName,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = swarmdto.DialTunnel(ctx, swarmdto.TunnelOptions{
			RelayURL:    dial.URL,
			Node:        standNodeName,
			LeaseSecret: res.LeaseSecret,
			Handler:     s.node,
			PingAfter:   s.opts.nodePingAfter,
			PingTimeout: s.opts.nodePingTimeout,
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if n, ok := s.srv.registry.Node(standNodeName); ok && n.Info.Online && n.Transport != nil && n.Transport.Alive() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the tunnel never came up")
}

// url is the address of a node route through the relay's mount.
func (s *liveStand) url(path string) string {
	return s.relay.URL + swarmdto.MountPath + standNodeName + path
}

// tcpUnder finds the *net.TCPConn below a connection and its wrappers.
func tcpUnder(c net.Conn) *net.TCPConn {
	for i := 0; i < 8 && c != nil; i++ {
		switch v := c.(type) {
		case *net.TCPConn:
			return v
		case interface{ NetConn() net.Conn }:
			c = v.NetConn()
		default:
			return nil
		}
	}
	return nil
}

// relayOption reads the user timeout of the newest connection the relay
// accepted: the client connection of the call under test.
func (s *liveStand) relayOption() (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.conns) == 0 {
		return 0, fmt.Errorf("the relay has accepted no connection")
	}
	tc := tcpUnder(s.conns[len(s.conns)-1])
	if tc == nil {
		return 0, fmt.Errorf("no TCP socket under the relay's connection")
	}
	return platform.TCPUserTimeout(tc)
}

func (s *liveStand) connCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// requireUserTimeout skips the test on a platform without the socket option,
// where the mount's behaviour is the logged no-op the other tests cover. It
// probes a socket of its own, so the relay's connection count stays the test's.
func (s *liveStand) requireUserTimeout() {
	s.t.Helper()
	requireUserTimeoutSupport(s.t)
}

func requireUserTimeoutSupport(t *testing.T) {
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

// h1Client is a client that speaks HTTP/1.1 to the relay over one kept-alive
// connection. dialed receives every raw connection it opens.
func (s *liveStand) h1Client(dialed func(net.Conn)) *http.Client {
	return &http.Client{Transport: s.transport(false, dialed)}
}

// h2Client is a client that speaks HTTP/2 to the TLS relay.
func (s *liveStand) h2Client(dialed func(net.Conn)) *http.Client {
	return &http.Client{Transport: s.transport(true, dialed)}
}

func (s *liveStand) transport(h2 bool, dialed func(net.Conn)) *http.Transport {
	var tr *http.Transport
	if s.opts.tls {
		tr = s.relay.Client().Transport.(*http.Transport).Clone()
	} else {
		tr = &http.Transport{}
	}
	tr.MaxConnsPerHost = 1
	tr.DisableKeepAlives = false
	if s.opts.tls {
		if h2 {
			tr.ForceAttemptHTTP2 = true
		} else {
			tr.ForceAttemptHTTP2 = false
			tr.Protocols = new(http.Protocols)
			tr.Protocols.SetHTTP1(true)
			// Clone prepares the original for HTTP/2 and carries its ALPN list over.
			tr.TLSClientConfig.NextProtos = []string{"http/1.1"}
		}
	}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err == nil && dialed != nil {
			dialed(c)
		}
		return c, err
	}
	return tr
}

// callResult is what a client got of one call.
type callResult struct {
	status int
	body   string
	proto  int
	err    error
}

// start runs a call in the background. The body of a streaming call is read to
// its end, which comes with the node's final frame.
func (s *liveStand) start(client *http.Client, method, path string, header map[string]string) <-chan callResult {
	out := make(chan callResult, 1)
	go func() {
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{}`)
		}
		req, err := http.NewRequest(method, s.url(path), body)
		if err != nil {
			out <- callResult{err: err}
			return
		}
		req.Header.Set("Authorization", "Bearer "+standClientToken)
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		res, err := client.Do(req)
		if err != nil {
			out <- callResult{err: err}
			return
		}
		defer func() { _ = res.Body.Close() }()
		b, err := io.ReadAll(res.Body)
		out <- callResult{status: res.StatusCode, body: string(b), proto: res.ProtoMajor, err: err}
	}()
	return out
}

// awaitStarted waits for the node to start the next call.
func (s *liveStand) awaitStarted() probeCall {
	s.t.Helper()
	select {
	case c := <-s.node.started:
		return c
	case <-time.After(5 * time.Second):
		s.t.Fatal("the node never saw the call")
		return probeCall{}
	}
}

// awaitEnded waits for the node to finish a call.
func (s *liveStand) awaitEnded(within time.Duration) (callEnd, bool) {
	select {
	case e := <-s.node.ended:
		return e, true
	case <-time.After(within):
		return callEnd{}, false
	}
}

func (s *liveStand) result(ch <-chan callResult) callResult {
	s.t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(10 * time.Second):
		s.t.Fatal("the client never got an answer")
		return callResult{}
	}
}

// openStream starts a call that the node holds open and returns once the
// client has read the first heartbeat, so the stream is known to flow through
// the relay. The caller closes the response.
func (s *liveStand) openStream(client *http.Client) *http.Response {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.url(completionsPath), strings.NewReader(`{}`))
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+standClientToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	line := make([]byte, len("data: hb\n\n"))
	if _, err := io.ReadFull(res.Body, line); err != nil {
		_ = res.Body.Close()
		s.t.Fatalf("the first heartbeat never arrived: %v", err)
	}
	return res
}

// failWrites makes every connection the relay accepted answer ETIMEDOUT to a
// write, as the kernel does after it aborted a socket.
func (s *liveStand) failWrites() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, fc := range s.faults {
		fc.failW.Store(true)
	}
}

// failReads wakes a read blocked on every accepted connection with ETIMEDOUT.
func (s *liveStand) failReads() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, fc := range s.faults {
		fc.abortReads()
	}
}
