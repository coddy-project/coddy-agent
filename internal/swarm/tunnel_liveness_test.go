package swarm

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
)

// The node pings after R of silence and gives the relay TP to answer: B = R + TP
// is the bound the whole feature promises, and the test fixes it to the
// constant the handler's heartbeat and user timeout are derived from.
func TestTunnelPingBudgetIsTheLivenessBound(t *testing.T) {
	if TunnelPingAfter != 15*time.Second {
		t.Fatalf("TunnelPingAfter = %v, want 15s", TunnelPingAfter)
	}
	if TunnelPingTimeout != 30*time.Second {
		t.Fatalf("TunnelPingTimeout = %v, want 30s", TunnelPingTimeout)
	}
	if sum := TunnelPingAfter + TunnelPingTimeout; sum != httpx.LivenessBound {
		t.Fatalf("TunnelPingAfter + TunnelPingTimeout = %v, want httpx.LivenessBound %v", sum, httpx.LivenessBound)
	}
	// The activity watchdog stays as the last resort, far beyond the check.
	if TunnelIdleTimeout < 2*httpx.LivenessBound {
		t.Fatalf("TunnelIdleTimeout %v is not a backstop behind the %v health check", TunnelIdleTimeout, httpx.LivenessBound)
	}
}

// With no override the node's HTTP/2 server carries the decided timers; an
// override replaces them for a scaled test and nothing else.
func TestTunnelServerCarriesTheDecidedPing(t *testing.T) {
	srv := newTunnelServer(TunnelOptions{})
	if srv.ReadIdleTimeout != TunnelPingAfter || srv.PingTimeout != TunnelPingTimeout {
		t.Fatalf("server pings after %v and waits %v, want %v and %v",
			srv.ReadIdleTimeout, srv.PingTimeout, TunnelPingAfter, TunnelPingTimeout)
	}
	if srv.MaxConcurrentStreams != TunnelMaxConcurrentStreams {
		t.Fatalf("MaxConcurrentStreams = %d, want %d", srv.MaxConcurrentStreams, TunnelMaxConcurrentStreams)
	}
	if srv.IdleTimeout != 0 {
		t.Fatalf("IdleTimeout = %v: HTTP/2's idle timeout ignores pings and stays off", srv.IdleTimeout)
	}
	scaled := newTunnelServer(TunnelOptions{PingAfter: 200 * time.Millisecond, PingTimeout: 400 * time.Millisecond})
	if scaled.ReadIdleTimeout != 200*time.Millisecond || scaled.PingTimeout != 400*time.Millisecond {
		t.Fatalf("scaled server pings after %v and waits %v", scaled.ReadIdleTimeout, scaled.PingTimeout)
	}
}

// gateConn is the relay's end of a tunnel whose reads can be switched off. A
// muted gate keeps reading the socket, so the node's writes are acknowledged by
// the kernel, but hands nothing to the relay's HTTP/2 client until it is
// unmuted: no frame is answered, no ping is acknowledged. That is what the node
// sees of a relay that vanished (the node's own writes still succeed into the
// send buffer).
type gateConn struct {
	net.Conn

	mu    sync.Mutex
	cond  *sync.Cond
	buf   []byte
	err   error
	muted bool
}

func newGateConn(c net.Conn) *gateConn {
	g := &gateConn{Conn: c}
	g.cond = sync.NewCond(&g.mu)
	go g.pump()
	return g
}

func (g *gateConn) pump() {
	chunk := make([]byte, 32<<10)
	for {
		n, err := g.Conn.Read(chunk)
		g.mu.Lock()
		g.buf = append(g.buf, chunk[:n]...)
		if err != nil {
			g.err = err
		}
		g.cond.Broadcast()
		g.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (g *gateConn) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		if !g.muted {
			if len(g.buf) > 0 {
				n := copy(p, g.buf)
				g.buf = g.buf[n:]
				return n, nil
			}
			if g.err != nil {
				return 0, g.err
			}
		}
		g.cond.Wait()
	}
}

