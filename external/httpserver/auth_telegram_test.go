//go:build http

package httpserver

// The edges of the Telegram Mini App sign-in; the happy path is
// features/webui_telegram_signin.feature.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const tgSignInToken = "123456:mini-app-test"

// signedLaunch builds launch data the way a Telegram client gets it.
func signedLaunch(token string, userID int64, at time.Time) string {
	fields := map[string]string{
		"auth_date": fmt.Sprint(at.Unix()),
		"query_id":  "AAH" + fmt.Sprint(at.UnixNano()),
		"user":      fmt.Sprintf(`{"id":%d,"first_name":"T"}`, userID),
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		lines = append(lines, k+"="+fields[k])
	}
	sec := hmac.New(sha256.New, []byte("WebAppData"))
	sec.Write([]byte(token))
	m := hmac.New(sha256.New, sec.Sum(nil))
	m.Write([]byte(strings.Join(lines, "\n")))
	v := url.Values{}
	for k, val := range fields {
		v.Set(k, val)
	}
	v.Set("hash", hex.EncodeToString(m.Sum(nil)))
	return v.Encode()
}

// newTelegramServer is a server gated by gate ("login" or "token") with a
// Telegram bot whose admins are admins.
func newTelegramServer(t *testing.T, gate string, admins ...int64) *Server {
	t.Helper()
	login := config.HTTPLoginConfig{}
	if gate == "login" {
		login = configuredLogin(t)
	}
	srv := newLoginServer(t, login)
	cfg := *srv.activeCfg()
	cfg.Gateways.Telegram = config.TelegramGatewayConfig{Enabled: true, Token: tgSignInToken, Admins: admins}
	if gate == "token" {
		cfg.HTTPServer.AuthToken = "api-token"
	}
	srv.cfgAt.Store(&cfg)
	return srv
}

func telegramSignIn(srv *Server, initData string, headers map[string]string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"init_data": initData})
	r := httptest.NewRequest(http.MethodPost, "/coddy/auth/telegram", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

func tgCookieOf(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if strings.HasPrefix(c.Name, tgSessionCookieBaseName) && c.Value != "" {
			return c
		}
	}
	return nil
}

