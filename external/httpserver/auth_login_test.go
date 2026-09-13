//go:build http

package httpserver

// The edges of the web sign-in: what a wrong password costs, what a cross-site
// write gets, what a cookie looks like, what happens when the account moves
// under a live session, and what a bearer client sees through all of it. The
// happy path is features/webui_login.feature.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

const (
	loginTestUser     = "operator"
	loginTestPassword = "correct-horse"
)

func loginTestHash(t *testing.T, plain string) string {
	t.Helper()
	h, err := webauth.HashPasswordWith(plain, loginTestHashParams)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return h
}

// newLoginServer builds a Server with a login account over a real (but
// LLM-free) session manager, so a request the gate lets through reaches a
// handler that can answer rather than a nil pointer.
func newLoginServer(t *testing.T, login config.HTTPLoginConfig) *Server {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		Paths:      config.Paths{Home: t.TempDir(), CWD: root},
		Models:     []config.ModelEntry{{Model: "openai/gpt-4o", MaxTokens: 100, Temperature: 0.2}},
		Agent:      config.Agent{Model: "openai/gpt-4o"},
		HTTPServer: config.HTTPServerConfig{Login: login},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	log := slog.New(slog.DiscardHandler)
	mgr := session.NewManager(cfg, noopSender{}, runner, log, root, &session.FileStore{Root: t.TempDir()})
	srv := New(cfg, mgr, log, root)
	t.Cleanup(srv.Drain)
	return srv
}

func configuredLogin(t *testing.T) config.HTTPLoginConfig {
	t.Helper()
	return config.HTTPLoginConfig{User: loginTestUser, PasswordHash: loginTestHash(t, loginTestPassword)}
}

// loginRequest builds a same-origin sign-in POST, the way the page sends it.
func loginRequest(user, password string) *http.Request {
	body, _ := json.Marshal(map[string]string{"user": user, "password": password})
	r := httptest.NewRequest(http.MethodPost, "/coddy/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	return r
}

// signIn runs one sign-in through the whole handler chain and returns the
// recorder, so a test can read both the status and the cookie.
func signIn(t *testing.T, srv *Server, user, password string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, loginRequest(user, password))
	return w
}

// isSessionCookie matches the per-origin cookie name, whose suffix is a digest
// of the host the request was addressed to.
func isSessionCookie(name string) bool {
	return strings.HasPrefix(name, sessionCookieBaseName)
}

func sessionCookieOf(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if isSessionCookie(c.Name) {
			return c
		}
	}
	t.Fatalf("no %q cookie in the response (status %d, body %s)", sessionCookieBaseName, w.Code, w.Body.String())
	return nil
}

func TestLoginRefusesWrongPasswordAndUnknownUser(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))

	wrongPass := signIn(t, srv, loginTestUser, "not-the-password")
	if wrongPass.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status %d, want 401", wrongPass.Code)
	}
	unknownUser := signIn(t, srv, "nobody", loginTestPassword)
	if unknownUser.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: status %d, want 401", unknownUser.Code)
	}
	// The two answers must be indistinguishable, or the form is a directory of
	// the accounts that exist.
	if wrongPass.Body.String() != unknownUser.Body.String() {
		t.Fatalf("a wrong password and an unknown user answer differently:\n%q\n%q",
			wrongPass.Body.String(), unknownUser.Body.String())
	}
	for _, w := range []*httptest.ResponseRecorder{wrongPass, unknownUser} {
		for _, c := range w.Result().Cookies() {
			if isSessionCookie(c.Name) && c.Value != "" {
				t.Fatal("a refused sign-in handed out a session cookie")
			}
		}
	}
}

