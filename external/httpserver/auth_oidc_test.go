//go:build http

package httpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

const testOIDCOrigin = "https://coddy.example"
const testLogoutURL = "https://idp.example/logout"

type oidcFixture struct {
	state    *loginFeatureState
	provider *httptest.Server
	calls    atomic.Int64
	signer   jose.Signer
	signAs   jose.Signer
	exchange func(*http.Request)
	claims   func(map[string]any)
}

func newOIDCFixture(t testing.TB) *oidcFixture {
	t.Helper()
	f := &oidcFixture{state: &loginFeatureState{}}
	t.Cleanup(f.state.close)
	if err := f.state.reset(); err != nil {
		t.Fatal(err)
	}
	hash, err := webauth.HashPasswordWith("unused-password", loginTestHashParams)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.signer, err = jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: jose.JSONWebKey{Key: key, KeyID: "fake"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.provider = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			jsonReply(w, map[string]any{"issuer": f.provider.URL, "authorization_endpoint": f.provider.URL + "/authorize", "token_endpoint": f.provider.URL + "/token", "jwks_uri": f.provider.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"ES256"}})
		case "/jwks":
			jsonReply(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "fake", Algorithm: "ES256"}}})
		case "/token":
			if f.exchange != nil {
				f.exchange(r)
			}
			client, secret, ok := r.BasicAuth()
			v := r.FormValue("code_verifier")
			code := strings.Split(r.FormValue("code"), ".")
			if !ok || client != "client" || secret != "test-secret" || r.FormValue("redirect_uri") != testOIDCOrigin+oidcCallbackPath || len(code) != 3 || oauth2.S256ChallengeFromVerifier(v) != code[1] || r.FormValue("grant_type") != "authorization_code" {
				w.WriteHeader(400)
				return
			}
			claims := map[string]any{"iss": f.provider.URL, "sub": code[2], "aud": "client", "azp": "client", "nonce": code[0], "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "roles": []string{"operator"}}
			if f.claims != nil {
				f.claims(claims)
			}
			signer := f.signer
			if f.signAs != nil {
				signer = f.signAs
			}
			raw, err := signOIDCTestClaims(signer, claims)
			if err != nil {
				w.WriteHeader(500)
				return
			}
			jsonReply(w, map[string]string{"id_token": raw, "access_token": "token-not-to-store", "token_type": "Bearer"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.provider.Close)
	cfg := config.HTTPLoginConfig{User: "legacy", PasswordHash: hash, TOTPSecret: "JBSWY3DPEHPK3PXP", LogoutURL: testLogoutURL, OIDC: config.HTTPOIDCConfig{Enable: true, Issuer: f.provider.URL, ClientID: "client", ClientSecret: "test-secret", RedirectURL: testOIDCOrigin + oidcCallbackPath, RequiredRole: "operator"}}
	if err := f.state.boot(cfg, LoginCredentials{}); err != nil {
		t.Fatal(err)
	}
	f.state.srv.external.client = f.provider.Client()
	f.state.srv.SetExtraAuthTokens([]string{"machine-secret"})
	return f
}

func signOIDCTestClaims(signer jose.Signer, claims map[string]any) (string, error) {
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	sig, err := signer.Sign(raw)
	if err != nil {
		return "", err
	}
	return sig.CompactSerialize()
}

func jsonReply(w http.ResponseWriter, v any) { writeJSON(w, http.StatusOK, v) }

func (f *oidcFixture) request(method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, testOIDCOrigin+path, strings.NewReader(body))
	r.Header.Set("Origin", testOIDCOrigin)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	f.state.srv.Handler().ServeHTTP(w, r)
	return w
}

