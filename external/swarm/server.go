//go:build swarm

package swarm

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
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

	mu          sync.RWMutex
	extraTokens []string
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
	}
	s.routes()
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
	s.mux.HandleFunc("POST /swarm/register", s.handleRegister)
	s.mux.HandleFunc("DELETE /swarm/nodes/{node}", s.handleUnregister)
	s.registerSessionRoutes()
	s.registerTunnelRoutes()
	s.registerTopologyRoutes()
	s.registerMountRoutes()
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
		if _, err := s.registry.Register(req); err != nil {
			return fmt.Errorf("swarm.upstreams %q: %w", up.Name, err)
		}
		s.registry.Pin(up.Name)
	}
	return nil
}

// ---- handlers ----

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(s.cfg.Swarm.Name)
	if name == "" {
		name = "swarm"
	}
	writeJSON(w, http.StatusOK, swarmdto.Info{
		Swarm:     true,
		Name:      name,
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
func isPublicSwarmPattern(pattern string) bool {
	return pattern == "GET /swarm/info"
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
		tokens := s.clientTokens()
		if len(tokens) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if !acceptToken(tokens, bearerOf(r)) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="coddy-swarm"`)
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
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
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			// Last-Event-ID is what resumes an interrupted stream. Without it in
			// the allow list a browser reattach fails preflight, which is a
			// confusing way to lose a conversation.
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Coddy-Session-ID, Last-Event-ID")
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