func TestLoginRefusesEmptyAndMalformedBodies(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"empty credentials", `{"user":"","password":""}`, http.StatusUnauthorized},
		{"no password", `{"user":"operator"}`, http.StatusUnauthorized},
		{"not json", `not json at all`, http.StatusBadRequest},
		{"empty body", ``, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/coddy/auth/login", strings.NewReader(tc.body))
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestLoginBodyIsBounded(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	// A megabyte of password is not a password. The read is capped, so what
	// arrives is truncated JSON and the answer is a parse error, not a hang.
	huge, _ := json.Marshal(map[string]string{"user": loginTestUser, "password": strings.Repeat("x", 1<<20)})
	r := httptest.NewRequest(http.MethodPost, "/coddy/auth/login", bytes.NewReader(huge))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

func TestLoginDisabledRefusesTheForm(t *testing.T) {
	srv := newLoginServer(t, config.HTTPLoginConfig{})
	w := signIn(t, srv, loginTestUser, loginTestPassword)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 when no sign-in is configured", w.Code)
	}
	// ...and the API is open, exactly as it was before this feature existed.
	me := httptest.NewRecorder()
	srv.Handler().ServeHTTP(me, httptest.NewRequest(http.MethodGet, "/coddy/auth/me", nil))
	var got map[string]interface{}
	if err := json.Unmarshal(me.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode /coddy/auth/me: %v", err)
	}
	if required, _ := got["login_required"].(bool); required {
		t.Fatal("a server with no account reports that sign-in is required")
	}
	if authRequired, _ := got["auth_required"].(bool); authRequired {
		t.Fatal("a server with no credential reports that auth is required")
	}
}

func TestLoginEnabledWithoutAnAccountFailsClosed(t *testing.T) {
	// A configuration that asks for a locked door and hands over no key. The
	// startup path refuses it outright; a hot reload can still produce it, and
	// then the gate must stay shut rather than swing open.
	on := true
	srv := newLoginServer(t, config.HTTPLoginConfig{Enabled: &on})

	gated := httptest.NewRecorder()
	srv.Handler().ServeHTTP(gated, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if gated.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: a broken login must not open the API", gated.Code)
	}
	w := signIn(t, srv, loginTestUser, loginTestPassword)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 with a message the operator can act on", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no account") {
		t.Fatalf("the answer does not say what is wrong: %s", w.Body.String())
	}
}

func TestLoginCookieFlags(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	c := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	if !c.HttpOnly {
		t.Fatal("the session cookie is readable by scripts on the page")
	}
	if c.SameSite != http.SameSiteStrictMode {
		// Strict costs nothing here - the page is public and every call the
		// loaded page makes is same-origin - and it keeps the cookie off the
		// one thing Lax allows: a cross-site link to an API route.
		t.Fatalf("SameSite = %v, want Strict", c.SameSite)
	}
	if c.Path != "/" {
		t.Fatalf("cookie path = %q, want /", c.Path)
	}
	if c.Secure {
		t.Fatal("a plain-HTTP sign-in set a Secure cookie, which the browser would never send back")
	}
	if c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Fatalf("with no TTL the cookie must last only for the browser session, got MaxAge=%d Expires=%v", c.MaxAge, c.Expires)
	}
}

func TestLoginCookieIsSecureBehindTLSAndAProxy(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))

	proxied := loginRequest(loginTestUser, loginTestPassword)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, proxied)
	if !sessionCookieOf(t, w).Secure {
		t.Fatal("a request a proxy says arrived over TLS should get a Secure cookie")
	}
}

func TestLoginCookieCarriesTheConfiguredTTL(t *testing.T) {
	login := configuredLogin(t)
	login.SessionTTLHours = 24
	srv := newLoginServer(t, login)
	c := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	if want := int((24 * time.Hour) / time.Second); c.MaxAge != want {
		t.Fatalf("cookie MaxAge = %d, want %d", c.MaxAge, want)
	}
}

// signedInRequest builds a request carrying a live session cookie.
func signedInRequest(t *testing.T, srv *Server, method, path string) *http.Request {
	t.Helper()
	c := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	r := httptest.NewRequest(method, path, nil)
	r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	return r
}

func TestCookieWriteFromAnotherSiteIsRefused(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"labelled cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"labelled same-site", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"opaque origin", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"same origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, 0},
		{"own origin header", map[string]string{"Origin": "http://example.com"}, 0},
		// A cookie with no label at all is refused too: a browser old enough to
		// send neither header on a cross-site POST is old enough to ignore
		// SameSite, so "unlabelled" cannot be read as "not a browser". A script
		// that wants to write sends an Origin, or carries a bearer token.
		{"no browser headers", nil, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// What is asserted is the gate's verdict, not the handler's answer:
			// a want of zero means "anything but the refusal".
			r := signedInRequest(t, srv, http.MethodPost, "/coddy/sessions/abc/workspace")
			r.Host = "example.com"
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			switch {
			case tc.want != 0 && w.Code != tc.want:
				t.Fatalf("status %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			case tc.want == 0 && (w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized):
				t.Fatalf("the gate refused a same-origin write: status %d (%s)", w.Code, w.Body.String())
			}
		})
	}
}

