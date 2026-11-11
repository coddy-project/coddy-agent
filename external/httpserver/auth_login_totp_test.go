//go:build http

package httpserver

// The edges of the one-time code at the web sign-in: a missing, wrong, stale or
// replayed code, a code that arrives with the wrong password, and a server
// without codes that ignores the field. The happy path is
// features/webui_login_totp.feature.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

const totpTestSecret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

// newTOTPLoginServer is a sign-in server with codes on and a clock the test
// holds still, so a code is the same for the whole test.
func newTOTPLoginServer(t *testing.T, now time.Time) *Server {
	t.Helper()
	login := configuredLogin(t)
	login.TOTPSecret = totpTestSecret
	srv := newLoginServer(t, login)
	srv.sessions.Now = func() time.Time { return now }
	return srv
}

func totpLoginAttempt(t *testing.T, srv *Server, user, password, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"user": user, "password": password, "code": code})
	r := httptest.NewRequest(http.MethodPost, "/coddy/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	// A loopback address is never throttled, so a run of refusals stays fast.
	r.RemoteAddr = "127.0.0.1:5555"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if srv.loginPolicyNow().account.totpSecret != "" && rec.Code == http.StatusOK {
		var result map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &result)
		return challengeVerify(srv, result["challenge"], code)
	}
	return rec
}

func codeAt(t *testing.T, at time.Time) string {
	t.Helper()
	code, err := webauth.TOTPCode(totpTestSecret, at)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestTOTPLoginRefusals(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	current := codeAt(t, now)
	wrong := "000000"
	if current == wrong {
		wrong = "111111"
	}
	cases := map[string]struct{ password, code string }{
		"no code":                        {loginTestPassword, ""},
		"wrong code":                     {loginTestPassword, wrong},
		"code from two steps ago":        {loginTestPassword, codeAt(t, now.Add(-2*webauth.TOTPPeriod))},
		"right code with wrong password": {"wrong-horse", current},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newTOTPLoginServer(t, now)
			rec := totpLoginAttempt(t, srv, loginTestUser, c.password, c.code)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body.String())
			}
			// One answer for every refusal: neither the password nor the code
			// is confirmed by the wording.
			var body map[string]interface{}
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["error"] != "invalid credentials" {
				t.Fatalf("refusal body = %s", rec.Body.String())
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Fatal("a refused sign-in set a cookie")
			}
		})
	}
}

func TestTOTPLoginRefusedWithWrongPasswordDoesNotSpendTheCode(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	srv := newTOTPLoginServer(t, now)
	code := codeAt(t, now)
	if rec := totpLoginAttempt(t, srv, loginTestUser, "wrong-horse", code); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status %d", rec.Code)
	}
	if rec := totpLoginAttempt(t, srv, loginTestUser, loginTestPassword, code); rec.Code != http.StatusOK {
		t.Fatalf("the operator's own retry with the same code was refused: %d %s", rec.Code, rec.Body.String())
	}
}

func TestTOTPLoginCodeIsTakenOnce(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	srv := newTOTPLoginServer(t, now)
	code := codeAt(t, now)
	if rec := totpLoginAttempt(t, srv, loginTestUser, loginTestPassword, code); rec.Code != http.StatusOK {
		t.Fatalf("first use: status %d %s", rec.Code, rec.Body.String())
	}
	if rec := totpLoginAttempt(t, srv, loginTestUser, loginTestPassword, code); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a replayed code opened a second session: status %d", rec.Code)
	}
	// The previous step's code is older than the one just taken.
	if rec := totpLoginAttempt(t, srv, loginTestUser, loginTestPassword, codeAt(t, now.Add(-webauth.TOTPPeriod))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an older code was taken after a newer one: status %d", rec.Code)
	}
	// The next step's code is a new code.
	srv.sessions.Now = func() time.Time { return now.Add(webauth.TOTPPeriod) }
	if rec := totpLoginAttempt(t, srv, loginTestUser, loginTestPassword, codeAt(t, now.Add(webauth.TOTPPeriod))); rec.Code != http.StatusOK {
		t.Fatalf("the next code was refused: status %d", rec.Code)
	}
}

func TestTOTPLoginAcceptsTheNeighbouringStepsAndSpacedCodes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, d := range []time.Duration{-webauth.TOTPPeriod, webauth.TOTPPeriod} {
		srv := newTOTPLoginServer(t, now)
		code := codeAt(t, now.Add(d))
		if rec := totpLoginAttempt(t, srv, loginTestUser, loginTestPassword, code[:3]+" "+code[3:]); rec.Code != http.StatusOK {
			t.Fatalf("code %v away: status %d %s", d, rec.Code, rec.Body.String())
		}
	}
}

func TestTOTPLoginServerWithoutCodesIgnoresTheField(t *testing.T) {
	srv := newLoginServer(t, configuredLogin(t))
	rec := totpLoginAttempt(t, srv, loginTestUser, loginTestPassword, "123456")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	me := httptest.NewRecorder()
	srv.Handler().ServeHTTP(me, httptest.NewRequest(http.MethodGet, "/coddy/auth/me", nil))
	if strings.Contains(me.Body.String(), "second_factor") {
		t.Fatalf("a server without codes announced a second factor: %s", me.Body.String())
	}
}

func TestTOTPSecretAppliesToAnAccountFromTheEnvironment(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	srv := newLoginServer(t, config.HTTPLoginConfig{TOTPSecret: totpTestSecret})
	srv.sessions.Now = func() time.Time { return now }
	if err := srv.SetExtraLogin("envuser", "env-pass"); err != nil {
		t.Fatal(err)
	}
	if rec := totpLoginAttempt(t, srv, "envuser", "env-pass", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an environment account signed in without its code: status %d", rec.Code)
	}
	if rec := totpLoginAttempt(t, srv, "envuser", "env-pass", codeAt(t, now)); rec.Code != http.StatusOK {
		t.Fatalf("an environment account with its code: status %d %s", rec.Code, rec.Body.String())
	}
}
