//go:build swarm

package swarm

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/netx"
	swarmdto "github.com/EvilFreelancer/coddy-agent/internal/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// Server is one relay: a registry, the routes that maintain it, and the mounts
// that proxy to what it holds.
type Server struct {
	cfg *config.Config
	log *slog.Logger
	mux *http.ServeMux

	registry *Registry

	// uuid identifies this relay process. It is what a chain uses to notice it
	// has come back to itself, and what a ring is collapsed by during
	// discovery, so it must be per process rather than per name.
	uuid      string
	startedAt time.Time
	// hostName is the name this relay goes by when the configuration gives it
	// none (relayName).
	hostName string

	mu          sync.RWMutex
	extraTokens []string
	// clients are the scoped relay clients (swarm.clients), read per request.
	clients []config.SwarmClient
	// limits and stats are the slots and windows of the scoped clients and the audit counters of the shared routes (limits.go, audit.go).
	limits *clientLimits
	stats  *relayCounters
}

// New builds a relay server from cfg.
func New(cfg *config.Config, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	uuid, err := randomSecret()
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:       cfg,
		log:       log,
		mux:       http.NewServeMux(),
		registry:  NewRegistry(time.Duration(cfg.Swarm.EffectiveLeaseTTLSeconds()) * time.Second),
		uuid:      uuid[:32],
		startedAt: time.Now(),
		hostName:  swarmdto.HostNodeName(),
		clients:   append([]config.SwarmClient(nil), cfg.Swarm.Clients...),
		limits:    newClientLimits(time.Now),
		stats:     newRelayCounters(time.Now),
	}
	s.routes()
	// An advertised address is somebody else's claim about where to dial, so
	// the relay decides what it is willing to act on. Loopback is allowed only
	// when the relay itself is bound to loopback, which is the development case.
	s.registry.SetEgressPolicy(netx.EgressPolicy{
		AllowLoopback: isLoopbackBind(cfg.Swarm.EffectiveHost()),
		// Deliberately not AllowPrivate: naming hosts relaxes the rules for
		// those hosts, not for every private address a node might claim.
		AllowHosts: cfg.Swarm.AllowPrivateUpstreams,
	})
	nodeDial := netx.Options{
		CAFile:   cfg.Swarm.NodeTLS.CAFile,
		CertFile: cfg.Swarm.NodeTLS.CertFile,
		KeyFile:  cfg.Swarm.NodeTLS.KeyFile,
	}
	// A CA that cannot be read stops the relay here, under the key that names it, instead of failing every registration later.
	if _, err := nodeDial.TLSConfig(""); err != nil {
		return nil, fmt.Errorf("swarm.node_tls: %w", err)
	}
	s.registry.SetNodeDial(nodeDial)
	if err := s.seedUpstreams(); err != nil {
		return nil, err
	}
	return s, nil
}

// SetExtraAuthTokens layers credentials that came from a flag or the
// environment on top of the configured one, so a token need not be written into
// config.yaml to be used.
func (s *Server) SetExtraAuthTokens(tokens []string) {
	s.mu.Lock()
	s.extraTokens = append([]string(nil), tokens...)
	s.mu.Unlock()
}

// UUID reports this relay's process identity.
func (s *Server) UUID() string { return s.uuid }

// Registry exposes the live registry, for the CLI and for tests.
func (s *Server) Registry() *Registry { return s.registry }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /swarm/info", s.handleInfo)
	s.mux.HandleFunc("GET /swarm/nodes", s.handleNodes)
	s.mux.HandleFunc("GET /swarm/stats", s.handleStats)
	s.mux.HandleFunc("POST /swarm/register", s.handleRegister)
	s.mux.HandleFunc("DELETE /swarm/nodes/{node}", s.handleUnregister)
	s.registerSessionRoutes()
	s.registerTunnelRoutes()
	s.registerTopologyRoutes()
	s.registerMountRoutes()
	mountSPARoot(s)
}

// writeSPANotice answers the root with a plain-text explanation instead of the
// SPA. Shared by both build variants of mountSPARoot.
func writeSPANotice(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(msg))
}

// Close releases what outlives the relay's HTTP server: the tunnels its nodes
// opened. A node sees its connection close and dials again, so a relay rebuilt
// on new settings (issue #401) or restarted is joined again within seconds.
func (s *Server) Close() {
	s.registry.CloseTransports()
}

// Handler returns the relay's HTTP handler with CORS and the auth gate applied.
func (s *Server) Handler() http.Handler {
	return s.corsMiddleware(s.authGate(s.mux))
}

