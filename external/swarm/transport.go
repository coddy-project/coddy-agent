//go:build swarm

package swarm

import (
	"net"
	"net/http"
	"net/url"
	"time"
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
func newDirectTransport(target *url.URL) NodeTransport {
	if target == nil {
		return nil
	}
	return &directTransport{
		target: target,
		rt: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConnsPerHost:   8,
			ForceAttemptHTTP2:     true,
		},
	}
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