func TestCookieReadFromAnotherSiteIsStillAllowed(t *testing.T) {
	// A GET changes nothing, and refusing one would break an <img> or a link
	// without protecting anything.
	srv := newLoginServer(t, configuredLogin(t))
	r := signedInRequest(t, srv, http.MethodGet, "/v1/models")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (body %s)", w.Code, w.Body.String())
	}
}

func TestCrossSiteSignInAndSignOutAreRefused(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))

	r := loginRequest(loginTestUser, loginTestPassword)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-site sign-in: status %d, want 403", w.Code)
	}

	out := httptest.NewRequest(http.MethodPost, "/coddy/auth/logout", nil)
	out.Header.Set("Sec-Fetch-Site", "cross-site")
	wo := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wo, out)
	if wo.Code != http.StatusForbidden {
		t.Fatalf("cross-site sign-out: status %d, want 403", wo.Code)
	}
}

func TestBearerClientIsNeverCSRFChecked(t *testing.T) {
	// The check exists because a browser attaches cookies on its own. It never
	// attaches a bearer token, so a token client must not be held to it - that
	// is what keeps `coddy --remote`, an editor and a relay working.
	login := configuredLogin(t)
	srv := newLoginServer(t, login)
	srv.SetExtraAuthTokens([]string{"relay-secret"})

	r := httptest.NewRequest(http.MethodPost, "/coddy/sessions/abc/workspace", strings.NewReader(`{"path":"/tmp"}`))
	r.Header.Set("Authorization", "Bearer relay-secret")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", "https://somewhere.example")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code == http.StatusForbidden {
		t.Fatal("a bearer client was refused as cross-site")
	}
}

func TestSignOutIsIdempotentAndClearsTheCookie(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	c := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))

	out := httptest.NewRequest(http.MethodPost, "/coddy/auth/logout", nil)
	out.Header.Set("Sec-Fetch-Site", "same-origin")
	out.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, out)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-out: status %d, want 200", w.Code)
	}
	cleared := sessionCookieOf(t, w)
	if cleared.MaxAge >= 0 {
		t.Fatalf("sign-out did not expire the cookie: MaxAge=%d", cleared.MaxAge)
	}
	// The same stale cookie now opens nothing.
	stale := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	stale.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	ws := httptest.NewRecorder()
	srv.Handler().ServeHTTP(ws, stale)
	if ws.Code != http.StatusUnauthorized {
		t.Fatalf("a signed-out cookie still opens the API: status %d", ws.Code)
	}
	// A second sign-out, from a page that did not notice, is not an error.
	again := httptest.NewRequest(http.MethodPost, "/coddy/auth/logout", nil)
	again.Header.Set("Sec-Fetch-Site", "same-origin")
	wa := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wa, again)
	if wa.Code != http.StatusOK {
		t.Fatalf("a sign-out with no cookie: status %d, want 200", wa.Code)
	}
}

func TestSessionExpiresWithItsTTL(t *testing.T) {
	login := configuredLogin(t)
	login.SessionTTLHours = 1
	srv := newLoginServer(t, login)
	now := time.Now()
	srv.sessions.Now = func() time.Time { return now }

	c := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))
	call := func() int {
		r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if got := call(); got != http.StatusOK {
		t.Fatalf("a fresh session: status %d, want 200", got)
	}
	now = now.Add(61 * time.Minute)
	if got := call(); got != http.StatusUnauthorized {
		t.Fatalf("an expired session: status %d, want 401", got)
	}
}

func TestGarbageCookieIsRefused(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	for _, value := range []string{"not-a-token", "", strings.Repeat("A", 512)} {
		r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName(r), Value: value})
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("cookie %q: status %d, want 401", value, w.Code)
		}
	}
}

