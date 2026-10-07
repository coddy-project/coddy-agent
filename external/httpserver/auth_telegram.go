//go:build http

package httpserver

// Telegram Mini App sign-in. The web UI opened as the Telegram bot's Mini App
// carries launch data signed with the bot's token; when the person who opened
// it is one of the bot's admins, that signature signs the browser in, with no
// password. Anybody else is refused and sees the ordinary sign-in form: the
// web UI is the whole agent, and only the bot's admins may reach it from the
// chat.
//
// Mini App sessions live in a store of their own, under a cookie of their
// own, keyed by a digest of the bot token: a rotated token ends them, and they
// never compete with the password sessions for room. They are accepted
// whatever closes the gate - the sign-in form or only a token - and each
// request checks again that the person is still an admin.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

const (
	// tgSessionCookieBaseName is the cookie of a Mini App session.
	tgSessionCookieBaseName = "coddy_tg_session"
	// telegramLaunchMaxAge is how old launch data may be: Telegram signs it
	// anew for every launch, so an hour covers a slow phone and keeps a leaked
	// copy from being useful for long.
	telegramLaunchMaxAge = time.Hour
	// tgSessionUserPrefix marks the user of a Mini App session.
	tgSessionUserPrefix = "telegram:"
)

// telegramBot is the Telegram bot this server signs Mini App users in for,
// when the configuration enables one with a token.
func (s *Server) telegramBot() (*config.TelegramGatewayConfig, string, bool) {
	cfg := s.activeCfg()
	if cfg == nil || !cfg.Gateways.Telegram.Enabled {
		return nil, "", false
	}
	tg := &cfg.Gateways.Telegram
	token := tg.EffectiveToken()
	return tg, token, token != ""
}

// tgCredential is the fingerprint Mini App sessions are bound to.
func tgCredential(token string) string {
	sum := sha256.Sum256([]byte("telegram\x00" + token))
	return hex.EncodeToString(sum[:])
}

func tgSessionCookieName(r *http.Request) string {
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if host == "" {
		return tgSessionCookieBaseName
	}
	sum := sha256.Sum256([]byte(host))
	return tgSessionCookieBaseName + "_" + hex.EncodeToString(sum[:4])
}

// tgSessionTTL is how long a Mini App session lives: the sign-in form's TTL
// when one is set, else the default.
func (s *Server) tgSessionTTL() time.Duration {
	if cfg := s.activeCfg(); cfg != nil {
		if ttl := cfg.HTTPServer.Login.SessionTTL(); ttl > 0 {
			return ttl
		}
	}
	return webauth.DefaultSessionTTL
}

// telegramSessionFromRequest returns the live Mini App session a request's
// cookie names, as long as its person is still one of the bot's admins.
func (s *Server) telegramSessionFromRequest(r *http.Request) (webauth.Session, bool) {
	tg, token, ok := s.telegramBot()
	if !ok {
		return webauth.Session{}, false
	}
	c, err := r.Cookie(tgSessionCookieName(r))
	if err != nil || c == nil || c.Value == "" {
		return webauth.Session{}, false
	}
	sess, ok := s.tgSessions.Lookup(c.Value, tgCredential(token))
	if !ok {
		return webauth.Session{}, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(sess.User, tgSessionUserPrefix), 10, 64)
	if err != nil || !tg.IsAdmin(id) {
		s.tgSessions.Revoke(c.Value)
		return webauth.Session{}, false
	}
	return sess, true
}

// errNotAnAdmin is the refusal a Mini App user who is not an admin gets.
var errNotAnAdmin = errors.New("only the bot's admins can open Coddy here")

func (s *Server) coddyAuthTelegramPost(w http.ResponseWriter, r *http.Request) {
	if !isSameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "cross-site sign-in refused"})
		return
	}
	tg, token, ok := s.telegramBot()
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "Telegram sign-in is not available on this server"})
		return
	}
	var req struct {
		InitData string `json:"init_data"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxLoginBodyBytes))
	if err != nil || json.Unmarshal(body, &req) != nil || strings.TrimSpace(req.InitData) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid JSON body"})
		return
	}
	now := time.Now()
	launch, err := webauth.VerifyTelegramInitData(req.InitData, token, now, telegramLaunchMaxAge)
	if err != nil {
		s.log.Warn("telegram sign-in refused", "reason", err.Error(), "remote", loginClientAddr(r))
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "invalid Telegram launch data"})
		return
	}
	if !tg.IsAdmin(launch.UserID) {
		s.log.Info("telegram sign-in refused", "reason", "not an admin", "user", launch.UserID)
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": errNotAnAdmin.Error()})
		return
	}
	// One launch signs one browser in: a copy of the launch data replayed
	// within its hour opens nothing.
	if !s.tgReplay.Use(launch.Hash, launch.AuthDate.Add(telegramLaunchMaxAge+time.Minute), now) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "this Telegram launch was already used; open the Mini App again"})
		return
	}
	ttl := s.tgSessionTTL()
	tok, sess, err := s.tgSessions.Issue(tgSessionUserPrefix+strconv.FormatInt(launch.UserID, 10), tgCredential(token), ttl)
	if err != nil {
		s.log.Error("telegram sign-in could not open a session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "could not open a session"})
		return
	}
	cookie := sessionCookie(r, tok, ttl)
	cookie.Name = tgSessionCookieName(r)
	http.SetCookie(w, cookie)
	s.log.Info("telegram sign-in", "user", launch.UserID, "username", launch.Username)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"user":       sess.User,
		"expires_at": sess.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// clearTelegramSession ends the Mini App session a request carries, if any.
func (s *Server) clearTelegramSession(w http.ResponseWriter, r *http.Request) {
	name := tgSessionCookieName(r)
	if c, err := r.Cookie(name); err == nil && c != nil && c.Value != "" {
		s.tgSessions.Revoke(c.Value)
		clear := sessionCookie(r, "", 0)
		clear.Name = name
		clear.MaxAge = -1
		clear.Expires = time.Unix(0, 0)
		http.SetCookie(w, clear)
	}
}