func (g *gateConn) setMuted(v bool) {
	g.mu.Lock()
	g.muted = v
	g.cond.Broadcast()
	g.mu.Unlock()
}

// tunnelStand is a hand-made relay: it accepts one node, answers the upgrade
// and then drives the connection as the HTTP/2 client the real relay is.
type tunnelStand struct {
	t    *testing.T
	gate *gateConn
	cc   *http2.ClientConn
	done chan error // the node's DialTunnel result
	// held is closed when the node's handler for /hold has started.
	held chan struct{}
	// ended receives the time the node's handler for /hold saw its context end.
	ended chan time.Time
}

// startTunnelStand dials a node (scaled by ping) at a fake relay and returns
// once the HTTP/2 connection is up. The node's handler holds /hold until its
// request context ends and answers anything else at once.
func startTunnelStand(t *testing.T, ping TunnelOptions) *tunnelStand {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	s := &tunnelStand{t: t, done: make(chan error, 1), held: make(chan struct{}, 4), ended: make(chan time.Time, 4)}
	ready := make(chan struct{})
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		br := bufio.NewReader(conn)
		if _, rerr := http.ReadRequest(br); rerr != nil {
			_ = conn.Close()
			return
		}
		if _, werr := conn.Write(EncodeTunnelAccept("inner")); werr != nil {
			_ = conn.Close()
			return
		}
		s.gate = newGateConn(SpliceBuffered(conn, br))
		cc, cerr := (&http2.Transport{AllowHTTP: true}).NewClientConn(s.gate)
		if cerr != nil {
			_ = conn.Close()
			return
		}
		s.cc = cc
		close(ready)
	}()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/once" {
			// 201 for the first ask about the topic on this connection, 200 after.
			if httpx.OncePerConn(r, "tunnel-test") {
				w.WriteHeader(http.StatusCreated)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			return
		}
		if r.URL.Path != "/hold" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.held <- struct{}{}
		<-r.Context().Done()
		s.ended <- time.Now()
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	opts := ping
	opts.RelayURL = "http://" + ln.Addr().String()
	opts.Node = "inner"
	opts.LeaseSecret = "secret"
	opts.Handler = handler
	go func() { s.done <- DialTunnel(ctx, opts) }()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the tunnel never came up")
	}
	t.Cleanup(func() { s.gate.setMuted(false); _ = s.cc.Close() })
	return s
}

// hold starts the call the node keeps open and returns once the node's handler
// is running. The returned function cancels the call from the relay's side.
func (s *tunnelStand) hold() (cancel func()) {
	s.t.Helper()
	ctx, cancelCall := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://inner.swarm.invalid/hold", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	go func() {
		if res, rerr := s.cc.RoundTrip(req); rerr == nil {
			_ = res.Body.Close()
		}
	}()
	select {
	case <-s.held:
	case <-time.After(5 * time.Second):
		s.t.Fatal("the node's handler never started")
	}
	return cancelCall
}

// ok runs a short call through the tunnel and returns its status.
func (s *tunnelStand) ok() (int, error) {
	req, err := http.NewRequest(http.MethodGet, "http://inner.swarm.invalid/ok", nil)
	if err != nil {
		return 0, err
	}
	res, err := s.cc.RoundTrip(req)
	if err != nil {
		return 0, err
	}
	_ = res.Body.Close()
	return res.StatusCode, nil
}