func TestRotatingThePasswordEndsLiveSessions(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	c := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))

	next := *srv.activeCfg()
	next.HTTPServer.Login.PasswordHash = loginTestHash(t, "a-new-password")
	srv.ReplaceConfig(&next)

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a session opened with the old password survived the rotation: status %d", w.Code)
	}
}

func TestRenamingTheAccountEndsLiveSessions(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	c := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))

	next := *srv.activeCfg()
	next.HTTPServer.Login.User = "someone-else"
	srv.ReplaceConfig(&next)

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a session of the old account survived the rename: status %d", w.Code)
	}
}

func TestUnrelatedConfigReloadKeepsSessions(t *testing.T) {
	// Saving settings from the browser reloads the config. Signing the operator
	// out of the page they just saved from would be its own bug report.
	srv := newLoginServer(t, configuredLogin(t))
	c := sessionCookieOf(t, signIn(t, srv, loginTestUser, loginTestPassword))

	next := *srv.activeCfg()
	next.HTTPServer.PublicDocs = true
	srv.ReplaceConfig(&next)

	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("an unrelated config save signed the browser out: status %d", w.Code)
	}
}

func TestTurningTheLoginOnClosesTheGateImmediately(t *testing.T) {
	srv := newLoginServer(t, config.HTTPLoginConfig{})
	open := httptest.NewRecorder()
	srv.Handler().ServeHTTP(open, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if open.Code != http.StatusOK {
		t.Fatalf("before the account: status %d, want 200", open.Code)
	}

	next := *srv.activeCfg()
	next.HTTPServer.Login = configuredLogin(t)
	srv.ReplaceConfig(&next)

	closed := httptest.NewRecorder()
	srv.Handler().ServeHTTP(closed, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if closed.Code != http.StatusUnauthorized {
		t.Fatalf("after the account: status %d, want 401", closed.Code)
	}
}

func TestEnvironmentAccountWinsOverTheFile(t *testing.T) {
	// The same precedence .env has everywhere else: the environment is the
	// operator's override of what the file says.
	srv := newLoginServer(t, configuredLogin(t))
	if err := srv.SetExtraLogin("envuser", "env-pass"); err != nil {
		t.Fatalf("set env login: %v", err)
	}
	if w := signIn(t, srv, "envuser", "env-pass"); w.Code != http.StatusOK {
		t.Fatalf("the environment account cannot sign in: status %d (%s)", w.Code, w.Body.String())
	}
	if w := signIn(t, srv, loginTestUser, loginTestPassword); w.Code != http.StatusUnauthorized {
		t.Fatalf("the file account still signs in while the environment overrides it: status %d", w.Code)
	}
	pol := srv.loginPolicyNow()
	if pol.account.source != loginSourceEnv {
		t.Fatalf("login source = %q, want %q", pol.account.source, loginSourceEnv)
	}
}

func TestExplicitDisableBeatsEnvironmentCredentials(t *testing.T) {
	off := false
	srv := newLoginServer(t, config.HTTPLoginConfig{Enabled: &off})
	if err := srv.SetExtraLogin("envuser", "env-pass"); err != nil {
		t.Fatalf("set env login: %v", err)
	}
	if srv.loginPolicyNow().enabled {
		t.Fatal("enable: false did not beat the environment variables")
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("the API is gated although the form was switched off: status %d", w.Code)
	}
}

func TestSetExtraLoginIgnoresHalfAnAccount(t *testing.T) {
	srv := newLoginServer(t, config.HTTPLoginConfig{})
	for _, tc := range [][2]string{{"user", ""}, {"", "password"}, {"", ""}, {"   ", "password"}} {
		if err := srv.SetExtraLogin(tc[0], tc[1]); err != nil {
			t.Fatalf("SetExtraLogin(%q, %q): %v", tc[0], tc[1], err)
		}
		if srv.loginPolicyNow().enabled {
			t.Fatalf("SetExtraLogin(%q, %q) enabled a form nobody can pass", tc[0], tc[1])
		}
	}
}

func TestSetExtraLoginDoesNotKeepThePlaintext(t *testing.T) {
	srv := newLoginServer(t, config.HTTPLoginConfig{})
	if err := srv.SetExtraLogin("envuser", "env-pass"); err != nil {
		t.Fatalf("set env login: %v", err)
	}
	if strings.Contains(srv.envLoginHash, "env-pass") {
		t.Fatal("the plaintext password is still in the server")
	}
	if !webauth.IsHash(srv.envLoginHash) {
		t.Fatalf("the environment password was not hashed: %q", srv.envLoginHash)
	}
}

func TestAuthMeReportsABearerClient(t *testing.T) {
	srv := newLoginServer(t, config.HTTPLoginConfig{})
	srv.SetExtraAuthTokens([]string{"relay-secret"})

	r := httptest.NewRequest(http.MethodGet, "/coddy/auth/me", nil)
	r.Header.Set("Authorization", "Bearer relay-secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var got map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if authed, _ := got["authenticated"].(bool); !authed {
		t.Fatalf("a valid bearer is not reported as authenticated: %s", w.Body.String())
	}
	if required, _ := got["login_required"].(bool); required {
		t.Fatal("a token-only server asks for a sign-in form")
	}
	if authRequired, _ := got["auth_required"].(bool); !authRequired {
		t.Fatal("a token-only server does not report that a credential is needed")
	}
}

func TestAuthMeNeverLeaksTheAccount(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/coddy/auth/me", nil))
	body := w.Body.String()
	if strings.Contains(body, loginTestUser) {
		t.Fatalf("the public sign-in state names the account: %s", body)
	}
	if strings.Contains(body, srv.activeCfg().HTTPServer.Login.PasswordHash) {
		t.Fatalf("the public sign-in state carries the password hash: %s", body)
	}
}

func TestLoginThrottleGrowsPerAddressAndSparesLoopback(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	srv.loginThrottle.Base = time.Millisecond
	srv.loginThrottle.Max = 4 * time.Millisecond

	// httptest requests come from 192.0.2.1 by default, which is not loopback.
	const remote = "203.0.113.8:40001"
	fail := func(addr string) {
		r := loginRequest(loginTestUser, "wrong")
		r.RemoteAddr = addr
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", w.Code)
		}
	}
	fail(remote)
	fail(remote)
	if d := srv.loginThrottle.Delay(remote); d <= 0 {
		t.Fatal("repeated failures from one address cost nothing")
	}

	for i := 0; i < 5; i++ {
		fail("127.0.0.1:40002")
	}
	if d := srv.loginThrottle.Delay("127.0.0.1:40002"); d != 0 {
		t.Fatalf("the machine locked itself out of its own agent: %v", d)
	}

	// A correct password clears the address, so one typo does not tax the rest
	// of the day.
	ok := loginRequest(loginTestUser, loginTestPassword)
	ok.RemoteAddr = remote
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, ok)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	if d := srv.loginThrottle.Delay(remote); d != 0 {
		t.Fatalf("a successful sign-in left a penalty of %v", d)
	}
}