func beginOIDCTest(t testing.TB, f *oidcFixture, sub string, cookies ...*http.Cookie) (string, *http.Cookie) {
	t.Helper()
	w := f.request("GET", oidcStartPath, "", cookies...)
	if w.Code != 303 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	var flow *http.Cookie
	if location := w.Header().Get("Location"); location == oidcStartPath+"?"+oidcBindingQuery {
		for _, c := range w.Result().Cookies() {
			if strings.HasPrefix(c.Name, oidcBindingCookiePrefix) {
				flow = c
				break
			}
		}
		if flow == nil || !flow.Secure || !flow.HttpOnly || flow.SameSite != http.SameSiteLaxMode || flow.MaxAge != 0 {
			t.Fatal("missing browser-session binding")
		}
		// An existing copy of the same cookie is replaced as it would be by the browser.
		next := []*http.Cookie{flow}
		for _, c := range cookies {
			if c.Name != flow.Name {
				next = append(next, c)
			}
		}
		w = f.request("GET", location, "", next...)
		if w.Code != 303 {
			t.Fatalf("bound start: %d %s", w.Code, w.Body.String())
		}
	} else {
		for _, c := range cookies {
			if strings.HasPrefix(c.Name, oidcBindingCookiePrefix) {
				flow = c
				break
			}
		}
	}
	if flow == nil {
		t.Fatal("no binding for the callback")
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host == "" || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("redirect_uri") != testOIDCOrigin+oidcCallbackPath {
		t.Fatal("start lacks OIDC/PKCE parameters")
	}

	code := q.Get("nonce") + "." + q.Get("code_challenge") + "." + sub
	return oidcCallbackPath + "?" + url.Values{"state": {q.Get("state")}, "code": {code}}.Encode(), flow
}

func findAuthCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func finishOIDCTest(t testing.TB, f *oidcFixture, path string, flow *http.Cookie) *http.Cookie {
	t.Helper()
	w := f.request("GET", path, "", flow)
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	c := findAuthCookie(w, oidcSessionCookie)
	if c == nil || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Value == flow.Value || c.MaxAge > 900 {
		t.Fatal("invalid authenticated cookie")
	}
	return c
}

func TestOIDCRejectsInvalidCallbacks(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"role-case":    func(m map[string]any) { m["roles"] = []string{"Operator"} },
		"role-missing": func(m map[string]any) { delete(m, "roles") },
		"role-scalar":  func(m map[string]any) { m["roles"] = "operator" },
		"role-null":    func(m map[string]any) { m["roles"] = []any{"operator", nil} },
		"role-mixed":   func(m map[string]any) { m["roles"] = []any{"operator", 7} },
		"role-nested":  func(m map[string]any) { m["roles"] = [][]string{{"operator"}} },
		"nonce":        func(m map[string]any) { m["nonce"] = "wrong" },
		"issuer":       func(m map[string]any) { m["iss"] = "https://different.example" },
		"audience":     func(m map[string]any) { m["aud"] = "someone-else" },
		"missing-azp":  func(m map[string]any) { m["aud"] = []string{"client", "someone-else"}; delete(m, "azp") },
		"wrong-azp":    func(m map[string]any) { m["azp"] = "someone-else" },
		"subject":      func(m map[string]any) { m["sub"] = "" },
		"expired":      func(m map[string]any) { m["exp"] = time.Now().Add(-time.Hour).Unix() },
	} {
		t.Run(name, func(t *testing.T) {
			f := newOIDCFixture(t)
			f.claims = mutate
			path, flow := beginOIDCTest(t, f, "one")
			w := f.request("GET", path, "", flow)
			if w.Code != 401 || findAuthCookie(w, oidcSessionCookie) != nil {
				t.Fatalf("invalid identity accepted: %d", w.Code)
			}
		})
	}
}