// The requests of one tunnel share one connection, so a thing the node says once
// per connection (the shared call's HTTP/2 residual line, whose requests all
// arrive here as streams) is said once for the tunnel and not on every call: the
// tunnel's handler must see httpx.ConnContext like a listener's does.
func TestTunnelRequestsShareOneConnectionState(t *testing.T) {
	s := startTunnelStand(t, TunnelOptions{})
	var first int
	for i := 0; i < 3; i++ {
		req, err := http.NewRequest(http.MethodPost, "http://inner.swarm.invalid/once", nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.cc.RoundTrip(req)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		_ = res.Body.Close()
		if res.StatusCode == http.StatusCreated {
			first++
		} else if res.StatusCode != http.StatusOK {
			t.Fatalf("call %d: status %d", i, res.StatusCode)
		}
	}
	if first != 1 {
		t.Fatalf("OncePerConn answered true %d times over three calls on one tunnel, want exactly 1", first)
	}
}

// A relay that stops answering is closed by the node within R + TP of its last
// frame, and the call that relay was holding sees its context end - which on
// the real node is the handler that frees the slot. Scaled 200 ms / 400 ms.
func TestTunnelClosesARelayThatNeverAnswersAPing(t *testing.T) {
	const after, wait = 200 * time.Millisecond, 400 * time.Millisecond
	s := startTunnelStand(t, TunnelOptions{PingAfter: after, PingTimeout: wait})
	_ = s.hold()

	mutedAt := time.Now()
	s.gate.setMuted(true)

	select {
	case at := <-s.ended:
		elapsed := at.Sub(mutedAt)
		if elapsed < wait {
			t.Fatalf("the relay was cut after %v, before the ping had been unanswered for %v", elapsed, wait)
		}
		if limit := after + wait + time.Second; elapsed > limit {
			t.Fatalf("the relay was cut after %v, want within the bound %v (plus 1s slack)", elapsed, after+wait)
		}
	case <-time.After(after + wait + 5*time.Second):
		t.Fatal("a relay that never answers a ping kept the call for ever")
	}
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatalf("DialTunnel ended with %v, want nil (the caller redials)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DialTunnel did not return after the connection was closed")
	}
}

// A relay that answers late but inside TP is not cut: the ping waits behind
// unacknowledged bytes for a retransmission on a real link, and the longer
// timeout is what lets a live relay through.
func TestTunnelKeepsARelayThatAnswersAPingLate(t *testing.T) {
	const after, wait = 200 * time.Millisecond, 800 * time.Millisecond
	s := startTunnelStand(t, TunnelOptions{PingAfter: after, PingTimeout: wait})
	cancelCall := s.hold()

	s.gate.setMuted(true)
	// The ping goes out after `after`; answer it half way into its timeout.
	time.Sleep(after + wait/2)
	s.gate.setMuted(false)

	select {
	case <-s.ended:
		t.Fatal("a relay that answered within the ping timeout was cut")
	case <-time.After(2 * (after + wait)):
	}
	if status, err := s.ok(); err != nil || status != http.StatusNoContent {
		t.Fatalf("the tunnel stopped working after a late answer: status %d, err %v", status, err)
	}
	select {
	case err := <-s.done:
		t.Fatalf("DialTunnel ended (%v) though the relay answered", err)
	default:
	}
	cancelCall()
	select {
	case <-s.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the call from the relay did not end the node's handler")
	}
}

// A live relay is never cut because a call is silent for longer than R: the
// node pings, the relay answers, and the call goes on.
func TestTunnelKeepsALiveRelayWhileTheCallIsSilent(t *testing.T) {
	const after, wait = 100 * time.Millisecond, 300 * time.Millisecond
	s := startTunnelStand(t, TunnelOptions{PingAfter: after, PingTimeout: wait})
	cancelCall := s.hold()

	select {
	case <-s.ended:
		t.Fatal("a live relay was cut during a silent call")
	case <-time.After(3 * (after + wait)):
	}
	if status, err := s.ok(); err != nil || status != http.StatusNoContent {
		t.Fatalf("a call after the silence: status %d, err %v", status, err)
	}
	cancelCall()
	select {
	case <-s.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the call did not end the node's handler")
	}
	select {
	case err := <-s.done:
		t.Fatalf("DialTunnel ended (%v) though the relay is alive", err)
	default:
	}
}
