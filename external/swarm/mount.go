//go:build swarm

package swarm

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
)

// mountedPrefixes are the route families a relay will carry to a node.
//
// This is a prefix allowlist rather than a route allowlist on purpose: a node
// gains routes with every release, and enumerating them would mean the relay
// silently stopped carrying features the day they shipped. What the list does
// enforce is the boundary between planes - a client may drive a node's API, and
// may reach further down a chain, but may not operate the relay's own registry
// through a mount.
var mountedPrefixes = []string{
	"/v1/",
	"/coddy/",
	swarmdto.MountPath, // nested mounts: this is how a chain composes
	"/swarm/info",
	"/swarm/nodes",
	"/swarm/sessions",
	"/swarm/topology",
}

// controlPlaneRoutes must never be reachable through a mount, whatever method
// is used.
//
// The proxy authenticates to the node on the caller's behalf, so anything
// reachable this way is something the relay authorises for them. Registration
// and tunnel establishment are how the swarm's membership is decided; a client
// that could reach them through a mount could enrol a node into a relay it only
// had read access to.
var controlPlaneRoutes = []string{
	"/swarm/register",
	"/swarm/tunnel",
}

// unregisterRoute matches a relay's own "evict this node" route. Reading a
// child relay's node list is ordinary; deleting from it is not, and the
// difference is the method, so a prefix rule alone would leave the hole open.
var unregisterRoute = regexp.MustCompile(`^/swarm/nodes/[^/]+$`)

// hopByHopHeaders never survive a proxy hop.
var hopByHopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Te", "Trailer", "Transfer-Encoding", "Upgrade",
}

// clientHeadersToDrop are headers a client must not be able to inject, because
// downstream they would either be believed or leak the caller's own identity.
var clientHeadersToDrop = []string{
	"Cookie", "Forwarded", "X-Forwarded-For", "X-Forwarded-Host",
	"X-Forwarded-Proto", "X-Real-Ip",
}

// registerMountRoutes wires the per-node proxy.
func (s *Server) registerMountRoutes() {
	s.mux.HandleFunc(swarmdto.MountPath+"{node}/{rest...}", s.handleMount)
}

// handleMount proxies one request to one node.
func (s *Server) handleMount(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("node")
	if err := swarmdto.ValidateNodeName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rest, err := mountRemainder(r, name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A chain composes by writing hops into the path, so a client could write
	// an arbitrarily long one - round a ring, indefinitely. The fan-out has a
	// hop budget in its own header; a hand-written path needs the same bound.
	if hops := strings.Count(rest, swarmdto.MountPath) + 1; hops > swarmMaxHops {
		writeError(w, http.StatusLoopDetected,
			fmt.Sprintf("path walks %d relays, more than the %d this swarm carries", hops, swarmMaxHops))
		return
	}
	if !mountAllows(r.Method, rest) {
		// Saying which plane the route belongs to is more useful than a bare
		// 404, and reveals nothing the caller could not learn by reading the
		// docs for the relay they are already talking to.
		writeError(w, http.StatusNotFound, fmt.Sprintf("route %q is not carried by a node mount", rest))
		return
	}

	node, ok := s.registry.Node(name)
	if !ok {
		writeHopError(w, http.StatusNotFound, name, "no such node in this relay", "")
		return
	}
	if !node.Info.Online || node.Transport == nil || !node.Transport.Alive() {
		writeHopError(w, http.StatusBadGateway, name, "node is registered but not reachable", node.Info.LastSeen)
		return
	}

	target := node.Transport.TargetURL()
	if target == nil {
		writeHopError(w, http.StatusBadGateway, name, "node has no usable transport", node.Info.LastSeen)
		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite:   s.rewriteFor(node, target, rest),
		Transport: node.Transport.RoundTripper(),
		// Flush every write straight through. A turn arrives as a stream of
		// small events, and buffering them would turn a live transcript into a
		// long silence followed by a wall of text.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Once the node's headers are out the response is committed and
			// there is no way to turn a failure into a status code; the stream
			// simply ends and the client treats the outcome as unknown.
			s.log.Warn("swarm mount failed", "node", name, "path", rest, "error", err)
			writeHopError(w, http.StatusBadGateway, name, err.Error(), node.Info.LastSeen)
		},
	}
	proxy.ServeHTTP(w, r)
}

