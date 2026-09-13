//go:build http

package httpserver

// The optional web sign-in: a browser posts a user and a password once, gets an
// HttpOnly cookie, and from then on reaches the same API an API client reaches
// with a bearer token. Both credentials open the one gate in auth.go, so adding
// a form never takes a token client's access away.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

// sessionCookieName is the cookie a signed-in browser carries. It is HttpOnly,
// so no script on the page can read it, and SameSite=Lax, so it does not travel
// with a cross-site request that changes anything.
const sessionCookieName = "coddy_session"

// maxLoginBodyBytes bounds the sign-in request body. A login is two short
// strings; anything larger is a mistake or an attempt to make the server chew.
const maxLoginBodyBytes = 8 << 10

// Where a resolved account came from, as reported by GET /coddy/config.
const (
	loginSourceConfig = "config"
	loginSourceEnv    = "env"
)

// loginAccount is the one operator account a server signs browsers in against.
type loginAccount struct {
	user string
	// hash is argon2id in PHC form, whether it was written into config.yaml or
	// derived from CODDY_HTTP_PASSWORD as this process started.
	hash string
	// source is loginSourceConfig or loginSourceEnv, for the settings screen.
	source string
	// ttl is how long a session lives; zero means until the browser closes.
	ttl time.Duration
}

// fingerprint identifies the account behind a session. A rotated password or a
// renamed user yields a different one, which is what ends the sessions the old
// account opened - with nothing to invalidate and no plaintext held anywhere.
func (a loginAccount) fingerprint() string { return webauth.CredentialFingerprint(a.user, a.hash) }

// loginPolicy is the sign-in form as it stands for one request.
type loginPolicy struct {
	// enabled means the form is active: anonymous browsers are refused and the
	// sign-in screen is what they should see.
	enabled bool
	// account is who may sign in. It is empty when broken is true.
	account loginAccount
	// broken is an `enable: true` with no account behind it - a configuration
	// that asks for a locked door and provides no key. The gate stays shut
	// rather than swinging open, and the startup path refuses this outright;
	// only a hot reload can reach it.
	broken bool
}

// SetExtraLogin registers the account supplied out of band (CODDY_HTTP_USER and
// CODDY_HTTP_PASSWORD, usually through $CODDY_HOME/.env).
//
// The password is hashed here, once, and the plaintext is not kept: from this
// point an environment account and a configured one are the same thing to the
// rest of the server. Empty values simply register nothing, so the variables
// being unset is not an error.
func (s *Server) SetExtraLogin(user, password string) error {
	user = strings.TrimSpace(user)
	if user == "" || password == "" {
		s.envLoginUser, s.envLoginHash = "", ""
		return nil
	}
	hash, err := webauth.HashPassword(password)
	if err != nil {
		return err
	}
	s.envLoginUser, s.envLoginHash = user, hash
	return nil
}

// loginPolicyNow resolves the account from the live config and the environment.
//
// The order is the one the rest of Coddy uses for credentials: an explicit
// switch in the file wins over everything, then the environment, then the file's
// own account. That is the same precedence .env has against config.yaml, so an
// operator who moves a credential into the environment does not have to remember
// a second rule.
func (s *Server) loginPolicyNow() loginPolicy {
	var cfgLogin config.HTTPLoginConfig
	if c := s.activeCfg(); c != nil {
		cfgLogin = c.HTTPServer.Login
	}
	if cfgLogin.IsExplicitlyDisabled() {
		return loginPolicy{}
	}
	ttl := cfgLogin.SessionTTL()
	switch {
	case s.envLoginUser != "" && s.envLoginHash != "":
		return loginPolicy{enabled: true, account: loginAccount{
			user: s.envLoginUser, hash: s.envLoginHash, source: loginSourceEnv, ttl: ttl,
		}}
	case cfgLogin.HasAccount():
		return loginPolicy{enabled: true, account: loginAccount{
			user: cfgLogin.User, hash: cfgLogin.PasswordHash, source: loginSourceConfig, ttl: ttl,
		}}
	case cfgLogin.IsExplicitlyEnabled():
		return loginPolicy{enabled: true, broken: true}
	}
	return loginPolicy{}
}