func TestConfigEndpointReportsTheLoginSource(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	read := func() map[string]interface{} {
		r := signedInRequest(t, srv, http.MethodGet, "/coddy/config")
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /coddy/config: status %d (%s)", w.Code, w.Body.String())
		}
		var out map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		hs, _ := out["httpserver"].(map[string]interface{})
		return hs
	}
	hs := read()
	if source, _ := hs["login_source"].(string); source != loginSourceConfig {
		t.Fatalf("login_source = %q, want %q", source, loginSourceConfig)
	}
	if configured, _ := hs["auth_configured"].(bool); configured {
		t.Fatal("auth_configured must report a bearer token, and there is none")
	}

	if err := srv.SetExtraLogin("envuser", "env-pass"); err != nil {
		t.Fatalf("set env login: %v", err)
	}
	// The environment account signs in, and the document still describes the
	// file: a save writes back what is in the file, never what is in the
	// environment.
	c := sessionCookieOf(t, signIn(t, srv, "envuser", "env-pass"))
	r := httptest.NewRequest(http.MethodGet, "/coddy/config", nil)
	r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	hs, _ = out["httpserver"].(map[string]interface{})
	if source, _ := hs["login_source"].(string); source != loginSourceEnv {
		t.Fatalf("login_source = %q, want %q", source, loginSourceEnv)
	}
	login, _ := hs["login"].(map[string]interface{})
	if user, _ := login["user"].(string); user == "envuser" {
		t.Fatal("the config document carries the environment account, so a save would write it into the file")
	}
}

