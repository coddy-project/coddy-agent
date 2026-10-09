//go:build http

package httpserver

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// authPolicy is the effective bearer-token policy for one request, snapshotting the live
// config plus any out-of-band tokens so PUT /coddy/config hot reloads take effect immediately.
type authPolicy struct {
	enabled bool
	// tokens are the main-class tokens: they open every route. Nothing but the
	// gate's own class check, the workspace capability key and the settings
	// document read them, and none of those may ever see a shared-model token.
	tokens []string
	// sharedTokens are the LLM-only class (httpserver.shared_models.tokens):
	// they open the shared-model routes (the three calls and the probe's ping) and nothing else. They are never
	// mixed into tokens, so a shared token cannot become key material, count as
	// "a token is configured" for the settings screen or pass a route that is
	// not one of the three.
	sharedTokens []string
	publicDocs   bool
	// allowInsecure is httpserver.allow_insecure as this snapshot read it: the
	// operator's statement that the API is published open, the one thing that
	// lets an anonymous caller reach the shared-model routes.
	allowInsecure bool
	// login is the web sign-in form as it stands for this request. A browser
	// that has passed it reaches the same routes a bearer token reaches.
	login loginPolicy
	// cfg is the configuration the snapshot was taken from. The shared-model
	// handlers answer by it, never by a second read of the live configuration,
	// so a request that straddles a reload is judged by one configuration
	// from its authentication to its last frame.
	cfg *config.Config
}

// anonymousSharedRefused reports whether the shared-model routes refuse a caller
// who presented nothing: no credential of any class is configured (a main
// token, the web sign-in, a shared-model token, one given by flag or
// environment) and the operator has not said allow_insecure. It is the one
// predicate the gate's pass-through and the handlers share, evaluated on the
// request's snapshot and independent of the listen address: a loopback
// listener behind a TLS proxy, or a node that joined a relay by tunnel, is as
// exposed as any other.
func (p authPolicy) anonymousSharedRefused() bool {
	return !p.enabled && !p.allowInsecure
}

// authPolicyKey keys the request-context value the gate stores its snapshot in.
type authPolicyKey struct{}

// withAuthSnapshot returns r carrying the policy snapshot the gate judged it by.
func withAuthSnapshot(r *http.Request, pol *authPolicy) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authPolicyKey{}, pol))
}

// authSnapshot is the snapshot the gate took for this request. A handler
// reached without the gate (a test that drives the mux directly) takes one of
// its own.
func (s *Server) authSnapshot(r *http.Request) *authPolicy {
	if pol, ok := r.Context().Value(authPolicyKey{}).(*authPolicy); ok && pol != nil {
		return pol
	}
	pol := s.authPolicyNow()
	return &pol
}

// SetExtraAuthTokens registers bearer tokens supplied via --auth-token / CODDY_HTTP_TOKEN.
// These enable auth on their own and are kept out of config.yaml (no redaction round-trip).
func (s *Server) SetExtraAuthTokens(tokens []string) {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if v := strings.TrimSpace(t); v != "" {
			out = append(out, v)
		}
	}
	s.extraAuthTokens = out
}

// SetExtraSwarmTokens registers the swarm tokens supplied out of band
// (--swarm-auth-token, --swarm-pairing-token, CODDY_SWARM_TOKEN). This server
// never uses them; it keeps them in view only so that no shared-model token can
// be the same as one (checkTokenClasses).
func (s *Server) SetExtraSwarmTokens(tokens []string) {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if v := strings.TrimSpace(t); v != "" {
			out = append(out, v)
		}
	}
	s.extraSwarmTokens = out
}

// extraCredentials is every credential this process holds that config.yaml does
// not carry, which the loader therefore cannot see: the tokens given by flag and
// by environment, the swarm tokens and a web sign-in account from the
// environment.
func (s *Server) extraCredentials() config.ExtraTokens {
	extra := config.ExtraTokensFromEnv()
	extra.HTTP = append(extra.HTTP, s.extraAuthTokens...)
	extra.Swarm = append(extra.Swarm, s.extraSwarmTokens...)
	extra.Login = extra.Login || (s.envLoginUser != "" && s.envLoginHash != "")
	return extra
}

// checkTokenClasses refuses a configuration in which a shared-model token is the
// same as a main-class or swarm token, the ones in the file and the ones this
// process was given by flag or environment, which only this server can see. It
// runs at start-up and before every configuration is installed (the settings
// screen, a reload), so a refused candidate leaves the running configuration.
// The error names both keys and never a value.
func (s *Server) checkTokenClasses(c *config.Config) error {
	return config.CheckSharedTokenClasses(c, s.extraCredentials())
}

// authPolicyNow builds the current policy from the atomic config and any extra tokens.
func (s *Server) authPolicyNow() authPolicy {
	var pol authPolicy
	if c := s.activeCfg(); c != nil {
		pol.cfg = c
		pol.tokens = append(pol.tokens, c.HTTPServer.EffectiveAuthTokens()...)
		pol.sharedTokens = append(pol.sharedTokens, c.HTTPServer.EffectiveSharedTokens()...)
		pol.publicDocs = c.HTTPServer.PublicDocs
		pol.allowInsecure = c.HTTPServer.AllowInsecure
	}
	if len(s.extraAuthTokens) > 0 {
		pol.tokens = append(pol.tokens, s.extraAuthTokens...)
	}
	// No token is of two classes. The load, the start and every installed
	// configuration refuse such a configuration; one that still reaches the gate
	// is read with the least privilege, so a missed duplicate cannot grant the
	// whole API to a borrower of a model.
	pol.tokens = withoutTokens(pol.tokens, pol.sharedTokens)
	pol.login = s.loginPolicyNow()
	// Auth is active whenever at least one credential is configured: a token
	// (YAML, CLI, or env), a shared-model token or the web sign-in account. Any
	// one closes the same gate, so turning on the form protects the API even with
	// no token set, and a node that holds only shared-model tokens refuses every
	// other route to every caller.
	pol.enabled = len(pol.tokens) > 0 || pol.login.enabled || len(pol.sharedTokens) > 0
	return pol
}

