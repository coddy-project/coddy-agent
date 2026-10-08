//go:build swarm

package swarm

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/httpx"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
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
	// A node's own API description. Read-only, and the one part of its HTTP
	// surface a mount used to answer 404 for while every other route worked,
	// which made a mount look broken to anything that reads a spec before
	// calling - curl, a generated client, a conformance check. The SPA's own
	// footer link is relative to wherever the SPA is served from, so it is not
	// what these are here for.
	"/openapi",
	"/docs",
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

// sharedCompletionsRoute is the node route of one shared-model call (llm.CoddyCompletionsPath;
// a test holds the two equal). It is the only route whose client the relay bounds.
const sharedCompletionsRoute = "/coddy/llm/completions"

// h2ResidualLog is the line logged once per connection of an HTTP/2 client the
// relay cannot bound; docs/features/shared-models.md names it.
const h2ResidualLog = "shared call over HTTP/2: a vanished client is not detected before the call ends"

// isSharedCompletions reports whether a request is the one shared-model call:
// POST on the completions route of a node, behind any number of further relay
// hops (a chain writes its hops into the path, so the first relay of a chain
// sees /swarm/nodes/<next>/coddy/llm/completions and bounds its client as well).
// rest is the escaped path after the node's name; the query is not part of it.
// It is compared decoded, because the node routes by the decoded path: a
// percent-encoded spelling of the route reaches the same handler and must not
// slip past the bound. mountRemainder has already refused encoded separators and
// relative segments, so decoding cannot change the segment structure.
func isSharedCompletions(method, rest string) bool {
	if method != http.MethodPost {
		return false
	}
	_, route, ok := splitHops("", rest)
	return ok && route == sharedCompletionsRoute
}

// probeSharedCall bounds the peer of one shared-model call at its source: a
// client that vanishes without a FIN or a RST is invisible to the node (the relay
// acknowledges and answers everything the node sends), so the relay sets the same
// per-call user timeout on its own client connection that the node sets on its
// peer, U = B - H from httpx. The returned restore is deferred by the caller and
// puts the system default back on every exit, so a kept-alive connection starts
// its next request unprobed.
//
// It runs before the request is forwarded, not after the body: the option counts
// only bytes the relay itself sent and has not had acknowledged, and the relay
// sends nothing to its client until the node's first frame, which the node writes
// only after it has read the body. The upload is bounded by the node's own body
// deadline, as before.
//
// When the dead client's connection is aborted the proxy's request context ends,
// the upstream request is cancelled (a reset stream on a tunnel, a closed
// connection on a direct dial) and the node frees the slot in the same round trip.
//
// Only an HTTP/1.x connection can be bounded: an HTTP/2 stream shares its
// connection with others, so the call is served unprobed and logged once per
// connection. Where the platform has no option (anything but Linux) or the server
// was built without httpx.ConnContext the call is served unprobed too, quietly.
func (s *Server) probeSharedCall(r *http.Request, node string) (restore func()) {
	bound, heartbeat := httpx.Liveness()
	restore, err := httpx.ProbeCall(r, bound, heartbeat)
	switch {
	case err == nil:
	case errors.Is(err, httpx.ErrSharedConnection):
		if httpx.OncePerConn(r, "probe-h2") {
			s.log.Info(h2ResidualLog, "node", node, "client", r.RemoteAddr)
		}
	case errors.Is(err, platform.ErrUserTimeoutUnsupported):
		if httpx.OncePerConn(r, "probe-unsupported") {
			s.log.Debug("shared call not bounded on the client leg", "node", node, "reason", err.Error())
		}
	default:
		if httpx.OncePerConn(r, "probe-failed") {
			s.log.Warn("shared call not bounded on the client leg", "node", node, "error", err)
		}
	}
	return restore
}

// registerMountRoutes wires the per-node proxy.
func (s *Server) registerMountRoutes() {
	s.mux.HandleFunc(swarmdto.MountPath+"{node}/{rest...}", s.handleMount)
}