func TestAuthRoutesArePublicAndTheRestIsNot(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	public := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/coddy/auth/me", nil),
		loginRequest(loginTestUser, loginTestPassword),
		httptest.NewRequest(http.MethodPost, "/coddy/auth/logout", nil),
	}
	for _, r := range public {
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s answered %d: it is the way through the gate, so it cannot be behind it (%s)",
				r.Method, r.URL.Path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/v1/models", "/coddy/sessions", "/coddy/config", "/coddy/skills", "/openapi.yaml", "/docs/"} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s is readable without signing in: status %d", path, w.Code)
		}
	}
}

func TestPublicDocsStayPublicBehindTheForm(t *testing.T) {
	login := configuredLogin(t)
	srv := newLoginServer(t, login)
	next := *srv.activeCfg()
	next.HTTPServer.PublicDocs = true
	srv.ReplaceConfig(&next)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("public_docs did not survive the sign-in form: status %d", w.Code)
	}
}

func TestIsSameOriginRequest(t *testing.T) {
	req := func(headers map[string]string, host string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/x", nil)
		r.Host = host
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name    string
		headers map[string]string
		host    string
		want    bool
	}{
		{"no labels at all", nil, "box:12345", false},
		{"sec-fetch same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, "box:12345", true},
		{"sec-fetch none", map[string]string{"Sec-Fetch-Site": "none"}, "box:12345", true},
		{"sec-fetch cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, "box:12345", false},
		{"sec-fetch beats a matching origin", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "http://box:12345"}, "box:12345", false},
		{"origin matches", map[string]string{"Origin": "http://box:12345"}, "box:12345", true},
		{"origin matches over https", map[string]string{"Origin": "https://box:12345"}, "box:12345", true},
		{"origin port differs", map[string]string{"Origin": "http://box:12346"}, "box:12345", false},
		{"origin host differs", map[string]string{"Origin": "http://evil.example"}, "box:12345", false},
		{"origin null", map[string]string{"Origin": "null"}, "box:12345", false},
		{"origin case-insensitive", map[string]string{"Origin": "http://BOX:12345"}, "box:12345", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSameOriginRequest(req(tc.headers, tc.host)); got != tc.want {
				t.Fatalf("isSameOriginRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsStateChanging(t *testing.T) {
	safe := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	unsafe := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	for _, m := range safe {
		if isStateChanging(httptest.NewRequest(m, "/x", nil)) {
			t.Fatalf("%s is a safe method", m)
		}
	}
	for _, m := range unsafe {
		if !isStateChanging(httptest.NewRequest(m, "/x", nil)) {
			t.Fatalf("%s changes state", m)
		}
	}
}

func TestTheGateIsNotFooledByTheShapeOfAURL(t *testing.T) {
	// The gate classifies the pattern the mux matched, not the string a client
	// typed, so none of these reaches anything behind it. What each one gets -
	// 401, 404 or a redirect - is the mux's business; what matters is that it
	// is never 200.
	srv := newLoginServer(t, configuredLogin(t))
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/coddy/auth/login"},          // the form, wrong method
		{http.MethodPost, "/coddy/auth/me"},            // the state, wrong method
		{http.MethodGet, "/coddy/auth/login/"},         // a trailing slash
		{http.MethodGet, "/coddy/auth/"},               // the prefix alone
		{http.MethodGet, "/coddy/auth/me/../sessions"}, // a traversal in the path
		{http.MethodGet, "/coddy/sessions/"},           // a protected route, slashed
		{http.MethodHead, "/coddy/config"},             // a protected route, HEAD
		{http.MethodGet, "/coddy/config?x=/coddy/auth/me"},
		{http.MethodGet, "/v1/models/"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code == http.StatusOK {
				t.Fatalf("%s %s answered 200 without a credential: %s", tc.method, tc.path, w.Body.String())
			}
			// A redirect is only safe when what it points at is itself gated, so
			// the target is followed and held to the same rule.
			if loc := w.Header().Get("Location"); loc != "" {
				wf := httptest.NewRecorder()
				srv.Handler().ServeHTTP(wf, httptest.NewRequest(tc.method, loc, nil))
				if wf.Code == http.StatusOK {
					t.Fatalf("%s %s redirects to %s, which answers 200 without a credential: %s",
						tc.method, tc.path, loc, wf.Body.String())
				}
			}
		})
	}
}

func TestAuthRoutesStayPublicOnAnOpenServer(t *testing.T) {
	// With nothing configured the three routes still answer, because the SPA
	// asks on every boot whether it needs a form.
	srv := newLoginServer(t, config.HTTPLoginConfig{})
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/coddy/auth/me", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /coddy/auth/me on an open server: %d", w.Code)
	}
	out := httptest.NewRequest(http.MethodPost, "/coddy/auth/logout", nil)
	out.Header.Set("Sec-Fetch-Site", "same-origin")
	wo := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wo, out)
	if wo.Code != http.StatusOK {
		t.Fatalf("POST /coddy/auth/logout on an open server: %d", wo.Code)
	}
}

