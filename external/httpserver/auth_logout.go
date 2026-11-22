//go:build http

package httpserver

import (
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const logoutBrowserCookie = "coddy_logout_browser"
const logoutTicketCookie = "coddy_logout_ticket"
const logoutContinueHeader = "X-Coddy-Logout-Continue"
const logoutTicketTTL = time.Minute

func sameExternalPolicy(a, b *config.Config) bool {
	if a == nil || b == nil {
		return a == b
	}
	return reflect.DeepEqual(a.HTTPServer.Login, b.HTTPServer.Login) &&
		reflect.DeepEqual(a.HTTPServer.EffectiveAuthTokens(), b.HTTPServer.EffectiveAuthTokens()) && a.HTTPServer.PublicDocs == b.HTTPServer.PublicDocs
}

func (s *Server) localLoginSnapshot() (loginPolicy, uint64) {
	s.external.mu.Lock()
	defer s.external.mu.Unlock()
	return s.loginPolicyNow(), s.external.legacyModeGeneration
}

func (s *Server) externalSameOrigin(r *http.Request) bool {
	return sameOIDCOrigin(r, externalSettings(s.activeCfg()))
}

func sameOIDCOrigin(r *http.Request, o oidcSettings) bool {
	return isSameOriginRequest(r) && sameOIDCNavigation(r, o)
}

func sameOIDCNavigation(r *http.Request, o oidcSettings) bool {
	cb := o.callbackURL()
	return cb != nil && oidcHostNavigation(r, cb.Host)
}

func oidcHostNavigation(r *http.Request, host string) bool {
	if !strings.EqualFold(r.Host, host) || crossSiteNavigation(r) {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		return strings.EqualFold(origin, "https://"+host)
	}
	return true
}

func crossSiteNavigation(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "same-site", "cross-site":
		return true
	}
	return false
}

func (s *Server) coddyAuthLogoutPost(w http.ResponseWriter, r *http.Request) {
	authNavigationHeaders(w)
	a := &s.external
	a.mu.Lock()
	defer a.mu.Unlock()
	pol := s.authPolicyForRequest(r)
	if !isSameOriginRequest(r) || (pol.login.oidc && !s.externalSameOrigin(r)) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "cross-site request refused"})
		return
	}
	now := s.authNow()
	a.sweep(now)
	eligible := false
	if pol.login.oidc {
		bindings := make(map[string]struct{})
		for binding := range oidcBindings(r) {
			bindings[binding] = struct{}{}
			http.SetCookie(w, externalCookie(oidcBindingName(binding), "", "/", true, http.SameSiteLaxMode, -1))
		}
		if sess, ok := s.lookupOIDC(r); ok {
			bindings[sess.browser] = struct{}{}
			eligible = true
		}
		for binding := range bindings {
			eligible = eligible || a.flows[binding] != nil
			delete(a.flows, binding)
		}
		for key, x := range a.sessions {
			if _, ok := bindings[x.browser]; ok {
				a.revoke(key, x)
			}
		}
		http.SetCookie(w, externalCookie(oidcSessionCookie, "", "/", true, http.SameSiteStrictMode, -1))
	} else if pol.enabled && !s.trustedNoAuth(r) {
		eligible = s.hasCookieSession(r, pol.login)
	}
	if c, err := r.Cookie(sessionCookieName(r)); err == nil {
		s.sessions.Revoke(c.Value)
	}
	s.clearTelegramSession(w, r)
	s.clearBrowserSession(w, r)
	clear := sessionCookie(r, "", 0) //nolint:gosec // G124: HttpOnly and SameSite=Strict, with Secure matching the legacy cookie being cleared.
	clear.MaxAge = -1
	clear.Expires = time.Unix(0, 0)
	http.SetCookie(w, clear)

	// Repeating logout invalidates the previous navigation even if this POST
	// carries only an expired cookie. The ticket itself is sent only to GET.
	delete(a.tickets, opaqueCookie(r, logoutBrowserCookie))
	if c := s.activeCfg(); !a.stopped && eligible && c != nil && config.ValidLogoutURL(c.HTTPServer.Login.LogoutURL) && len(a.tickets) < logoutLimit {
		browser, ticket := authOpaque(), authOpaque()
		if a.tickets == nil {
			a.tickets = make(map[string]logoutTicket)
		}
		a.tickets[browser] = logoutTicket{ticket, now.Add(logoutTicketTTL)}
		secure := pol.login.oidc || requestIsTLS(r)
		http.SetCookie(w, externalCookie(logoutBrowserCookie, browser, "/coddy/auth/logout", secure, http.SameSiteStrictMode, int(logoutTicketTTL.Seconds())))
		http.SetCookie(w, externalCookie(logoutTicketCookie, ticket, logoutContinuePath, secure, http.SameSiteStrictMode, int(logoutTicketTTL.Seconds())))
		w.Header().Set(logoutContinueHeader, "1")
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) logoutContinue(w http.ResponseWriter, r *http.Request) {
	authNavigationHeaders(w)
	a := &s.external
	a.mu.Lock()
	defer a.mu.Unlock()
	c := s.activeCfg()
	secure := externalSettings(c).enabled || requestIsTLS(r)
	http.SetCookie(w, externalCookie(logoutTicketCookie, "", logoutContinuePath, secure, http.SameSiteStrictMode, -1))
	http.SetCookie(w, externalCookie(logoutBrowserCookie, "", "/coddy/auth/logout", secure, http.SameSiteStrictMode, -1))
	dest := "/"
	binding := opaqueCookie(r, logoutBrowserCookie)
	if t, ok := a.tickets[binding]; ok && r.Method == http.MethodGet && !a.stopped && s.authNow().Before(t.until) && subtleEqual(opaqueCookie(r, logoutTicketCookie), t.ticket) && !crossSiteNavigation(r) {
		delete(a.tickets, binding)
		if c != nil && config.ValidLogoutURL(c.HTTPServer.Login.LogoutURL) {
			dest = c.HTTPServer.Login.LogoutURL
		}
	}
	w.Header().Set("Location", dest)
	w.WriteHeader(http.StatusSeeOther)
}
