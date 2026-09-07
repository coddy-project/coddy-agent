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

	"golang.org/x/net/http2"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// TunnelPath is the route a node dials to hand a relay a connection.
const TunnelPath = "/swarm/tunnel"

// TunnelMaxConcurrentStreams bounds how many requests share one tunnel.
const TunnelMaxConcurrentStreams = 250

// bufferedConn re-attaches bytes that were already read off a connection.
//
// Both sides of the upgrade read their peer's HTTP message through a buffered
// reader, which happily pulls in whatever came next - and what comes next here
// is the start of the HTTP/2 preface. Dropping those bytes leaves a connection
// that looks healthy and then fails to parse its first frame, so they are
// spliced back in front of the socket.
type bufferedConn struct {
	net.Conn
	reader io.Reader
}

// SpliceBuffered returns a connection that yields the already-buffered bytes
// before anything further off the socket.
func SpliceBuffered(conn net.Conn, buffered *bufio.Reader) net.Conn {
	if buffered == nil || buffered.Buffered() == 0 {
		return conn
	}
	return &bufferedConn{Conn: conn, reader: io.MultiReader(io.LimitReader(buffered, int64(buffered.Buffered())), conn)}
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

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

	served := SpliceBuffered(conn, br)
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&http2.Server{
			// One connection carries every request for this node, so the stream
			// bound is what keeps a burst from starving a live turn.
			MaxConcurrentStreams: TunnelMaxConcurrentStreams,
			IdleTimeout:          0, // the relay decides when the connection is done
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
	dial, err := opts.Dial.DialFunc()
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
