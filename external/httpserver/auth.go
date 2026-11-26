//go:build http

package httpserver

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
)

// authPolicy is the effective bearer-token policy for one request, snapshotting the live
// config plus any out-of-band tokens so PUT /coddy/config hot reloads take effect immediately.
type authPolicy struct {
	enabled    bool
	tokens     []string
	publicDocs bool
	// login is the web sign-in form as it stands for this request. A browser
	// that has passed it reaches the same routes a bearer token reaches.
	login loginPolicy
}

// SetExtraAuthTokens registers bearer tokens supplied via --auth-token / CODDY_HTTP_TOKEN.
// These enable auth on their own and are kept out of config.yaml (no redaction round-trip).
// SetNoAuth makes this server ask for no credential at all, whatever the
// configuration carries. It is set once, before the server serves, by a caller
// that knows nobody else can reach it: `coddy-desktop` on loopback.
func (s *Server) SetNoAuth(on bool) { s.noAuth = on }

// SetNoAuthHeader enables the per-request no-auth choice only on a private
// IPC server whose transport has already authenticated the local caller.
func (s *Server) SetNoAuthHeader(name string) { s.noAuthHeader = name }

func (s *Server) authPolicyForRequest(r *http.Request) authPolicy {
	if s.trustedNoAuth(r) {
		return authPolicy{}
	}
	return s.authPolicyNow()
}

func (s *Server) trustedNoAuth(r *http.Request) bool {
	return s.noAuthHeader != "" && r.Header.Get(s.noAuthHeader) == "1"
}

// SetDesktopWindow gives the server the window its tab shell drives. Set once,
// before the server serves, by coddy-desktop and by nothing else.
func (s *Server) SetDesktopWindow(w any) { s.desktopWindow = w }

func (s *Server) SetExtraAuthTokens(tokens []string) {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if v := strings.TrimSpace(t); v != "" {
			out = append(out, v)
		}
	}
	s.extraAuthTokens = out
}

// authPolicyNow builds the current policy from the atomic config and any extra tokens.
func (s *Server) authPolicyNow() authPolicy {
	var pol authPolicy
	// A server that asks for nothing asks for nothing: not the configuration's
	// token, not its sign-in form. The desktop window is the one caller, and
	// what it serves is loopback for the person who started it.
	if s.noAuth {
		return pol
	}
	if c := s.activeCfg(); c != nil {
		pol.tokens = append(pol.tokens, c.HTTPServer.EffectiveAuthTokens()...)
		pol.publicDocs = c.HTTPServer.PublicDocs
	}
	if len(s.extraAuthTokens) > 0 {
		pol.tokens = append(pol.tokens, s.extraAuthTokens...)
	}
	pol.login = s.loginPolicyNow()
	// Auth is active whenever at least one credential is configured: a token
	// (YAML, CLI, or env) or the web sign-in account. Either one closes the same
	// gate, so turning on the form protects the API even with no token set.
	pol.enabled = len(pol.tokens) > 0 || pol.login.enabled
	return pol
}