func (s *Server) registerAuthRoutes() {
	s.mux.HandleFunc("POST /coddy/auth/login", s.coddyAuthLoginPost)
	s.mux.HandleFunc("POST /coddy/auth/logout", s.coddyAuthLogoutPost)
	s.mux.HandleFunc("GET /coddy/auth/me", s.coddyAuthMeGet)
}

// isAuthRoutePattern reports the three routes the gate lets through unauthenticated.
//
// They have to be reachable by a browser that has nothing yet: one says whether
// a form is needed, one is the form, and one ends a session that may already be
// invalid. None of them returns anything about the machine behind the gate.
func isAuthRoutePattern(pattern string) bool {
	switch pattern {
	case "POST /coddy/auth/login", "POST /coddy/auth/logout", "GET /coddy/auth/me":
		return true
	}
	return false
}

// sessionFromRequest returns the live session a request's cookie names.
func (s *Server) sessionFromRequest(r *http.Request, pol loginPolicy) (webauth.Session, bool) {
	if !pol.enabled || pol.broken {
		return webauth.Session{}, false
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c == nil || c.Value == "" {
		return webauth.Session{}, false
	}
	return s.sessions.Lookup(c.Value, pol.account.fingerprint())
}

// isStateChanging reports whether a request may alter anything. The safe methods
// are exactly the ones a cross-site page can already trigger without a form.
func isStateChanging(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// isSameOriginRequest is the CSRF check for a cookie-authenticated write.
//
// Browsers label every request they make: `Sec-Fetch-Site` says where it came
// from, and `Origin` says which page. A request carrying neither is not a
// browser navigation at all - a curl with a cookie, say - and is let through,
// because refusing it would break clients without protecting anybody: the
// attack this stops needs a browser, and every browser that can mount it also
// sends the headers.
//
// Bearer clients never reach here: a token is not attached by a browser on
// somebody else's behalf, which is the whole reason CSRF exists.
func isSameOriginRequest(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || strings.EqualFold(origin, "null") {
		return origin == ""
	}
	return originMatchesHost(origin, r.Host)
}

// originMatchesHost compares an Origin header with the host the request was
// addressed to, which is what "same origin" means to the server: the scheme is
// whatever got it here, and a proxy in front may have terminated TLS.
func originMatchesHost(origin, host string) bool {
	if i := strings.Index(origin, "://"); i >= 0 {
		origin = origin[i+3:]
	}
	return origin != "" && strings.EqualFold(origin, host)
}

func (s *Server) coddyAuthMeGet(w http.ResponseWriter, r *http.Request) {
	pol := s.loginPolicyNow()
	auth := s.authPolicyNow()
	out := map[string]interface{}{
		// login_required is what makes the app draw a sign-in screen instead of
		// the composer; auth_required tells it that a server it cannot sign in
		// to still wants a credential, so it can say so rather than render an
		// empty page over a row of 401s.
		"login_required": pol.enabled,
		"auth_required":  auth.enabled,
		"authenticated":  false,
	}
	if pol.enabled && !pol.broken {
		out["mode"] = config.LoginModePassword
	}
	if sess, ok := s.sessionFromRequest(r, pol); ok {
		out["authenticated"] = true
		out["user"] = sess.User
		out["expires_at"] = sess.ExpiresAt.UTC().Format(time.RFC3339)
	} else if acceptBearer(auth.tokens, bearerToken(r)) {
		// A token client asking the same question: it is authenticated, and it
		// has no sign-in to do.
		out["authenticated"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) coddyAuthLoginPost(w http.ResponseWriter, r *http.Request) {
	pol := s.loginPolicyNow()
	if !pol.enabled {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "sign-in is not enabled on this server"})
		return
	}
	// A sign-in is a state change carrying a cookie afterwards, so it is checked
	// like one: a page on another site must not be able to sign this browser
	// into somebody else's account.
	if !isSameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "cross-site sign-in refused"})
		return
	}
	if pol.broken {
		s.log.Error("sign-in is enabled but no account is configured",
			"hint", "set httpserver.login.user and password_hash (`coddy serve set-password`), or CODDY_HTTP_USER / CODDY_HTTP_PASSWORD")
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "sign-in is enabled but no account is configured"})
		return
	}

	var req struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxLoginBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "read body"})
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid JSON body"})
		return
	}

	// The wait grows with the failures already seen from this address, which is
	// what makes guessing expensive without ever locking the account - and the
	// machine's own loopback is exempt, so nobody can shut the operator out of
	// their own agent from outside.
	if d := s.loginThrottle.Delay(r.RemoteAddr); d > 0 {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return
		}
	}

	if !s.verifyLogin(pol.account, req.User, req.Password) {
		s.loginThrottle.Fail(r.RemoteAddr)
		s.log.Warn("web sign-in refused", "user", req.User, "remote", webauth.NormalizeAddr(r.RemoteAddr))
		// The same answer whether the user exists or not: a form that says
		// "no such user" is a list of the users that do exist.
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "invalid credentials"})
		return
	}
	s.loginThrottle.Reset(r.RemoteAddr)

	token, sess, err := s.sessions.Issue(pol.account.user, pol.account.fingerprint(), pol.account.ttl)
	if err != nil {
		s.log.Error("web sign-in could not open a session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "could not open a session"})
		return
	}
	http.SetCookie(w, sessionCookie(r, token, pol.account.ttl))
	s.log.Info("web sign-in", "user", pol.account.user, "remote", webauth.NormalizeAddr(r.RemoteAddr))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"user":       sess.User,
		"expires_at": sess.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// verifyLogin checks a submitted pair against the account.
//
// The password is hashed even when the user name is already wrong, so a wrong
// name and a wrong password cost the same time and neither answers the question
// "does this account exist".
func (s *Server) verifyLogin(acc loginAccount, user, password string) bool {
	userOK := subtleEqual(acc.user, strings.TrimSpace(user))
	passOK, err := webauth.VerifyPassword(acc.hash, password)
	if err != nil {
		// A stored hash this build cannot read is the operator's problem, not
		// the visitor's, and it must be loud: otherwise the form just keeps
		// saying "invalid credentials" to the right password.
		s.log.Error("web sign-in cannot read the configured password hash", "error", err,
			"hint", "rewrite it with `coddy serve set-password`")
		return false
	}
	return userOK && passOK
}

func (s *Server) coddyAuthLogoutPost(w http.ResponseWriter, r *http.Request) {
	if !isSameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{"error": "cross-site request refused"})
		return
	}
	if c, err := r.Cookie(sessionCookieName); err == nil && c != nil && c.Value != "" {
		s.sessions.Revoke(c.Value)
	}
	// Clear the cookie whatever happened, so a browser holding a session this
	// server no longer knows about stops sending it.
	clear := sessionCookie(r, "", 0)
	clear.MaxAge = -1
	clear.Expires = time.Unix(0, 0)
	http.SetCookie(w, clear)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// sessionCookie builds the cookie for one session.
//
// Secure is set when the request arrived over TLS, directly or through a proxy
// that says so. Setting it unconditionally would make the cookie unusable on the
// plain-HTTP loopback deployments that are the common case, and a browser that
// never sends the cookie back cannot sign in at all.
func sessionCookie(r *http.Request, token string, ttl time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsTLS(r),
	}
	if ttl > 0 {
		c.MaxAge = int(ttl / time.Second)
	}
	return c
}

func requestIsTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