func TestOIDCStatePKCEAndPublicDesktopHeader(t *testing.T) {
	f := newOIDCFixture(t)
	path, flow := beginOIDCTest(t, f, "one")
	_, other := beginOIDCTest(t, f, "two")
	for _, c := range []*http.Cookie{nil, other} {
		var cookies []*http.Cookie
		if c != nil {
			cookies = append(cookies, c)
		}
		if w := f.request("GET", path, "", cookies...); w.Code != 400 {
			t.Fatalf("unbound callback: %d", w.Code)
		}
	}
	u, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	state := q.Get("state")
	q.Set("state", "wrong")
	u.RawQuery = q.Encode()
	if w := f.request("GET", u.String(), "", flow); w.Code != 400 {
		t.Fatalf("wrong state: %d", w.Code)
	}
	q = u.Query()
	q.Set("state", state)
	q.Set("code", "invalid.badpkce.one")
	u.RawQuery = q.Encode()
	if w := f.request("GET", u.String(), "", flow); w.Code != 401 {
		t.Fatalf("bad PKCE: %d", w.Code)
	}
	if w := f.request("GET", path, "", flow); w.Code != 400 {
		t.Fatalf("callback replay: %d", w.Code)
	}
	req, err := http.NewRequest("GET", f.state.ts.URL+"/coddy/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(DesktopNoAuthHeader, "1")
	res, err := f.state.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("public spoof: %d", res.StatusCode)
	}
	// Only a separately configured private handler recognizes the same header.
	f.state.srv.SetNoAuthHeader(DesktopNoAuthHeader)
	w := httptest.NewRecorder()
	f.state.srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("private bridge: %d", w.Code)
	}
}

func TestOIDCFencesBothCommitOrderings(t *testing.T) {
	for _, policy := range []bool{false, true} {
		for _, commitFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("policy=%v/commitFirst=%v", policy, commitFirst), func(t *testing.T) {
				f := newOIDCFixture(t)
				arrived, release := make(chan struct{}), make(chan struct{})
				f.exchange = func(r *http.Request) {
					close(arrived)
					select {
					case <-release:
					case <-r.Context().Done():
					}
				}
				path, flow := beginOIDCTest(t, f, "one")
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- f.request("GET", path, "", flow) }()
				select {
				case <-arrived:
				case <-time.After(3 * time.Second):
					close(release)
					t.Fatal("callback did not reach issuer")
				}
				var issued *http.Cookie
				if commitFirst {
					close(release)
					w := <-done
					issued = findAuthCookie(w, oidcSessionCookie)
					if issued == nil {
						t.Fatal("callback did not commit")
					}
				}
				if policy {
					c := *f.state.srv.activeCfg()
					c.HTTPServer.Login.OIDC.RequiredRole = "new-role"
					f.state.srv.ReplaceConfig(&c)
				} else {
					cookies := []*http.Cookie{flow}
					if issued != nil {
						cookies = append(cookies, issued)
					}
					if w := f.request("POST", "/coddy/auth/logout", "", cookies...); w.Code != 200 || w.Header().Get(logoutContinueHeader) != "1" {
						t.Errorf("logout failed: %d", w.Code)
					}
				}
				if !commitFirst {
					close(release)
					w := <-done
					if w.Code != 401 || findAuthCookie(w, oidcSessionCookie) != nil {
						t.Fatal("delayed callback committed")
					}
				}
				cookies := []*http.Cookie{flow}
				if issued != nil {
					cookies = append(cookies, issued)
				}
				if w := f.request("GET", "/coddy/sessions", "", cookies...); w.Code != 401 {
					t.Fatalf("revoked session: %d", w.Code)
				}
			})
		}
	}
}

