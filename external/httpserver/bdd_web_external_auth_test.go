//go:build http

package httpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
	"github.com/cucumber/godog"
)

func TestWebExternalAuthFeature(t *testing.T) {
	suite := godog.TestSuite{ScenarioInitializer: func(ctx *godog.ScenarioContext) {
		var response *httptest.ResponseRecorder
		ctx.Step(`^a browser visits the logout continuation without a ticket$`, func() error {
			state := &loginFeatureState{}
			defer state.close()
			if err := state.reset(); err != nil {
				return err
			}
			if err := state.start("", ""); err != nil {
				return err
			}
			response = httptest.NewRecorder()
			state.srv.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/coddy/auth/logout/continue", nil))
			return nil
		})
		ctx.Step(`^it is redirected to the Coddy root$`, func() error {
			if response.Code != 303 || response.Header().Get("Location") != "/" {
				return fmt.Errorf("status %d, location %q", response.Code, response.Header().Get("Location"))
			}
			return nil
		})
		var f *oidcFixture
		var users []*http.Cookie
		var loggedOut *httptest.ResponseRecorder
		ctx.Step(`^the server uses an external issuer and has an enrolled local account$`, func() { f = newOIDCFixture(t) })
		ctx.Step(`^two subjects with the configured role sign in at the external issuer$`, func() error {
			for _, sub := range []string{"alpha", "beta"} {
				p, b := beginOIDCTest(t, f, sub)
				if w := f.request("GET", "/coddy/sessions", "", b); w.Code != 401 {
					return fmt.Errorf("pre-authentication cookie reached sessions: %d", w.Code)
				}
				c := finishOIDCTest(t, f, p, b)
				users = append(users, c)
				w := f.request("GET", "/coddy/auth/me", "", c)
				var m map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
					return err
				}
				if m["authenticated"] != true || m["oidc_enabled"] != true || m["user"] != sub || m["second_factor"] != nil || m["telegram_login"] != false || m["browser_login"] != false {
					return fmt.Errorf("unexpected external auth state: %v", m)
				}
			}
			if users[0].Value == users[1].Value {
				return fmt.Errorf("two subjects share a credential")
			}
			return nil
		})
		ctx.Step(`^both new sessions can use both chats, settings and tools without contacting the issuer$`, func() error {
			before := f.calls.Load()
			var paths []string
			for i, c := range users {
				w := f.request("POST", "/v1/responses", fmt.Sprintf(`{"model":"agent","input":"test chat %d"}`, i), c)
				if w.Code != 200 {
					return fmt.Errorf("creating chat: status %d %s", w.Code, w.Body.String())
				}
				sid := w.Header().Get("X-Coddy-Session-ID")
				if sid == "" {
					return fmt.Errorf("no chat ID")
				}
				paths = append(paths, "/coddy/sessions/"+sid+"/messages", "/coddy/sessions/"+sid+"/tool-calls")
			}
			for _, c := range users {
				for _, route := range append(paths, "/coddy/config", "/coddy/mcp") {
					if w := f.request("GET", route, "", c); w.Code != 200 {
						return fmt.Errorf("%s: %d", route, w.Code)
					}
				}
			}
			if f.calls.Load() != before {
				return fmt.Errorf("API requests contacted the issuer")
			}
			return nil
		})
		ctx.Step(`^the local password and one-time code work again only after disabling external sign-in$`, func() error {
			const password = `{"user":"legacy","password":"unused-password"}`
			for _, route := range []string{"/coddy/auth/login", "/coddy/auth/login/verify", "/coddy/auth/browser", "/coddy/auth/telegram"} {
				if w := f.request("POST", route, password); w.Code != 403 {
					return fmt.Errorf("alternate login %s: %d", route, w.Code)
				}
			}
			if w := f.request("POST", "/coddy/auth/totp/setup", "{}", users[0]); w.Code != 401 {
				return fmt.Errorf("enrollment was enabled: %d", w.Code)
			}
			f.state.replaceConfig(func(cfg *config.Config) { cfg.HTTPServer.Login.OIDC.Enable = false })
			if w := f.request("GET", "/coddy/sessions", "", users[0]); w.Code != 401 {
				return fmt.Errorf("external credential survived the mode switch")
			}
			w := f.request("POST", "/coddy/auth/login", password)
			var m map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
				return err
			}
			if w.Code != 200 || m["second_factor"] != "totp" || m["challenge"] == "" {
				return fmt.Errorf("local factor was not restored: %d %s", w.Code, w.Body.String())
			}
			code, err := webauth.TOTPCode(f.state.srv.activeCfg().HTTPServer.Login.TOTPSecret, f.state.srv.authNow())
			if err != nil {
				return err
			}
			w = f.request("POST", "/coddy/auth/login/verify", fmt.Sprintf(`{"challenge":%q,"code":%q}`, m["challenge"], code))
			if w.Code != 200 || len(w.Result().Cookies()) == 0 {
				return fmt.Errorf("restored local credentials failed: %d", w.Code)
			}
			return nil
		})
		ctx.Step(`^the external browser signs out$`, func() {
			p, b := beginOIDCTest(t, f, "alpha")
			c := finishOIDCTest(t, f, p, b)
			loggedOut = f.request("POST", "/coddy/auth/logout", "", c)
		})
		ctx.Step(`^the eligible browser can continue logout exactly once$`, func() error {
			if loggedOut.Code != 200 || loggedOut.Header().Get(logoutContinueHeader) != "1" || loggedOut.Header().Get("Location") != "" || loggedOut.Body.String() != "{\"ok\":true}\n" {
				return fmt.Errorf("unexpected logout response: %d %s", loggedOut.Code, loggedOut.Body.String())
			}
			before := f.calls.Load()
			cookies := loggedOut.Result().Cookies()
			for _, test := range []struct {
				want    string
				cookies []*http.Cookie
			}{{testLogoutURL, cookies}, {"/", cookies}} {
				w := f.request("GET", logoutContinuePath, "", test.cookies...)
				if w.Code != 303 || w.Header().Get("Location") != test.want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
					return fmt.Errorf("unexpected continuation: %d %s", w.Code, w.Header().Get("Location"))
				}
			}
			if f.calls.Load() != before {
				return fmt.Errorf("logout contacted issuer")
			}
			return nil
		})

	}, Options: &godog.Options{Paths: []string{"../../features/web_external_auth.feature"}, TestingT: t, Format: "progress"}}
	if suite.Run() != 0 {
		t.Fail()
	}
}
