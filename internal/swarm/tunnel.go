package swarm

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// TunnelPath is the route a node dials to hand a relay a connection.
const TunnelPath = "/swarm/tunnel"

// TunnelMaxConcurrentStreams bounds how many requests share one tunnel.
const TunnelMaxConcurrentStreams = 250

// TunnelIdleTimeout is how long a node keeps serving a connection on which
// nothing at all arrives. The relay pings every 30s, so silence this long means
// the relay is gone rather than merely idle.
const TunnelIdleTimeout = 150 * time.Second

// handshakeTimeout bounds the upgrade exchange, not the connection it produces.
const handshakeTimeout = 30 * time.Second

// SpliceBuffered re-attaches bytes a reader already pulled off a connection.
//
// Both sides of the upgrade read their peer's HTTP message through a buffered
// reader, which happily takes in whatever came next - and what comes next here
// is the start of the HTTP/2 preface.
var SpliceBuffered = netx.SpliceBuffered

// TunnelOptions describe one dial-out from a node to a relay.
type TunnelOptions struct {
	// RelayURL is the relay to dial.
	RelayURL string
	// Node is the name this node registered under.
	Node string
	// LeaseSecret proves this node owns that name.
	LeaseSecret string
	// Handler is the node's own HTTP surface, served back over the connection.
	Handler http.Handler
	// Dial carries proxy and TLS settings.
	Dial netx.Options
}

// DialTunnel opens a connection to the relay and serves Handler over it until
// the connection ends or ctx is cancelled.
//
// The roles invert: the node dialled, but from here on the relay sends requests
// and this side answers them. Nothing above this function knows the difference,
// because what travels the connection is ordinary HTTP.
func DialTunnel(ctx context.Context, opts TunnelOptions) error {
	if opts.Handler == nil {
		return fmt.Errorf("swarm tunnel: a handler is required")
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(opts.RelayURL), "/"))
	if err != nil || u.Host == "" {
		return fmt.Errorf("swarm tunnel: invalid relay url %q", opts.RelayURL)
	}

	conn, err := dialRelay(ctx, u, opts)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, u.String()+TunnelPath, nil)
	if err != nil {
		_ = conn.Close()
		return err
	}
	req.Header.Set("Authorization", "Bearer "+opts.LeaseSecret)
	req.Header.Set("X-Coddy-Swarm-Node", opts.Node)
	// The upgrade is an HTTP/1.1 mechanism; HTTP/2 begins only once it has
	// succeeded, by prior knowledge rather than negotiation.
	req.Header.Set("Connection", "close")

	// A peer that accepts the connection and then says nothing would otherwise
	// hold this goroutine - and a shutdown waiting on it - forever. The
	// deadline covers the handshake only; it is cleared before the connection
	// starts carrying turns, which legitimately go quiet for minutes.
	if derr := conn.SetDeadline(time.Now().Add(handshakeTimeout)); derr != nil {
		_ = conn.Close()
		return fmt.Errorf("swarm tunnel: set handshake deadline: %w", derr)
	}
	// Cancellation has to reach a blocking read, and only closing the
	// connection does that.
	handshakeDone := make(chan struct{})
	defer close(handshakeDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-handshakeDone:
		}
	}()

	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return fmt.Errorf("swarm tunnel: send upgrade: %w", err)
	}

	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("swarm tunnel: read upgrade response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		_ = res.Body.Close()
		_ = conn.Close()
		return fmt.Errorf("swarm tunnel: relay refused the connection: %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	// The acceptance body has to be consumed before the stream changes hands.
	// Anything left of it sits in the buffered reader, and splicing it in front
	// of the connection would feed the relay's JSON to an HTTP/2 parser as if it
	// were a client preface.
	if _, err := io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16)); err != nil {
		_ = res.Body.Close()
		_ = conn.Close()
		return fmt.Errorf("swarm tunnel: drain upgrade response: %w", err)
	}
	_ = res.Body.Close()

	// The handshake is over; from here the connection carries turns that are
	// allowed to be quiet for a long time.
	if derr := conn.SetDeadline(time.Time{}); derr != nil {
		_ = conn.Close()
		return fmt.Errorf("swarm tunnel: clear handshake deadline: %w", derr)
	}

	// The relay pings this connection to prove it is alive, but HTTP/2's own
	// idle timeout deliberately does not count a ping as activity - and an
	// active stream suppresses it entirely. So liveness is watched here, on the
	// bytes actually arriving: a connection nobody is talking on is a relay
	// that has gone away, and the node should redial rather than serve nobody.
	watched := newActivityConn(SpliceBuffered(conn, br))
	stopWatch := watched.watch(TunnelIdleTimeout)
	defer stopWatch()

	served := watched
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&http2.Server{
			// One connection carries every request for this node, so the stream
			// bound is what keeps a burst from starving a live turn.
			MaxConcurrentStreams: TunnelMaxConcurrentStreams,
			// Liveness is watched on the connection itself (see above), not
			// here: HTTP/2's idle timeout ignores pings and is suppressed by an
			// open stream, so it would never fire on a tunnel that is quietly
			// dead while holding a long turn.
			IdleTimeout: 0,
		}).ServeConn(served, &http2.ServeConnOpts{Handler: opts.Handler})
	}()

	select {
	case <-ctx.Done():
		_ = conn.Close()
		<-done
		return ctx.Err()
	case <-done:
		return nil
	}
}