// handleMount proxies one request to one node.
func (s *Server) handleMount(w http.ResponseWriter, r *http.Request) {
	// A request let in on a media capability alone has shown the relay no
	// credential, so it learns nothing from the relay: every refusal of the
	// relay's own is the gate's plain 401 - no node named, no path rule, no
	// node's state. Only the node's own answer reaches such a caller.
	anonymous := s.mediaCapabilityOnly(r)
	refuse := func(write func()) {
		if anonymous {
			writeUnauthorized(w)
			return
		}
		write()
	}

	name := r.PathValue("node")
	if err := swarmdto.ValidateNodeName(name); err != nil {
		refuse(func() { writeError(w, http.StatusBadRequest, err.Error()) })
		return
	}

	rest, err := mountRemainder(r, name)
	if err != nil {
		refuse(func() { writeError(w, http.StatusBadRequest, err.Error()) })
		return
	}
	// A chain composes by writing hops into the path, so a client could write
	// an arbitrarily long one - round a ring, indefinitely. The fan-out has a
	// hop budget in its own header; a hand-written path needs the same bound.
	if hops := strings.Count(rest, swarmdto.MountPath) + 1; hops > swarmMaxHops {
		refuse(func() {
			writeError(w, http.StatusLoopDetected,
				fmt.Sprintf("path walks %d relays, more than the %d this swarm carries", hops, swarmMaxHops))
		})
		return
	}
	if !mountAllows(r.Method, rest) {
		// Saying which plane the route belongs to is more useful than a bare
		// 404, and reveals nothing the caller could not learn by reading the
		// docs for the relay they are already talking to.
		refuse(func() {
			writeError(w, http.StatusNotFound, fmt.Sprintf("route %q is not carried by a node mount", rest))
		})
		return
	}

	// A scoped client is judged before the registry is looked at, so that what it hears about a node outside its list is what it
	// hears about a node that does not exist: the allowlist first (exact hop paths, every hop of the chain), then the closed
	// table of shared-model routes.
	if p := principalFrom(r.Context()); p.class == principalScoped {
		hops, route, split := splitHops(name, rest)
		if !split || !clientAdmitsHops(*p.client, hops) {
			writeHopError(w, http.StatusNotFound, name, "no such node in this relay", "")
			return
		}
		if !sharedRoute(r.Method, route) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("route %q is not carried by a node mount", rest))
			return
		}
	}

	node, ok := s.registry.Node(name)
	if !ok {
		refuse(func() { writeHopError(w, http.StatusNotFound, name, "no such node in this relay", "") })
		return
	}
	if refuseRelaySettingsWrite(w, r, node, rest) {
		return
	}
	if !node.Info.Online || node.Transport == nil || !node.Transport.Alive() {
		refuse(func() {
			writeHopError(w, http.StatusBadGateway, name, "node is registered but not reachable", node.Info.LastSeen)
		})
		return
	}

	target := node.Transport.TargetURL()
	if target == nil {
		refuse(func() {
			writeHopError(w, http.StatusBadGateway, name, "node has no usable transport", node.Info.LastSeen)
		})
		return
	}

	if isSharedCompletions(r.Method, rest) {
		defer s.probeSharedCall(r, name)()
	}

	// The transport carrying this request reads the body from a goroutine of
	// its own, and net/http closes an inbound body the moment the handler
	// writes response headers - which, for a proxied stream, is while the node
	// is still answering. Having copied the declared length, the transport
	// reads once more to see whether the body was longer than it claimed; if
	// that read lands after the close it fails, the transport drops the
	// connection to the node, and an answer that had already started arrives
	// truncated. Nothing can follow the length the client declared, so the
	// forwarded body answers that read here rather than reaching for a body
	// that is no longer there.
	if r.Body != nil && r.ContentLength > 0 {
		r.Body = &declaredBody{rc: r.Body, left: r.ContentLength}
	}

	proxy := &httputil.ReverseProxy{
		Rewrite:   s.rewriteFor(node, target, rest),
		Transport: node.Transport.RoundTripper(),
		// Flush every write straight through. A turn arrives as a stream of
		// small events, and buffering them would turn a live transcript into a
		// long silence followed by a wall of text.
		FlushInterval: -1,
		// The browser is talking to the relay's origin, so the relay's CORS answer
		// (corsMiddleware) is the only one that means anything. A node that browsers
		// also reach directly has httpserver.cors on and answers with headers of its
		// own; passed through, they land next to the relay's, and a browser refuses a
		// response that names the allowed origin twice - "*, *" included.
		ModifyResponse: func(res *http.Response) error {
			for name := range res.Header {
				if strings.HasPrefix(strings.ToLower(name), "access-control-") {
					res.Header.Del(name)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Once the node's headers are out the response is committed and
			// there is no way to turn a failure into a status code; the stream
			// simply ends and the client treats the outcome as unknown.
			s.log.Warn("swarm mount failed", "node", name, "path", rest, "error", err)
			refuse(func() { writeHopError(w, http.StatusBadGateway, name, err.Error(), node.Info.LastSeen) })
		},
	}
	proxy.ServeHTTP(w, r)
}

// declaredBody is a request body bounded by the length its sender declared.
//
// It is deliberately not a plain io.LimitReader: the point is the Close, which
// belongs to the inbound server and may happen while the transport is still
// reading. Past the declared length the wrapper stops asking.
type declaredBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *declaredBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *declaredBody) Close() error { return b.rc.Close() }