// rewriteFor builds the request the node will see.
func (s *Server) rewriteFor(node Node, target *url.URL, rest string) func(*httputil.ProxyRequest) {
	return func(pr *httputil.ProxyRequest) {
		out := pr.Out
		out.URL.Scheme = target.Scheme
		out.URL.Host = target.Host
		out.Host = target.Host

		// The node may itself live under a base path, which has to survive.
		base := strings.TrimRight(target.Path, "/")
		joined := base + rest
		out.URL.Path = joined
		out.URL.RawPath = joined

		query := pr.In.URL.Query()
		// The client authenticated to the relay, possibly with a token in the
		// query because an EventSource cannot set headers. Forwarding that
		// token would write the relay's own credential into the node's access
		// log, for no benefit: the node authenticates the relay, not the client.
		query.Del("access_token")
		out.URL.RawQuery = query.Encode()

		for _, h := range hopByHopHeaders {
			out.Header.Del(h)
		}
		for _, h := range clientHeadersToDrop {
			out.Header.Del(h)
		}
		// A client must not be able to forge the chain's own bookkeeping.
		for h := range out.Header {
			if strings.HasPrefix(strings.ToLower(h), "x-coddy-swarm-") {
				out.Header.Del(h)
			}
		}

		// The caller's credential is replaced, never forwarded: it authorises
		// them against this relay and means nothing to the node.
		out.Header.Del("Authorization")
		if node.Token != "" {
			out.Header.Set("Authorization", "Bearer "+node.Token)
		}
	}
}

// mountRemainder returns the part of the path that belongs to the node, taken
// from the escaped form so an encoded character survives the hop intact.
func mountRemainder(r *http.Request, node string) (string, error) {
	prefix := swarmdto.MountPath + node
	escaped := r.URL.EscapedPath()
	if !strings.HasPrefix(escaped, prefix) {
		return "", fmt.Errorf("request path does not match the node mount")
	}
	rest := strings.TrimPrefix(escaped, prefix)
	if rest == "" {
		rest = "/"
	}
	if !strings.HasPrefix(rest, "/") {
		return "", fmt.Errorf("request path does not match the node mount")
	}
	// An encoded separator or a relative segment would mean the path the relay
	// checked and the path the node resolves are not the same string, which is
	// the shape of every proxy path-confusion bug.
	lowered := strings.ToLower(rest)
	if strings.Contains(lowered, "%2f") || strings.Contains(lowered, "%5c") {
		return "", fmt.Errorf("encoded path separators are not accepted")
	}
	for _, seg := range strings.Split(rest, "/") {
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("relative path segments are not accepted")
		}
	}
	return rest, nil
}

// mountAllows reports whether a request may be carried to a node.
func mountAllows(method, rest string) bool {
	for _, denied := range controlPlaneRoutes {
		if rest == denied || strings.HasPrefix(rest, denied+"/") {
			return false
		}
	}
	if method == http.MethodDelete && unregisterRoute.MatchString(rest) {
		return false
	}
	for _, allowed := range mountedPrefixes {
		if rest == strings.TrimSuffix(allowed, "/") || strings.HasPrefix(rest, allowed) {
			return true
		}
	}
	return false
}

// writeHopError reports a failure in terms of the hop that produced it.
//
// A client three relays away otherwise sees an unattributed gateway error and
// has to bisect the topology by hand to find out which node is actually down.
func writeHopError(w http.ResponseWriter, status int, node, reason, lastSeen string) {
	payload := map[string]interface{}{
		"message": fmt.Sprintf("swarm node %q: %s", node, reason),
		"node":    node,
		"reason":  reason,
	}
	if lastSeen != "" {
		payload["last_seen"] = lastSeen
	}
	writeJSON(w, status, map[string]interface{}{"error": payload})
}
