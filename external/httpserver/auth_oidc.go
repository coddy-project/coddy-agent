//go:build http

package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const (
	oidcStartPath           = "/coddy/auth/oidc/start"
	oidcCallbackPath        = config.OIDCCallbackPath
	logoutContinuePath      = "/coddy/auth/logout/continue"
	oidcFlowTTL             = 5 * time.Minute
	oidcSessionTTL          = 15 * time.Minute
	oidcFlowLimit           = 256
	oidcSessionLimit        = 1024
	logoutLimit             = 256
	oidcSessionCookie       = "__Host-coddy_oidc_session"
	oidcBindingCookiePrefix = "__Host-coddy_oidc_browser_"
	oidcBindingQuery        = "binding=1"
)

type oidcSettings struct {
	enabled                                bool
	issuer, client, secret, callback, role string
}

func externalSettings(c *config.Config) oidcSettings {
	if c == nil {
		return oidcSettings{}
	}
	o := c.HTTPServer.Login.OIDC
	return oidcSettings{o.Enable, o.Issuer, o.ClientID, o.ClientSecret, o.RedirectURL, o.RequiredRole}
}

func validateExternalLogin(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	return cfg.HTTPServer.Login.OIDC.Validate()
}

func (o oidcSettings) callbackURL() *url.URL {
	u, _ := (config.HTTPOIDCConfig{RedirectURL: o.callback}).CallbackURL()
	return u
}

func oidcHTTPS(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.ContainsAny(raw, "#\\") {
		return nil
	}
	return u
}

type oidcFlow struct {
	state, nonce, verifier string
	generation             uint64
	until                  time.Time
	used                   bool
}

type oidcSession struct {
	scope                    capabilityScope
	issuer, subject, browser string
	host                     string
	until                    time.Time
	life                     context.Context
	revoke                   context.CancelFunc
}

type logoutTicket struct {
	ticket string
	until  time.Time
}

// All auth-policy publication, flow transitions and revocations linearize on mu.
// A flow's pointer is its browser generation: replacing or removing it fences
// work already exchanging a code outside the lock. No tombstones are needed.
type externalAuth struct {
	legacyModeGeneration uint64
	telegramGeneration   uint64
	capSessions          map[string]string
	machineScope         capabilityScope
	mu                   sync.Mutex
	generation           uint64
	flows                map[string]*oidcFlow
	sessions             map[string]oidcSession
	tickets              map[string]logoutTicket
	stopped              bool

	discoveryMu       sync.Mutex
	discoverySettings oidcSettings
	provider          *oidcProvider
	discoveryRetry    time.Time
	slots             chan struct{}
	budget            oidcBudget
	client            *http.Client // installed once; tests may inject a TLS fake before serving
}

func (a *externalAuth) sweep(now time.Time) {
	for k, f := range a.flows {
		if !now.Before(f.until) {
			delete(a.flows, k)
		}
	}
	for k, x := range a.sessions {
		if !now.Before(x.until) {
			a.revoke(k, x)
		}
	}
	for k, t := range a.tickets {
		if !now.Before(t.until) {
			delete(a.tickets, k)
		}
	}
}

func (a *externalAuth) revoke(key string, x oidcSession) {
	delete(a.sessions, key)
	delete(a.capSessions, x.scope.id)
	x.revoke()
}

func (a *externalAuth) reset() {
	a.generation++
	a.machineScope = capabilityScope{authOpaque(), authOpaque()}
	clear(a.flows)
	clear(a.tickets)
	for k, x := range a.sessions {
		a.revoke(k, x)
	}
}

func authOpaque() string {
	var b [32]byte
	// Go's crypto/rand.Read fills the buffer or terminates the process.
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func opaqueCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil || len(c.Value) != 43 {
		return ""
	}
	return c.Value
}

// Use distinct cookie names so concurrent first-start redirects cannot
// overwrite a binding already echoed by another tab. These are browser-session
// cookies; the transaction expiry lives exclusively in the server's flow.
func oidcBindingName(binding string) string {
	sum := sha256.Sum256([]byte(binding))
	return oidcBindingCookiePrefix + hex.EncodeToString(sum[:12])
}

func oidcBindings(r *http.Request) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, c := range r.Cookies() {
			if strings.HasPrefix(c.Name, oidcBindingCookiePrefix) && len(c.Value) == 43 && oidcBindingName(c.Value) == c.Name && !yield(c.Value) {
				return
			}
		}
	}
}

func externalCookie(name, value, path string, secure bool, sameSite http.SameSite, seconds int) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: path, Secure: secure, HttpOnly: true, SameSite: sameSite, MaxAge: seconds} //nolint:gosec // G124: OIDC always requires Secure; legacy logout follows request TLS. SameSite is Strict except for the OIDC callback binding (Lax).
}

func authNavigationHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func oidcRefused(w http.ResponseWriter, status int) {
	http.Error(w, "external sign-in refused", status)
}

func (s *Server) oidcAdmission(r *http.Request) (oidcSettings, uint64, bool) {
	a := &s.external
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := s.activeCfg()
	o := externalSettings(cfg)
	cb := o.callbackURL()
	return o, a.generation, !a.stopped && !s.noAuth && !s.trustedNoAuth(r) && o.enabled && cb != nil && validateExternalLogin(cfg) == nil && strings.EqualFold(r.Host, cb.Host)
}

func (s *Server) oidcStart(w http.ResponseWriter, r *http.Request) {
	authNavigationHeaders(w)
	o, gen, ok := s.oidcAdmission(r)
	preflight := r.URL.RawQuery == oidcBindingQuery
	if !ok || r.Method != http.MethodGet || (r.URL.RawQuery != "" && !preflight) || !sameOIDCNavigation(r, o) {
		oidcRefused(w, http.StatusNotFound)
		return
	}
	a := &s.external
	if !a.takeSlot() {
		oidcRefused(w, http.StatusTooManyRequests)
		return
	}
	defer a.releaseSlot()
	ctx, cancel := context.WithTimeout(r.Context(), oidcNetworkTimeout)
	defer cancel()

	a.mu.Lock()
	if gen != a.generation {
		a.mu.Unlock()
		oidcRefused(w, http.StatusServiceUnavailable)
		return
	}
	now := s.authNow()
	a.sweep(now)
	// The live session's binding wins. Logout can then fence this flow using
	// the session alone, even if a separate binding cookie is not sent to it.
	var browser string
	if sess, live := s.lookupOIDC(r); live {
		browser = sess.browser
	}
	var echoed bool
	for binding := range oidcBindings(r) {
		if browser == "" {
			browser = binding
		}
		if browser == binding {
			echoed = true
			break
		}
	}
	if !echoed {
		a.mu.Unlock()
		if preflight {
			oidcRefused(w, http.StatusBadRequest)
			return
		}
		if browser == "" {
			browser = authOpaque()
		}
		http.SetCookie(w, externalCookie(oidcBindingName(browser), browser, "/", true, http.SameSiteLaxMode, 0))
		w.Header().Set("Location", oidcStartPath+"?"+oidcBindingQuery)
		w.WriteHeader(http.StatusSeeOther)
		return
	}
	if a.flows[browser] == nil && len(a.flows) >= oidcFlowLimit {
		a.mu.Unlock()
		oidcRefused(w, http.StatusTooManyRequests)
		return
	}
	f := &oidcFlow{state: authOpaque(), nonce: authOpaque(), verifier: oauth2.GenerateVerifier(), generation: gen, until: now.Add(oidcFlowTTL)}
	if a.flows == nil {
		a.flows = make(map[string]*oidcFlow)
	}
	a.flows[browser] = f
	a.mu.Unlock()

	p, err := a.discover(ctx, o)
	a.mu.Lock()
	if err != nil || a.flows[browser] != f || !s.authNow().Before(f.until) {
		if a.flows[browser] == f {
			delete(a.flows, browser)
		}
		a.mu.Unlock()
		oidcRefused(w, http.StatusServiceUnavailable)
		return
	}
	a.mu.Unlock()
	w.Header().Set("Location", p.oauth.AuthCodeURL(f.state, oidc.Nonce(f.nonce), oauth2.S256ChallengeOption(f.verifier)))
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	authNavigationHeaders(w)
	o, gen, ok := s.oidcAdmission(r)
	if !ok || r.Method != http.MethodGet || len(r.URL.RawQuery) > 8<<10 {
		oidcRefused(w, 400)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q["state"]) != 1 {
		oidcRefused(w, 400)
		return
	}
	a := &s.external
	a.mu.Lock()
	var browser string
	var f *oidcFlow
	now := s.authNow()
	if gen == a.generation {
		for binding := range oidcBindings(r) {
			candidate := a.flows[binding]
			if candidate != nil && candidate.generation == gen && !candidate.used && now.Before(candidate.until) && subtleEqual(candidate.state, q.Get("state")) {
				browser, f = binding, candidate
				break
			}
		}
	}
	if f == nil {
		a.mu.Unlock()
		oidcRefused(w, 400)
		return
	}
	f.used = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.flows[browser] == f {
			delete(a.flows, browser)
		}
	}()
	if q.Has("error") || len(q["code"]) != 1 || q.Get("code") == "" || (q.Has("iss") && (len(q["iss"]) != 1 || q.Get("iss") != o.issuer)) {
		oidcRefused(w, 400)
		return
	}
	if !a.takeSlot() {
		oidcRefused(w, 429)
		return
	}
	defer a.releaseSlot()
	ctx, cancel := context.WithTimeout(r.Context(), oidcNetworkTimeout)
	defer cancel()
	p, err := a.discover(ctx, o)
	if err != nil {
		oidcRefused(w, 503)
		return
	}
	tok, err := p.oauth.Exchange(context.WithValue(ctx, oauth2.HTTPClient, a.client), q.Get("code"), oauth2.VerifierOption(f.verifier))
	if err != nil {
		oidcRefused(w, 401)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	id, err := p.verify(ctx, raw, o, f.nonce)
	if err != nil {
		oidcRefused(w, 401)
		return
	}
	token, expires, ok := s.commitOIDCSession(ctx, browser, f, id)
	if !ok {
		oidcRefused(w, 401)
		return
	}
	http.SetCookie(w, externalCookie(oidcSessionCookie, token, "/", true, http.SameSiteStrictMode, max(1, int(expires.Sub(s.authNow()).Seconds()))))
	w.Header().Set("Location", "/")
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Server) commitOIDCSession(ctx context.Context, browser string, f *oidcFlow, id *oidc.IDToken) (string, time.Time, bool) {
	a := &s.external
	a.mu.Lock()
	defer a.mu.Unlock()
	now := s.authNow()
	until := minTime(now.Add(oidcSessionTTL), id.Expiry)
	if ctx.Err() != nil || f.generation != a.generation || a.flows[browser] != f || !f.used || !now.Before(f.until) || !now.Before(until) {
		return "", time.Time{}, false
	}
	a.sweep(now)
	// A new sign-in in the same browser replaces, rather than promotes, its credentials.
	for k, x := range a.sessions {
		if x.browser == browser {
			a.revoke(k, x)
		}
	}
	if len(a.sessions) >= oidcSessionLimit {
		return "", time.Time{}, false
	}
	if a.sessions == nil {
		a.sessions = make(map[string]oidcSession)
	}
	callback := externalSettings(s.activeCfg()).callbackURL()
	if callback == nil {
		return "", time.Time{}, false
	}
	token := authOpaque()
	life, revoke := context.WithCancel(context.Background())
	scope := capabilityScope{id: authOpaque(), secret: token}
	if a.capSessions == nil {
		a.capSessions = make(map[string]string)
	}
	a.capSessions[scope.id] = token
	a.sessions[token] = oidcSession{issuer: id.Issuer, subject: id.Subject, browser: browser, host: callback.Host, until: until, life: life, revoke: revoke, scope: scope}
	return token, until, true
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Caller holds external.mu and has confirmed external mode on this request.
func (s *Server) lookupOIDC(r *http.Request) (oidcSession, bool) {
	a := &s.external
	key := opaqueCookie(r, oidcSessionCookie)
	x, ok := a.sessions[key]
	if ok && !s.authNow().Before(x.until) {
		a.revoke(key, x)
		return oidcSession{}, false
	}
	return x, ok && strings.EqualFold(r.Host, x.host)
}

var errOIDCIdentity = errors.New("invalid external identity")

func (p *oidcProvider) verify(ctx context.Context, raw string, o oidcSettings, nonce string) (*oidc.IDToken, error) {
	if raw == "" || len(raw) > 32<<10 {
		return nil, errOIDCIdentity
	}
	id, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, errOIDCIdentity
	}
	var claims map[string]json.RawMessage
	if id.Claims(&claims) != nil || id.Issuer != o.issuer || strings.TrimSpace(id.Subject) == "" || !subtleEqual(id.Nonce, nonce) || !validOIDCAZP(claims["azp"], id.Audience, o.client) || !hasOIDCRole(claims["roles"], o.role) {
		return nil, errOIDCIdentity
	}
	return id, nil
}

func validOIDCAZP(raw json.RawMessage, aud []string, client string) bool {
	if len(raw) == 0 {
		return len(aud) == 1
	}
	var azp string
	return json.Unmarshal(raw, &azp) == nil && azp == client
}

func hasOIDCRole(raw json.RawMessage, required string) bool {
	// A []string accepts JSON null elements as empty strings. Decode each
	// element through *string, and reject the entire array on any non-string.
	var roles []*string
	if json.Unmarshal(raw, &roles) != nil || roles == nil {
		return false
	}
	found := false
	for _, role := range roles {
		if role == nil {
			return false
		}
		found = found || *role == required
	}
	return found
}