// seedUpstreams registers the nodes an operator configured by hand. They are
// leases like any other except that nothing refreshes them, so they are given
// an endless one: the operator asserted they exist.
func (s *Server) seedUpstreams() error {
	for _, up := range s.cfg.Swarm.Upstreams {
		kind := up.Kind
		if kind == "" {
			kind = swarmdto.KindAgent
		}
		uuid, err := randomSecret()
		if err != nil {
			return err
		}
		req := swarmdto.RegisterRequest{
			Name:         up.Name,
			Kind:         kind,
			Transport:    swarmdto.TransportDirect,
			AdvertiseURL: up.URL,
			InstanceUUID: "static-" + uuid[:16],
			Token:        up.Token,
			Version:      "configured",
		}
		if up.Dial.InsecureSkipVerify {
			s.log.Warn("swarm upstream: certificate verification disabled", "node", up.Name)
		}
		if _, err := s.registry.RegisterWithDial(req, netx.Options{
			Proxy:              up.Dial.Proxy,
			CAFile:             up.Dial.CAFile,
			InsecureSkipVerify: up.Dial.InsecureSkipVerify,
			CertFile:           up.Dial.CertFile,
			KeyFile:            up.Dial.KeyFile,
		}); err != nil {
			return fmt.Errorf("swarm.upstreams %q: %w", up.Name, err)
		}
		s.registry.Pin(up.Name)
	}
	return nil
}

// ---- handlers ----

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, swarmdto.Info{
		Swarm:     true,
		Name:      s.relayName(),
		UUID:      s.uuid,
		Version:   version.Get(),
		NodeCount: s.registry.Len(),
		StartedAt: s.startedAt.UTC().Format(time.RFC3339),
		// A relay that just came up has an empty registry because nothing has
		// checked in yet, which is a different situation from a relay nobody
		// ever joined. Saying so lets a client wait instead of concluding the
		// swarm is empty.
		RegistryWarming: s.registry.Len() == 0 && time.Since(s.startedAt) < s.registry.ttl,
	})
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "swarm.node_list",
		"nodes":  s.registry.List(),
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Swarm.RegistrationOpen() {
		writeError(w, http.StatusForbidden, "registration is closed: set swarm.pairing_tokens")
		return
	}
	if !s.cfg.Swarm.AcceptsPairingToken(bearerOf(r)) {
		writeError(w, http.StatusUnauthorized, "pairing token rejected")
		return
	}
	var req swarmdto.RegisterRequest
	if err := decodeJSON(w, r, &req, 64<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.registry.Register(req)
	switch {
	case err == ErrNameTaken:
		// The name is live and the caller did not prove it owns it. A pairing
		// token authorises joining, never impersonating.
		writeError(w, http.StatusConflict, fmt.Sprintf("node name %q is held by a live lease", req.Name))
		return
	case errors.Is(err, errRelayRoute):
		// The relay's own files and settings are not the node's to read.
		s.log.Warn("swarm: the route to a registering node could not be built", "node", req.Name, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "the relay cannot build its route to this node")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("swarm node registered",
		"node", res.NodeID, "kind", req.Kind, "transport", req.Transport,
		"generation", res.Generation, "instance", req.InstanceUUID)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleUnregister(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("node")
	if err := swarmdto.ValidateNodeName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.registry.Delete(name) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown node %q", name))
		return
	}
	s.log.Info("swarm node removed", "node", name)
	w.WriteHeader(http.StatusNoContent)
}

// ---- auth ----

// publicPatterns are reachable without a client credential. Only the discovery
// probe qualifies: a client has to be able to tell a relay from a plain agent
// before it holds anything, and the answer names the swarm without listing what
// is in it.
// The SPA shell and its static assets match the "/" catch-all; they are public
// for the same reason a login page is: a browser holds no credential until the
// page it is loading has asked for one.
func isPublicSwarmPattern(pattern string) bool {
	return pattern == "GET /swarm/info" || pattern == "/"
}

// authGate requires the client credential on everything but discovery.
//
// Registration is exempt from the *client* token because it carries its own
// pairing credential; the two are separate trust domains and conflating them
// would mean every node had to hold the fleet-wide client token.
func (s *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := s.mux.Handler(r)
		// Registration and tunnel establishment carry their own credentials -
		// a pairing token and a lease secret - and belong to a different trust
		// domain than the client token. Requiring both would mean every node
		// had to hold the fleet-wide client credential as well.
		if isPublicSwarmPattern(pattern) || pattern == "POST /swarm/register" || pattern == "POST "+swarmdto.TunnelPath {
			next.ServeHTTP(w, r)
			return
		}
		// A media element's request carries the node's capability, not a client
		// token: the mount hands it to the node, which checks it.
		if s.workspaceMediaCapability(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authRequired() {
			next.ServeHTTP(w, r)
			return
		}
		p := s.principalOf(r)
		switch p.class {
		case principalFull:
			next.ServeHTTP(w, withPrincipal(r, p))
		case principalScoped:
			// A scoped client passes the gate on the node mount only; the mount makes the fine check. Everywhere else it
			// gets the plain 401 an unknown token gets, so it learns nothing about the relay.
			if pattern != swarmdto.MountPath+"{node}/{rest...}" {
				writeUnauthorized(w)
				return
			}
			next.ServeHTTP(w, withPrincipal(r, p))
		default:
			// A token the gate refuses on a shared route is counted, once, under the label unknown: the handler never runs.
			if strings.HasPrefix(pattern, swarmdto.MountPath) && sharedRequest(r) {
				s.stats.add(statClientUnknown, statNoNode, statAuth, 0)
			}
			writeUnauthorized(w)
		}
	})
}

