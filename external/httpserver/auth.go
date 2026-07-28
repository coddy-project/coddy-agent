//go:build http

package httpserver

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// authPolicy is the effective bearer-token policy for one request, snapshotting the live
// config plus any out-of-band tokens so PUT /coddy/config hot reloads take effect immediately.
type authPolicy struct {
	enabled    bool
	tokens     []string
	publicDocs bool
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

// authPolicyNow builds the current policy from the atomic config and any extra tokens.
func (s *Server) authPolicyNow() authPolicy {
	var pol authPolicy
	if c := s.activeCfg(); c != nil {
		pol.tokens = append(pol.tokens, c.HTTPServer.EffectiveAuthTokens()...)
		pol.publicDocs = c.HTTPServer.PublicDocs
	}
	if len(s.extraAuthTokens) > 0 {
		pol.tokens = append(pol.tokens, s.extraAuthTokens...)
	}
	// Auth is active whenever at least one token is configured (YAML, CLI, or env).
	pol.enabled = len(pol.tokens) > 0
	return pol
}

// authGate wraps next with per-request bearer authentication. When no policy is active it is a
// transparent pass-through, so unauthenticated deployments behave exactly as before.
func (s *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pol := s.authPolicyNow()
		_, pattern := s.mux.Handler(r)
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
		if !acceptBearer(pol.tokens, got) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="coddy"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isProtectedPattern classifies a matched route pattern rather than using a fragile string prefix:
// the SPA shell and static assets fall through to the "/" catch-all and stay public; every
// registered API route (/v1/*, /coddy/*) is protected; /docs and /openapi.* are protected unless
// publicDocs is set.
func isProtectedPattern(pattern string, publicDocs bool) bool {
	if pattern == "" || pattern == "/" {
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
	return pattern == "GET /coddy/sessions/{id}/composer-stream"
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