// dialRelay opens the raw stream, through a proxy and under TLS when asked.
func dialRelay(ctx context.Context, u *url.URL, opts TunnelOptions) (net.Conn, error) {
	// Which proxy variable applies depends on the scheme being spoken, so the
	// dialler is told rather than left to guess.
	dialOpts := opts.Dial
	dialOpts.Scheme = u.Scheme
	dial, err := dialOpts.DialFunc()
	if err != nil {
		return nil, err
	}
	addr := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			addr = net.JoinHostPort(u.Hostname(), "443")
		} else {
			addr = net.JoinHostPort(u.Hostname(), "80")
		}
	}
	conn, err := dial(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("swarm tunnel: dial %s: %w", addr, err)
	}
	if u.Scheme != "https" {
		return conn, nil
	}
	tlsCfg, err := opts.Dial.TLSConfig(u.Hostname())
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	// http/1.1 is advertised on purpose: the upgrade below is an HTTP/1.1
	// mechanism, and the stream is only repurposed afterwards.
	tlsCfg.NextProtos = []string{"http/1.1"}
	tlsConn := tls.Client(conn, tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("swarm tunnel: tls handshake: %w", err)
	}
	return tlsConn, nil
}

// TunnelResponse is what the relay writes before the roles invert.
type TunnelResponse struct {
	OK   bool   `json:"ok"`
	Node string `json:"node"`
}

// EncodeTunnelAccept renders the acceptance line the relay sends by hand,
// because a hijacked connection has no ResponseWriter left to use.
func EncodeTunnelAccept(node string) []byte {
	body, _ := json.Marshal(TunnelResponse{OK: true, Node: node})
	return []byte(fmt.Sprintf(
		"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		len(body), body,
	))
}

// activityConn records when bytes last moved in either direction, so a watchdog
// can tell a quiet connection from a dead one.
//
// Counting only what arrives is not enough, and gets it exactly backwards for
// the case that matters most: while this node is streaming a long turn the
// relay has nothing to read-idle about, so it sends no pings, and a watchdog
// watching only inbound bytes would cut a healthy stream off mid-answer. A
// connection this node is actively writing to is alive by definition.
type activityConn struct {
	net.Conn
	mu   sync.Mutex
	last time.Time
}

func newActivityConn(c net.Conn) *activityConn {
	return &activityConn{Conn: c, last: time.Now()}
}

func (c *activityConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.touch()
	}
	return n, err
}

func (c *activityConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.touch()
	}
	return n, err
}

func (c *activityConn) touch() {
	c.mu.Lock()
	c.last = time.Now()
	c.mu.Unlock()
}

func (c *activityConn) idleFor() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.last)
}

// watch closes the connection once nothing has arrived for idle. It returns a
// function that stops watching.
func (c *activityConn) watch(idle time.Duration) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(idle / 3)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if c.idleFor() > idle {
					_ = c.Close()
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }
}