func (s *Server) clientTokens() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	if t := strings.TrimSpace(s.cfg.Swarm.AuthToken); t != "" {
		out = append(out, t)
	}
	for _, t := range s.extraTokens {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func acceptToken(accepted []string, got string) bool {
	if got == "" {
		return false
	}
	for _, t := range accepted {
		if subtle.ConstantTimeCompare([]byte(t), []byte(got)) == 1 {
			return true
		}
	}
	return false
}

// eventStreamRoutes are the node routes a browser opens with EventSource, which
// cannot set a header. The relay accepts a query token on exactly those, the
// same narrow exception the agent makes, and strips it before the hop.
var eventStreamRoutes = []string{"/composer-stream", "/coddy/events"}

// workspaceMediaPath matches a node's workspace raw route behind one or more
// mount hops: the one route a native <video> or <audio> element loads with a
// capability the node signed, because a media element cannot send a header.
var workspaceMediaPath = regexp.MustCompile(`^(` + regexp.QuoteMeta(swarmdto.MountPath) + `[^/]+)+/coddy/sessions/[^/]+/workspace/raw$`)

// workspaceMediaCapability reports whether r is a media request the relay
// carries on the node's own capability rather than on a client token: a GET or
// HEAD of the workspace raw route under a mount, with exactly one non-empty
// access_token (a second one would ride along to the node unchecked), no bearer
// of its own, and a token that is not one of this relay's client tokens (that
// one is never handed to a node). The relay cannot check the node's signature,
// so it neither accepts nor vouches for the request: the node checks the
// capability itself.
func (s *Server) workspaceMediaCapability(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if bearerOf(r) != "" {
		return false
	}
	if !workspaceMediaPath.MatchString(r.URL.Path) {
		return false
	}
	values := r.URL.Query()["access_token"]
	if len(values) != 1 {
		return false
	}
	token := strings.TrimSpace(values[0])
	if token == "" {
		return false
	}
	return !acceptToken(s.allClientTokens(), token)
}

// mediaCapabilityOnly reports whether r got past the gate on a media
// capability alone: the relay asks for a client token and r brought none.
func (s *Server) mediaCapabilityOnly(r *http.Request) bool {
	return s.authRequired() && s.workspaceMediaCapability(r)
}

// writeUnauthorized is the gate's refusal: the same plain 401 whatever was
// asked, so it tells a caller without a client token nothing about the swarm.
func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="coddy-swarm"`)
	writeError(w, http.StatusUnauthorized, "unauthorized")
}

// credentialOf reads the client's token, allowing the query only where a header
// is impossible.
func credentialOf(r *http.Request) string {
	if t := bearerOf(r); t != "" {
		return t
	}
	if r.Method != http.MethodGet {
		return ""
	}
	path := r.URL.Path
	if !strings.HasPrefix(path, swarmdto.MountPath) {
		return ""
	}
	for _, suffix := range eventStreamRoutes {
		if strings.HasSuffix(path, suffix) {
			return strings.TrimSpace(r.URL.Query().Get("access_token"))
		}
	}
	return ""
}

func bearerOf(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// corsMiddleware answers preflight and tags allowed responses. The relay owns
// these headers rather than passing a node's through, because the browser is
// talking to the relay's origin whatever happens further down.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allow, ok := s.cfg.Swarm.CORS.AllowOrigin(origin); ok {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", allow)
			if allow != "*" {
				h.Add("Vary", "Origin")
			}
			h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
			// Last-Event-ID is what resumes an interrupted stream. Without it in
			// the allow list a browser reattach fails preflight, which is a
			// confusing way to lose a conversation. Range and If-None-Match are
			// what the Files window reads a workspace file with (media in pieces,
			// a preview revalidated), and it reads the ETag and the range back:
			// the node's own CORS answer is dropped on the way, so the relay
			// allows and exposes them itself.
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Coddy-Session-ID, Last-Event-ID, Range, If-None-Match")
			h.Set("Access-Control-Expose-Headers", "ETag, Content-Range, Accept-Ranges, Content-Disposition")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]interface{}{
		"error": map[string]interface{}{"message": msg},
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out interface{}, limit int64) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

// GenerateToken mints a credential for a relay that was started without one.
// Leaving a relay open on loopback is not harmless: it holds every node's
// credential, so any local process could drive the whole fleet through it.
func GenerateToken() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