// rewriteFor builds the request the node will see.
func (s *Server) rewriteFor(node Node, target *url.URL, rest string) func(*httputil.ProxyRequest) {
	return func(pr *httputil.ProxyRequest) {
		// Decided on the request alone, whether this relay asks a client token
		// or not: a relay without one, mounted under a relay with one, must not
		// vouch for a request the outer relay let in on a capability, or the
		// outer gate could be walked around through it.
		capability := s.workspaceMediaCapability(pr.In)
		out := pr.Out
		out.URL.Scheme = target.Scheme
		out.URL.Host = target.Host
		out.Host = target.Host

		// The node may itself live under a base path, which has to survive.
		//
		// Path holds the decoded form and RawPath the escaped one. Putting the
		// escaped string in both would send the node a double-encoded path: a
		// space arrives as %2520 rather than %20.
		base := strings.TrimRight(target.Path, "/")
		escaped := base + rest
		decoded, err := url.PathUnescape(escaped)
		if err != nil {
			decoded = escaped
		}
		out.URL.Path = decoded
		out.URL.RawPath = escaped

		query := pr.In.URL.Query()
		// The client authenticated to the relay, possibly with a token in the
		// query because an EventSource cannot set headers. Forwarding that
		// token would write the relay's own credential into the node's access
		// log, for no benefit: the node authenticates the relay, not the client.
		// A media capability is the exception, since it is the node's own
		// credential and the node has to see it.
		if !capability {
			query.Del("access_token")
		}
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
		// them against this relay and means nothing to the node. For a media
		// capability the relay vouches for nothing, the node checks the
		// capability itself.
		out.Header.Del("Authorization")
		if !capability && node.Token != "" {
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
	// Judging the escaped form alone is not enough: %2e%2e survives that check
	// and becomes ".." the moment anything decodes it, which is how a request
	// aimed at a node's API climbs back out into the relay's own routes. Every
	// segment is therefore decoded before it is judged.
	for _, seg := range strings.Split(rest, "/") {
		decoded, derr := url.PathUnescape(seg)
		if derr != nil {
			return "", fmt.Errorf("path segment %q is not decodable", seg)
		}
		if decoded == "." || decoded == ".." {
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
