//go:build swarm

package swarm

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/netx"
)

// directTransport reaches a node the relay can dial itself.
type directTransport struct {
	target *url.URL
	rt     http.RoundTripper
}

// newDirectTransport builds the transport for a reachable node.
//
// Timeouts here bound the handshake and the idle pool, never the response.
//
// ResponseHeaderTimeout is deliberately left unset. It bounds time-to-first-byte,
// and the first byte of a coddy turn arrives only after the node has loaded a
// model, or after an operator has answered a permission prompt that the node is
// holding the response open for. Setting it would turn "the human is thinking"
// into a 504 from a relay the human never sees, and in a chain the effective
// budget would collapse to the smallest hop's.
// newDirectTransport builds the transport for a reachable node.
//
// pinned are the addresses the relay already checked against its egress policy.
// The dial goes to exactly those rather than resolving the name again, because
// re-resolving would reopen the window the check closed: a name that answers
// with a public address while it is examined and a private one a moment later.
func newDirectTransport(target *url.URL, dial netx.Options, pinned []netip.Addr) (NodeTransport, error) {
	if target == nil {
		return nil, nil
	}
	tr, err := dial.Transport()
	if err != nil {
		return nil, err
	}
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ExpectContinueTimeout = time.Second
	tr.IdleConnTimeout = 90 * time.Second
	tr.MaxIdleConnsPerHost = 8
	tr.ForceAttemptHTTP2 = true
	if len(pinned) > 0 {
		base := tr.DialContext
		if base == nil {
			base = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
		}
		tr.DialContext = netx.PinnedDialer(pinned, base)
		// A pinned dialer already decides where to connect, so an ambient proxy
		// setting must not quietly send the request somewhere else.
		tr.Proxy = nil
	}
	return &directTransport{target: target, rt: tr}, nil
}

func (d *directTransport) RoundTripper() http.RoundTripper { return d.rt }
func (d *directTransport) TargetURL() *url.URL             { return d.target }
func (d *directTransport) Alive() bool                     { return true }

func (d *directTransport) Close() error {
	if tr, ok := d.rt.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	return nil
}