// authGate wraps next with per-request bearer authentication. When no policy is active it is a
// transparent pass-through, so unauthenticated deployments behave exactly as before.
func (s *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := s.mux.Handler(r)
		if isOpaqueOrigin(r) && pattern != "" && pattern != "/" && pattern != canvasFilePattern {
			http.Error(w, "requests from an opaque origin are refused", http.StatusForbidden)
			return
		}
		s.external.mu.Lock()
		pol := s.authPolicyForRequest(r)
		if pattern == workspaceRawPattern {
			token, present, valid := workspaceRawAccessToken(r.URL.RawQuery)
			if present {
				if valid && s.serveFileCapability(next, w, r, pattern, pol.login.oidc, token) {
					return
				}
				s.external.mu.Unlock()
				http.Error(w, "invalid media capability", http.StatusUnauthorized)
				return
			}
		}
		if pattern == canvasFilePattern && pol.login.oidc {
			// ServeMux hasn't populated PathValue yet.
			token, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, canvasFilePrefix), "/")
			if s.serveFileCapability(next, w, r, pattern, true, token) {
				return
			}
		}
		protected := isProtectedPattern(pattern, pol.publicDocs)
		// Extension machine endpoints check their own key, but cannot mint a
		// browser session in external mode. The canvas and media bypasses do
		// not accept legacy capabilities when external auth is active.
		if !pol.enabled || !protected || (pattern == canvasFilePattern && !pol.login.oidc) {
			s.external.mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}
		got := bearerToken(r)
		if got == "" && isSSETokenPattern(pattern) {
			got = strings.TrimSpace(r.URL.Query().Get("access_token"))
		}
		if acceptBearer(pol.tokens, got) {
			if pol.login.oidc {
				r = r.WithContext(context.WithValue(r.Context(), capabilityScopeKey{}, s.external.machineScope))
			}
			s.external.mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}
		if pol.login.oidc {
			sess, ok := s.lookupOIDC(r)
			sameOrigin := ok && oidcHostNavigation(r, sess.host)
			if isStateChanging(r) {
				sameOrigin = sameOrigin && isSameOriginRequest(r)
			}
			s.external.mu.Unlock()
			if ok {
				if !sameOrigin || r.Header.Get("Upgrade") != "" {
					http.Error(w, "cross-site request refused", http.StatusForbidden)
					return
				}
				r = r.WithContext(context.WithValue(r.Context(), capabilityScopeKey{}, sess.scope))
				s.serveOIDCRequest(next, w, r, sess)
				return
			}
		} else {
			ok := s.hasCookieSession(r, pol.login)
			s.external.mu.Unlock()
			if ok {
				if !isStateChanging(r) || isSameOriginRequest(r) {
					next.ServeHTTP(w, r)
					return
				}
				http.Error(w, "cross-site request refused", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="coddy"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// workspaceRawAccessToken distinguishes absent from malformed, including
// an invalid escape that url.Values would otherwise silently drop.
func workspaceRawAccessToken(raw string) (token string, present, valid bool) {
	if raw == "" {
		return "", false, false
	}
	q, err := url.ParseQuery(raw)
	if values, ok := q["access_token"]; ok {
		if err != nil || len(values) != 1 || values[0] == "" {
			return "", true, false
		}
		return values[0], true, true
	}
	if err != nil {
		for field := range strings.SplitSeq(raw, "&") {
			name, _, _ := strings.Cut(field, "=")
			if key, e := url.QueryUnescape(name); e == nil && key == "access_token" {
				return "", true, false
			}
		}
	}
	return "", false, false
}

// serveFileCapability is entered with external.mu held; if it handles the
// request it releases the lock. It cannot admit any route but the named file.
func (s *Server) serveFileCapability(next http.Handler, w http.ResponseWriter, r *http.Request, pattern string, external bool, token string) bool {
	var x oidcSession
	if external {
		scoped, session, ok := s.scopeCapability(r, token)
		if !ok {
			return false
		}
		r, x = scoped, session
	}
	valid := pattern == canvasFilePattern || s.acceptWorkspaceCapability(r)
	s.external.mu.Unlock()
	if !valid {
		http.Error(w, "invalid media capability", http.StatusUnauthorized)
	} else if x.life != nil {
		s.serveOIDCRequest(next, w, r, x)
	} else {
		next.ServeHTTP(w, r)
	}
	return true
}

// isOpaqueOrigin reports a request a page in an opaque origin made: a
// sandboxed frame (a canvas page among them), a data: or file: page. Such a
// page is never a client of the API, whatever credential it carries: a swarm
// relay replaces the caller's Authorization with its own node credential, and
// a page can read a token out of a workspace file, so a bearer proves nothing
// here. Requests without CORS (an <img>, a classic <script>) carry no Origin
// at all and read nothing the page can see.
func isOpaqueOrigin(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Origin")), "null")
}

// hasCookieSession reports whether the request carries a live browser
// session: the sign-in form's, a Telegram admin's Mini App session, or the
// side panel of Coddy for Browser.
func (s *Server) hasCookieSession(r *http.Request, login loginPolicy) bool {
	if login.oidc {
		_, ok := s.lookupOIDC(r)
		return ok
	}
	if _, ok := s.sessionFromRequest(r, login); ok {
		return true
	}
	if _, ok := s.telegramSessionFromRequest(r); ok {
		return true
	}
	_, ok := s.browserSessionFromRequest(r)
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
	// Coddy for Browser's endpoint checks the extension key itself; the
	// HTTP API's credentials are not what an extension holds.
	if pattern == browser.ConnectPattern || pattern == browser.FilesPattern {
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