// reachesAPI asks a protected route with the cookie.
func reachesAPI(srv *Server, c *http.Cookie) int {
	r := httptest.NewRequest(http.MethodGet, "/coddy/sessions", nil)
	if c != nil {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w.Code
}

func TestTelegramSignInEdges(t *testing.T) {
	now := time.Now()
	t.Run("replayed launch", func(t *testing.T) {
		srv := newTelegramServer(t, "login", 7)
		data := signedLaunch(tgSignInToken, 7, now)
		if w := telegramSignIn(srv, data, nil); w.Code != http.StatusOK {
			t.Fatalf("first use: %d %s", w.Code, w.Body)
		}
		if w := telegramSignIn(srv, data, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("replay: %d", w.Code)
		}
	})
	t.Run("stale, tampered and foreign launches", func(t *testing.T) {
		srv := newTelegramServer(t, "login", 7)
		for name, data := range map[string]string{
			"stale":    signedLaunch(tgSignInToken, 7, now.Add(-2*time.Hour)),
			"tampered": strings.Replace(signedLaunch(tgSignInToken, 7, now), "%3A7%2C", "%3A8%2C", 1),
			"foreign":  signedLaunch("999:another-bot", 7, now),
		} {
			if w := telegramSignIn(srv, data, nil); w.Code != http.StatusUnauthorized || tgCookieOf(w) != nil {
				t.Errorf("%s: %d, cookie %v", name, w.Code, tgCookieOf(w))
			}
		}
	})
	t.Run("cross-site", func(t *testing.T) {
		srv := newTelegramServer(t, "login", 7)
		if w := telegramSignIn(srv, signedLaunch(tgSignInToken, 7, now), map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != http.StatusForbidden {
			t.Fatalf("cross-site: %d", w.Code)
		}
	})
	t.Run("no bot", func(t *testing.T) {
		srv := newLoginServer(t, configuredLogin(t))
		if w := telegramSignIn(srv, signedLaunch(tgSignInToken, 7, now), nil); w.Code != http.StatusBadRequest {
			t.Fatalf("without a bot: %d", w.Code)
		}
	})
	t.Run("a server closed by a token only", func(t *testing.T) {
		srv := newTelegramServer(t, "token", 7)
		c := tgCookieOf(telegramSignIn(srv, signedLaunch(tgSignInToken, 7, now), nil))
		if c == nil || reachesAPI(srv, c) != http.StatusOK {
			t.Fatal("a Mini App session is not accepted behind a token-only gate")
		}
		if reachesAPI(srv, nil) != http.StatusUnauthorized {
			t.Fatal("the gate is open without a credential")
		}
	})
	t.Run("an admin removed or a token rotated ends the session", func(t *testing.T) {
		for _, change := range []func(*config.Config){
			func(c *config.Config) { c.Gateways.Telegram.Admins = nil },
			func(c *config.Config) { c.Gateways.Telegram.Token = "123456:rotated" },
			func(c *config.Config) { c.Gateways.Telegram.Enabled = false },
		} {
			srv := newTelegramServer(t, "login", 7)
			c := tgCookieOf(telegramSignIn(srv, signedLaunch(tgSignInToken, 7, now), nil))
			if c == nil || reachesAPI(srv, c) != http.StatusOK {
				t.Fatal("no session to end")
			}
			cfg := *srv.activeCfg()
			change(&cfg)
			srv.cfgAt.Store(&cfg)
			if code := reachesAPI(srv, c); code != http.StatusUnauthorized {
				t.Fatalf("the session outlived the change: %d", code)
			}
		}
	})
	t.Run("sign-out ends it", func(t *testing.T) {
		srv := newTelegramServer(t, "login", 7)
		c := tgCookieOf(telegramSignIn(srv, signedLaunch(tgSignInToken, 7, now), nil))
		r := httptest.NewRequest(http.MethodPost, "/coddy/auth/logout", nil)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.AddCookie(c)
		srv.Handler().ServeHTTP(httptest.NewRecorder(), r)
		if reachesAPI(srv, c) != http.StatusUnauthorized {
			t.Fatal("the Mini App session survived the sign-out")
		}
	})
}

// telegramSignInWorld is the godog world of the feature.
type telegramSignInWorld struct {
	t      *testing.T
	srv    *Server
	last   *httptest.ResponseRecorder
	cookie *http.Cookie
}

func (w *telegramSignInWorld) serverWithAdmin(admin int64) error {
	w.srv = newTelegramServer(w.t, "login", admin)
	return nil
}

func (w *telegramSignInWorld) posts(user int64) error {
	w.last = telegramSignIn(w.srv, signedLaunch(tgSignInToken, user, time.Now()), nil)
	w.cookie = tgCookieOf(w.last)
	return nil
}

func (w *telegramSignInWorld) accepted() error {
	if w.last.Code != http.StatusOK || w.cookie == nil {
		return fmt.Errorf("sign-in: %d %s", w.last.Code, w.last.Body)
	}
	return nil
}

func (w *telegramSignInWorld) refusedWith(code int) error {
	if w.last.Code != code || w.cookie != nil {
		return fmt.Errorf("sign-in: %d, cookie %v", w.last.Code, w.cookie)
	}
	return nil
}

func (w *telegramSignInWorld) reaches() error {
	if code := reachesAPI(w.srv, w.cookie); code != http.StatusOK {
		return fmt.Errorf("API answered %d", code)
	}
	return nil
}

func (w *telegramSignInWorld) doesNotReach() error {
	if code := reachesAPI(w.srv, w.cookie); code != http.StatusUnauthorized {
		return fmt.Errorf("API answered %d", code)
	}
	return nil
}

func (w *telegramSignInWorld) signedInAs(user string) error {
	r := httptest.NewRequest(http.MethodGet, "/coddy/auth/me", nil)
	r.AddCookie(w.cookie)
	rec := httptest.NewRecorder()
	w.srv.Handler().ServeHTTP(rec, r)
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["authenticated"] != true || body["user"] != user {
		return fmt.Errorf("/coddy/auth/me: %v", body)
	}
	return nil
}

func TestWebUITelegramSignInFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "webui telegram sign-in",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &telegramSignInWorld{t: t}
			sc.Given(`^a web UI behind a sign-in form with a Telegram bot whose admin is (\d+)$`, w.serverWithAdmin)
			sc.When(`^the Mini App posts launch data signed for user (\d+)$`, w.posts)
			sc.Then(`^the sign-in is accepted$`, w.accepted)
			sc.Then(`^the sign-in is refused with status (\d+)$`, w.refusedWith)
			sc.Then(`^the browser reaches the API with its cookie$`, w.reaches)
			sc.Then(`^the browser does not reach the API$`, w.doesNotReach)
			sc.Then(`^the server reports the browser signed in as "([^"]*)"$`, w.signedInAs)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/webui_telegram_signin.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("webui telegram sign-in feature failed")
	}
}
