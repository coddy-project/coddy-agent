//go:build swarm

package swarm

import (
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/net/http2"

	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// Bounds on one tunnel. A single connection is one flow-control domain, so a
// bulk response and a live stream share it and the limits decide how well.
const (
	tunnelReadIdleTimeout = 30 * time.Second
	tunnelPingTimeout     = 15 * time.Second
)

// tunnelTransport reaches a node over the connection that node opened.
type tunnelTransport struct {
	cc     *http2.ClientConn
	conn   net.Conn
	target *url.URL

	mu     sync.Mutex
	closed bool
}

func (t *tunnelTransport) RoundTripper() http.RoundTripper { return t.cc }
func (t *tunnelTransport) TargetURL() *url.URL             { return t.target }

// Alive asks the connection itself rather than trusting a timestamp. A node
// whose network died without a FIN looks idle, not gone, and a relay that keeps
// advertising it sends clients into a request that will never be answered.
func (t *tunnelTransport) Alive() bool {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return false
	}
	state := t.cc.State()
	return !state.Closed && !state.Closing
}

func (t *tunnelTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.mu.Unlock()
	_ = t.cc.Close()
	return t.conn.Close()
}

func (s *Server) registerTunnelRoutes() {
	s.mux.HandleFunc("POST "+swarmdto.TunnelPath, s.handleTunnel)
}

// handleTunnel takes over a connection a node dialled and turns it around.
//
// From the node's point of view it made an outbound request, which is the only
// thing a network that accepts no inbound connections will allow. From this
// point on the relay sends requests down that same connection and the node
// answers them, so everything above this function treats a node in a closed
// contour exactly like a reachable one.
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	node := r.Header.Get("X-Coddy-Swarm-Node")
	if err := swarmdto.ValidateNodeName(node); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	secret := bearerOf(r)
	if secret == "" {
		writeError(w, http.StatusUnauthorized, "a lease secret is required to open a tunnel")
		return
	}

	// Ownership is proved before anything is written. Hijacking first and
	// checking afterwards hands an accept - and a taken-over connection - to a
	// caller who has proved nothing, and only then hangs up on them.
	if err := s.registry.VerifyLease(node, secret); err != nil {
		s.log.Warn("swarm tunnel refused", "node", node, "error", err)
		writeError(w, http.StatusUnauthorized, "lease secret does not match this node")
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		// A layer-7 proxy or an HTTP/2 terminator in front of the relay leaves
		// no raw connection to take over. Saying so is more useful than a
		// generic failure, because the fix is a deployment change.
		writeError(w, http.StatusNotImplemented,
			"this relay cannot take over the connection; a tunnel needs an end-to-end HTTP/1.1 path, so an intermediary that re-frames requests has to be bypassed")
		return
	}

	conn, brw, err := hj.Hijack()
	if err != nil {
		s.log.Warn("swarm tunnel hijack failed", "node", node, "error", err)
		return
	}

	if _, err := brw.Write(swarmdto.EncodeTunnelAccept(node)); err != nil {
		_ = conn.Close()
		return
	}
	if err := brw.Flush(); err != nil {
		_ = conn.Close()
		return
	}

	// The node may already have started its HTTP/2 preface, and those bytes sit
	// in the hijacked reader. Losing them would leave a connection that looks
	// established and then fails on its first frame.
	served := swarmdto.SpliceBuffered(conn, brw.Reader)

	tr := &http2.Transport{
		AllowHTTP: true,
		// A dead peer that never sends a FIN is indistinguishable from an idle
		// one until something writes, so the connection is probed.
		ReadIdleTimeout:            tunnelReadIdleTimeout,
		PingTimeout:                tunnelPingTimeout,
		StrictMaxConcurrentStreams: true,
	}
	cc, err := tr.NewClientConn(served)
	if err != nil {
		s.log.Warn("swarm tunnel handshake failed", "node", node, "error", err)
		_ = conn.Close()
		return
	}

	target, _ := url.Parse("http://" + node + ".swarm.invalid")
	transport := &tunnelTransport{cc: cc, conn: conn, target: target}

	if err := s.registry.AttachTransport(node, secret, transport); err != nil {
		s.log.Warn("swarm tunnel refused", "node", node, "error", err)
		_ = transport.Close()
		return
	}
	s.log.Info("swarm tunnel established", "node", node)

	// Nothing else to do here: the connection now belongs to the registry, and
	// it lives until the node goes away or a newer connection replaces it.
	go s.watchTunnel(node, transport)
}

// watchTunnel drops the lease's transport when its connection dies, so a node
// stops being advertised the moment it can no longer answer.
func (s *Server) watchTunnel(node string, transport *tunnelTransport) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if transport.Alive() {
			continue
		}
		s.registry.DetachTransport(node, transport)
		s.log.Info("swarm tunnel closed", "node", node)
		return
	}
}