// withoutTokens returns tokens without the entries that also appear in drop.
func withoutTokens(tokens, drop []string) []string {
	if len(drop) == 0 || len(tokens) == 0 {
		return tokens
	}
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		shared := false
		for _, d := range drop {
			if subtleEqual(t, d) {
				shared = true
			}
		}
		if !shared {
			out = append(out, t)
		}
	}
	return out
}

// authGate wraps next with per-request bearer authentication. When no policy is active it is a
// transparent pass-through, so unauthenticated deployments behave exactly as before.
func (s *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One snapshot per request: this gate judges by it and the shared-model
		// handlers read it from the context, so a reload in between never gives
		// one request two policies.
		pol := s.authPolicyNow()
		r = withAuthSnapshot(r, &pol)
		_, pattern := s.mux.Handler(r)
		if pattern == workspaceRawPattern && r.URL.Query().Has("access_token") {
			if s.acceptWorkspaceCapability(r) {
				next.ServeHTTP(w, r)
			} else {
				http.Error(w, "invalid media capability", http.StatusUnauthorized)
			}
			return
		}
		if !pol.enabled || !isProtectedPattern(pattern, pol.publicDocs) {
			next.ServeHTTP(w, r)
			return
		}
		got := bearerToken(r)
		if got == "" && isSSETokenPattern(pattern) {
			// EventSource cannot set an Authorization header cross-origin, so the composer-stream
			// re-attach GET also accepts a ?access_token= query parameter (this route only).
			got = strings.TrimSpace(r.URL.Query().Get("access_token"))
		}
		if acceptBearer(pol.tokens, got) {
			next.ServeHTTP(w, r)
			return
		}
		// A shared-model token opens the shared-model routes and is refused
		// everywhere else exactly like an unknown token, /v1/chat/completions and
		// /v1/responses included: it falls through to the same 401.
		if isSharedLLMPattern(pattern) && acceptBearer(pol.sharedTokens, got) {
			next.ServeHTTP(w, r)
			return
		}
		// A TLS client certificate is not a credential. The handshake admitted the peer by its chain
		// (httpserver.tls.client_ca_file) and that is all TLS does here: the application reads no identity out of it.
		// A signed-in browser carries a cookie instead of a token. Same-origin
		// requests send it on their own, which is why the SPA needs no
		// ?access_token= on its event streams.
		if s.hasCookieSession(r, pol.login) {
			// A cookie travels with any request the browser makes, including one
			// a page on another site caused, so the writes are checked for where
			// they came from. Token clients never reach this branch.
			if !isStateChanging(r) || isSameOriginRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		// The counters are of calls: a ping of the application probe is not one, so a refused ping is not counted, as the relay does not.
		if isSharedLLMPattern(pattern) && pattern != sharedAlivePattern {
			s.countSharedGateRefusal()
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="coddy"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// hasCookieSession reports whether the request carries a live browser
// session: the sign-in form's, or a Telegram admin's Mini App session.
func (s *Server) hasCookieSession(r *http.Request, login loginPolicy) bool {
	if _, ok := s.sessionFromRequest(r, login); ok {
		return true
	}
	_, ok := s.telegramSessionFromRequest(r)
	return ok
}

// subtleEqual compares two strings without leaking which byte differed.
func subtleEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// isProtectedPattern classifies a matched route pattern rather than using a fragile string prefix:
// the SPA shell and static assets fall through to the "/" catch-all and stay public; every
// registered API route (/v1/*, /coddy/*) is protected except the sign-in routes themselves;
// /docs and /openapi.* are protected unless publicDocs is set.
func isProtectedPattern(pattern string, publicDocs bool) bool {
	if pattern == "" || pattern == "/" {
		return false
	}
	// The sign-in routes are the way through the gate, so they cannot be behind
	// it: a browser with no credential yet has to be able to ask whether one is
	// needed and to present one.
	if isAuthRoutePattern(pattern) {
		return false
	}
	if publicDocs && isDocsPattern(pattern) {
		return false
	}
	return true
}

func isDocsPattern(pattern string) bool {
	switch pattern {
	case "GET /docs", "GET /docs/", "GET /openapi.yaml", "GET /openapi.json":
		return true
	default:
		return false
	}
}

// isSSETokenPattern reports whether a route may authenticate via ?access_token= (EventSource).
func isSSETokenPattern(pattern string) bool {
	return pattern == "GET /coddy/sessions/{id}/composer-stream" ||
		pattern == "GET /coddy/events"
}

// bearerToken extracts the token from an "Authorization: Bearer <token>" header.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) >= len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

// acceptBearer reports whether got matches any accepted token using a constant-time compare.
func acceptBearer(tokens []string, got string) bool {
	if got == "" {
		return false
	}
	ok := false
	for _, t := range tokens {
		if subtle.ConstantTimeCompare([]byte(t), []byte(got)) == 1 {
			ok = true
		}
	}
	return ok
}
