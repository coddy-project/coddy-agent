//go:build http

package httpserver

// Coddy for Browser sign-in. The extension's side panel shows this web UI in
// a frame, signed in by a one-time ticket the extension's endpoint handed the
// extension for the key it proved (external/browser, panel.go). The ticket
// is the password: whoever holds the extension key already drives the agent
// through the browser tool, so the panel's web UI is no wider a door.
//
// Panel sessions live in a store of their own, under a cookie of their own,
// bound to a digest of the extension key: a new browser.token ends them, and
// so does browser.enable turned off.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

const (
	// browserSessionCookieBaseName is the cookie of a panel session.
	browserSessionCookieBaseName = "coddy_browser_session"
	// browserSessionUserPrefix marks the user of a panel session.
	browserSessionUserPrefix = "browser:"
)

// browserEndpointKey is the extension key the endpoint accepts: browser.token
// while browser.enable is on, "" otherwise.
func browserEndpointKey(cfg *config.Config) string {
	if cfg == nil || !cfg.Browser.Enable {
		return ""
	}
	return strings.TrimSpace(cfg.Browser.Token)
}

// browserPanelKey is the digest panel sessions are bound to, when the
// configuration turns the extension's endpoint on with a key.
func (s *Server) browserPanelKey() (string, bool) {
	cfg := s.activeCfg()
	if cfg == nil || !cfg.Browser.Enable || strings.TrimSpace(cfg.Browser.Token) == "" {
		return "", false
	}
	return browser.KeyDigest(cfg.Browser.Token), true
}

func browserSessionCookieName(r *http.Request) string {
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if host == "" {
		return browserSessionCookieBaseName
	}
	sum := sha256.Sum256([]byte(host))
	return browserSessionCookieBaseName + "_" + hex.EncodeToString(sum[:4])
}

// browserSessionFromRequest returns the live panel session a request's
// cookie names, while the key it was opened with is still the configured one.
func (s *Server) browserSessionFromRequest(r *http.Request) (webauth.Session, bool) {
	key, ok := s.browserPanelKey()
	if !ok {
		return webauth.Session{}, false
	}
	c, err := r.Cookie(browserSessionCookieName(r))
	if err != nil || c == nil || c.Value == "" {
		return webauth.Session{}, false
	}
	return s.browserSessions.Lookup(c.Value, key)
}

func (s *Server) coddyAuthBrowserPost(w http.ResponseWriter, r *http.Request) {
	if !isSameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "cross-site sign-in refused"})
		return
	}
	key, ok := s.browserPanelKey()
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "Coddy for Browser sign-in is not available on this server"})
		return
	}
	var req struct {
		Ticket string `json:"ticket"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxLoginBodyBytes))
	if err != nil || json.Unmarshal(body, &req) != nil || strings.TrimSpace(req.Ticket) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid JSON body"})
		return
	}
	grant, ok := browser.RedeemPanelTicket(strings.TrimSpace(req.Ticket), time.Now())
	if !ok || grant.KeyDigest != key {
		s.log.Warn("browser panel sign-in refused", "reason", "unknown, used or stale ticket", "remote", loginClientAddr(r))
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "the side panel's ticket is used or expired; reopen the panel"})
		return
	}
	ttl := s.tgSessionTTL()
	tok, sess, err := s.browserSessions.Issue(browserSessionUserPrefix+grant.BrowserID, key, ttl)
	if err != nil {
		s.log.Error("browser panel sign-in could not open a session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "could not open a session"})
		return
	}
	cookie := sessionCookie(r, tok, ttl) //nolint:gosec // G124: sessionCookie sets HttpOnly and SameSite=Strict, and Secure whenever the request came over TLS
	cookie.Name = browserSessionCookieName(r)
	http.SetCookie(w, cookie)
	s.log.Info("browser panel sign-in", "browser", grant.BrowserID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"user":       sess.User,
		"expires_at": sess.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// clearBrowserSession ends the panel session a request carries, if any.
func (s *Server) clearBrowserSession(w http.ResponseWriter, r *http.Request) {
	name := browserSessionCookieName(r)
	if c, err := r.Cookie(name); err == nil && c != nil && c.Value != "" {
		s.browserSessions.Revoke(c.Value)
		clear := sessionCookie(r, "", 0) //nolint:gosec // G124: sessionCookie sets HttpOnly and SameSite=Strict, and Secure whenever the request came over TLS
		clear.Name = name
		clear.MaxAge = -1
		clear.Expires = time.Unix(0, 0)
		http.SetCookie(w, clear)
	}
}

// browserPanelTurn reads metadata.browser of a turn the panel's web UI sends:
// the tab the person writes from, which becomes the default tab of the chat's
// browser tool for this turn, with what the turn adds to the prompt. A turn
// without one takes back the default tab of an earlier panel turn. Nothing of
// it applies while the extension's endpoint is off: no browser tool would act
// on it.
func (s *Server) browserPanelTurn(raw json.RawMessage, sessionDir string) (browser.PanelTurn, error) {
	if browserEndpointKey(s.activeCfg()) == "" {
		return browser.PanelTurn{}, nil
	}
	var meta struct {
		Browser *browser.PanelTab `json:"browser"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &meta) != nil || meta.Browser == nil {
		return browser.PanelTurn{}, browser.ClearPanelTurn(sessionDir)
	}
	return browser.PreparePanelTurn(sessionDir, *meta.Browser)
}