func TestSessionCookieIsScopedToTheOrigin(t *testing.T) {
	// Cookies are not scoped by port, so two Coddy servers on one host would
	// otherwise overwrite each other's session on every sign-in - a relay and
	// the node behind it, or a production one beside the one being tried out.
	srv := newLoginServer(t, configuredLogin(t))

	signInAt := func(host string) string {
		r := loginRequest(loginTestUser, loginTestPassword)
		r.Host = host
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("sign-in at %s: %d", host, w.Code)
		}
		return sessionCookieOf(t, w).Name
	}
	first := signInAt("box:12345")
	second := signInAt("box:12346")
	if first == second {
		t.Fatalf("both servers name the cookie %q, so one signs the other out", first)
	}
	if !strings.HasPrefix(first, sessionCookieBaseName) || !strings.HasPrefix(second, sessionCookieBaseName) {
		t.Fatalf("cookie names lost their common prefix: %q, %q", first, second)
	}
	// The name is stable for one origin, or a reload would look like a sign-out.
	if again := signInAt("box:12345"); again != first {
		t.Fatalf("the same origin got two names: %q then %q", first, again)
	}
	if upper := signInAt("BOX:12345"); upper != first {
		t.Fatalf("the host's case changed the cookie name: %q vs %q", upper, first)
	}
}

func TestASessionOfAnotherOriginDoesNotOpenThisOne(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	other := loginRequest(loginTestUser, loginTestPassword)
	other.Host = "box:12346"
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, other)
	c := sessionCookieOf(t, w)

	// The value is live, but it is filed under the other origin's name, so a
	// request addressed here does not present it.
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Host = "box:12345"
	r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	wr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wr, r)
	if wr.Code != http.StatusUnauthorized {
		t.Fatalf("a cookie named for another origin opened this one: %d", wr.Code)
	}
}

func TestLoginThrottleFollowsTheClientBehindAProxy(t *testing.T) {
	// The deployment the documentation recommends puts a TLS-terminating proxy
	// in front of a loopback listener, which would otherwise hand every request
	// in the world the exemption meant for the operator's own machine.
	srv := newLoginServer(t, configuredLogin(t))
	srv.loginThrottle.Base = time.Millisecond
	srv.loginThrottle.Max = 4 * time.Millisecond

	fail := func(headers map[string]string) {
		r := loginRequest(loginTestUser, "wrong")
		r.RemoteAddr = "127.0.0.1:40000" // the proxy, on this machine
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", w.Code)
		}
	}
	fail(map[string]string{"X-Forwarded-For": "203.0.113.30, 10.0.0.2"})
	fail(map[string]string{"X-Forwarded-For": "203.0.113.30, 10.0.0.2"})
	if d := srv.loginThrottle.Delay("203.0.113.30"); d <= 0 {
		t.Fatal("a client behind a proxy was never throttled")
	}
	// A different client through the same proxy is a different row.
	if d := srv.loginThrottle.Delay("203.0.113.31"); d != 0 {
		t.Fatalf("one client's failures cost another %v", d)
	}
	// The operator at the machine, with no proxy in the way, still is not.
	fail(nil)
	fail(nil)
	if d := srv.loginThrottle.Delay("127.0.0.1"); d != 0 {
		t.Fatalf("the machine throttled itself by %v", d)
	}
}