func TestOIDCLogoutClosesAcceptedResponse(t *testing.T) {
	f := newOIDCFixture(t)
	path, flow := beginOIDCTest(t, f, "one")
	cookie := finishOIDCTest(t, f, path, flow)
	release, finished := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	f.state.srv.mux.HandleFunc("POST /oidc-test-turn", func(w http.ResponseWriter, r *http.Request) {
		turn := acceptedTurnContext(r.Context())
		_, _ = io.WriteString(w, "ready\n")
		w.(http.Flusher).Flush()
		<-release
		if turn.Err() != nil {
			t.Error("accepted turn was canceled")
		}
		_, _ = io.WriteString(w, "forbidden output\n")
		close(finished)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "POST", f.state.ts.URL+"/oidc-test-turn", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "coddy.example"
	r.AddCookie(cookie)
	r.Header.Set("Origin", testOIDCOrigin)
	res, err := f.state.client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	ready := make([]byte, 6)
	if _, err := io.ReadFull(res.Body, ready); err != nil || string(ready) != "ready\n" {
		t.Fatalf("stream did not open: %q %v", ready, err)
	}
	if w := f.request("POST", "/coddy/auth/logout", "", cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	body, err := io.ReadAll(res.Body)
	if len(body) != 0 || ctx.Err() != nil || err == nil {
		t.Fatalf("stream was not aborted promptly: received %q, error %v", body, err)
	}
	select {
	case <-finished:
		t.Fatal("turn finished before barrier release")
	default:
	}
	close(release)
	released = true
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("accepted turn did not finish")
	}
}

func TestOIDCRejectsSignatureAndAlgorithm(t *testing.T) {
	for _, algo := range []jose.SignatureAlgorithm{jose.ES256, jose.HS256} {
		t.Run(string(algo), func(t *testing.T) {
			f := newOIDCFixture(t)
			var key any = []byte("01234567890123456789012345678901")
			if algo == jose.ES256 {
				k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				key = jose.JSONWebKey{Key: k, KeyID: "fake"}
			}
			var err error
			f.signAs, err = jose.NewSigner(jose.SigningKey{Algorithm: algo, Key: key}, nil)
			if err != nil {
				t.Fatal(err)
			}
			p, b := beginOIDCTest(t, f, "one")
			if w := f.request("GET", p, "", b); w.Code != 401 || findAuthCookie(w, oidcSessionCookie) != nil {
				t.Fatalf("untrusted token: %d", w.Code)
			}
		})
	}
}

func TestOIDCLogoutContinuationFences(t *testing.T) {
	for _, kind := range []string{"binding", "expiry", "policy"} {
		t.Run(kind, func(t *testing.T) {
			f := newOIDCFixture(t)
			now := time.Now()
			f.state.srv.sessions.Now = func() time.Time { return now }
			p, b := beginOIDCTest(t, f, "one")
			session := finishOIDCTest(t, f, p, b)
			logout := f.request("POST", "/coddy/auth/logout", "", session)
			ticket := findAuthCookie(logout, logoutTicketCookie)
			browser := findAuthCookie(logout, logoutBrowserCookie)
			if ticket == nil || browser == nil || ticket.Path != logoutContinuePath || !ticket.Secure || !ticket.HttpOnly || ticket.SameSite != http.SameSiteStrictMode {
				t.Fatal("missing protected continuation")
			}
			switch kind {
			case "binding":
				browser.Value = authOpaque()
			case "expiry":
				now = now.Add(logoutTicketTTL)
			case "policy":
				f.state.replaceConfig(func(c *config.Config) { c.HTTPServer.Login.LogoutURL = "https://different.example/logout" })
			}
			r := f.request("GET", logoutContinuePath+"?next=https://attacker.example", "", ticket, browser)
			if r.Code != 303 || r.Header().Get("Location") != "/" || findAuthCookie(r, logoutTicketCookie).MaxAge != -1 {
				t.Fatalf("invalid continuation: %d %s", r.Code, r.Header().Get("Location"))
			}
		})
	}
	f := newOIDCFixture(t)
	r := httptest.NewRequest("POST", testOIDCOrigin+"/coddy/auth/logout", nil)
	r.Header.Set("Origin", testOIDCOrigin)
	r.Header.Set("Authorization", "Bearer machine-secret")
	w := httptest.NewRecorder()
	f.state.srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get(logoutContinueHeader) != "" {
		t.Fatal("bearer-only request obtained continuation")
	}
}

func TestOIDCInvalidConfigStaysClosed(t *testing.T) {
	f := newOIDCFixture(t)
	f.state.replaceConfig(func(c *config.Config) {
		c.HTTPServer.Login.OIDC.RedirectURL = "https://coddy.example/wrong?test-secret"
		c.HTTPServer.AllowInsecure = true
	})
	cfg := f.state.srv.activeCfg()
	if err := validateExternalLogin(cfg); err == nil || strings.Contains(err.Error(), "test-secret") {
		t.Fatal("startup validation accepted or disclosed invalid redirect URL")
	}
	f.state.mgr.ReplaceConfig(cfg)
	s, cleanup, err := configuredServer(context.Background(), Options{Cfg: cfg, Mgr: f.state.mgr, Log: slog.New(slog.DiscardHandler)})
	if err == nil {
		cleanup()
		t.Fatalf("startup accepted malformed configuration: %v", s != nil)
	}
	if w := f.request("GET", "/coddy/sessions", "", &http.Cookie{Name: sessionCookieName(httptest.NewRequest("GET", testOIDCOrigin, nil)), Value: authOpaque(), Secure: true, HttpOnly: true}); w.Code != 401 {
		t.Fatalf("invalid config fell open: %d", w.Code)
	}
	if w := f.request("POST", "/coddy/auth/login", `{"user":"legacy","password":"unused-password"}`); w.Code != 403 {
		t.Fatalf("invalid OIDC fell back to password: %d", w.Code)
	}
}

func TestOIDCLogoutFencesReauthenticationUsingSessionBinding(t *testing.T) {
	f := newOIDCFixture(t)
	now := f.state.srv.authNow()
	f.state.srv.sessions.Now = func() time.Time { return now }
	arrived, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var pause atomic.Bool
	f.exchange = func(r *http.Request) {
		if pause.Load() {
			close(arrived)
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
	}
	p, original := beginOIDCTest(t, f, "first")
	session := finishOIDCTest(t, f, p, original)
	p, other := beginOIDCTest(t, f, "other")
	otherSession := finishOIDCTest(t, f, p, other)
	now = now.Add(oidcFlowTTL + time.Second)
	// The old short-lived flow cookie would have expired. Starting with only
	// the authenticated session must re-establish that same browser binding.
	p, binding := beginOIDCTest(t, f, "first", session)
	if binding.Value != original.Value {
		t.Fatal("reauthentication did not reuse the session binding")
	}
	pause.Store(true)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.request("GET", p, "", binding) }()
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("callback did not reach exchange")
	}
	if w := f.request("POST", "/coddy/auth/logout", "", session); w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
	// No flow for this binding remains while the callback is paused, but the
	// other authenticated browser is still authorized.
	f.state.srv.external.mu.Lock()
	_, pending := f.state.srv.external.flows[binding.Value]
	f.state.srv.external.mu.Unlock()
	if pending {
		t.Error("logout left the reauthentication flow pending")
	}
	if w := f.request("GET", "/coddy/sessions", "", otherSession); w.Code != 200 {
		t.Errorf("other browser: %d", w.Code)
	}
	// Unblock the callback without closing release twice on an early failure.
	release <- struct{}{}
	if w := <-done; w.Code != 401 || findAuthCookie(w, oidcSessionCookie) != nil {
		t.Fatal("callback issued a session after logout")
	}
}

func TestOIDCPreflightDoesNotAdmitFlowBeforeBindingIsEchoed(t *testing.T) {
	f := newOIDCFixture(t)
	a := f.request("GET", oidcStartPath, "")
	b := f.request("GET", oidcStartPath, "")
	start := oidcStartPath + "?" + oidcBindingQuery
	if a.Code != 303 || b.Code != 303 || a.Header().Get("Location") != start || b.Header().Get("Location") != start {
		t.Fatal("expected same-origin preflight")
	}
	ac, bc := a.Result().Cookies(), b.Result().Cookies()
	if len(ac) != 1 || len(bc) != 1 || ac[0].Name == bc[0].Name {
		t.Fatal("parallel preflights overwrite a binding")
	}
	if w := f.request("GET", start, ""); w.Code != 400 {
		t.Fatalf("missing echoed binding: %d", w.Code)
	}
	f.state.srv.external.mu.Lock()
	pending := len(f.state.srv.external.flows)
	f.state.srv.external.mu.Unlock()
	if pending != 0 || f.calls.Load() != 0 {
		t.Fatal("unbound preflight admitted work")
	}
}
