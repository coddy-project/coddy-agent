//go:build http

package httpserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"strings"
)

type capabilityScopeKey struct{}
type capabilityScope struct{ id, secret string }

func (c capabilityScope) signingKey(base []byte) []byte {
	if c.id == "" {
		return base
	}
	mac := hmac.New(sha256.New, base)
	_, _ = mac.Write([]byte("oidc-capability\x00" + c.secret))
	return mac.Sum(nil)
}

func (c capabilityScope) wrap(token string) string {
	if c.id == "" {
		return token
	}
	return c.id + "~" + token
}

func scopeIn(ctx context.Context) capabilityScope {
	c, _ := ctx.Value(capabilityScopeKey{}).(capabilityScope)
	return c
}

// A request never receives key material from the URL. The locator only
// selects a revocable server-side key; the file handler checks the MAC and
// the original capability restrictions before opening the file.
func (s *Server) scopeCapability(r *http.Request, token string) (*http.Request, oidcSession, bool) {
	id, rest, ok := strings.Cut(token, "~")
	if !ok || rest == "" || len(id) != 43 {
		return r, oidcSession{}, false
	}
	a := &s.external
	if id == a.machineScope.id {
		return r.WithContext(context.WithValue(r.Context(), capabilityScopeKey{}, a.machineScope)), oidcSession{}, true
	}
	key := a.capSessions[id]
	x, ok := a.sessions[key]
	if !ok || !strings.EqualFold(r.Host, x.host) {
		return r, oidcSession{}, false
	}
	if !s.authNow().Before(x.until) {
		a.revoke(key, x)
		return r, oidcSession{}, false
	}
	return r.WithContext(context.WithValue(r.Context(), capabilityScopeKey{}, x.scope)), x, true
}

func (c capabilityScope) unwrap(token string) string {
	if c.id == "" {
		return token
	}
	rest, ok := strings.CutPrefix(token, c.id+"~")
	if !ok {
		return ""
	}
	return rest
}
