//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func challengeStart(t *testing.T, s *Server) string {
	t.Helper()
	w := signIn(t, s, loginTestUser, loginTestPassword)
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 200 || body["second_factor"] != "totp" || body["challenge"] == "" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("password step: %d %s", w.Code, w.Body.String())
	}
	return body["challenge"]
}
func challengeVerify(s *Server, challenge, code string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"challenge": challenge, "code": code})
	r := httptest.NewRequest(http.MethodPost, "/coddy/auth/login/verify", strings.NewReader(string(body)))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestChallengeVerificationAndSingleUse(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := newTOTPLoginServer(t, now)
	c := challengeStart(t, s)
	w := challengeVerify(s, c, codeAt(t, now))
	if w.Code != 200 || len(w.Result().Cookies()) == 0 {
		t.Fatalf("verify: %d %s", w.Code, w.Body.String())
	}
	if w = challengeVerify(s, c, codeAt(t, now)); w.Code != 401 {
		t.Fatalf("reused challenge: %d", w.Code)
	}
}
func TestChallengeWrongCodeConsumesChallenge(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := newTOTPLoginServer(t, now)
	c := challengeStart(t, s)
	if w := challengeVerify(s, c, "not-a-code"); w.Code != 401 {
		t.Fatalf("wrong code: %d", w.Code)
	}
	if w := challengeVerify(s, c, codeAt(t, now)); w.Code != 401 {
		t.Fatalf("retry: %d", w.Code)
	}
}
func TestChallengeExpiryAndBinding(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := newTOTPLoginServer(t, now)
	c := challengeStart(t, s)
	s.sessions.Now = func() time.Time { return now.Add(4 * time.Minute) }
	if w := challengeVerify(s, c, codeAt(t, now.Add(4*time.Minute))); w.Code != 401 {
		t.Fatalf("expired: %d", w.Code)
	}
	s.sessions.Now = func() time.Time { return now }
	c = challengeStart(t, s)
	next := *s.activeCfg()
	next.HTTPServer.Login.PasswordHash = loginTestHash(t, "changed")
	s.ReplaceConfig(&next)
	if w := challengeVerify(s, c, codeAt(t, now)); w.Code != 401 {
		t.Fatalf("changed account: %d", w.Code)
	}
}

func TestChallengeBackupDurableAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			now := time.Unix(1800000000, 0)
			s := newTOTPLoginServer(t, now)
			state, codes, err := webauth.GenerateBackupCodes(2)
			if err != nil {
				t.Fatal(err)
			}
			c := s.activeCfg()
			c.HTTPServer.Login.BackupCodes = state
			c.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}}
			c.Paths.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
			raw, err := config.MarshalConfigYAMLForFile(c, c.Paths.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(c.Paths.ConfigPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if fail {
				s.mgr.SetConfigAdjuster(func(*config.Config) error { return errors.New("injected") })
			}
			challenge := challengeStart(t, s)
			w := challengeVerify(s, challenge, codes[0])
			after, err := os.ReadFile(c.Paths.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if w.Code != 500 || len(w.Result().Cookies()) != 0 || !bytes.Equal(after, raw) {
					t.Fatalf("failed persistence: %d %s", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != 200 || len(w.Result().Cookies()) == 0 {
				t.Fatalf("backup: %d %s", w.Code, w.Body.String())
			}
			loaded, err := config.ParseConfigYAML(after, c.Paths)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.HTTPServer.Login.BackupCodes.Generation != state.Generation || len(loaded.HTTPServer.Login.BackupCodes.Hashes) != 1 {
				t.Fatal("backup consumption not persisted")
			}
			challenge = challengeStart(t, s)
			if w = challengeVerify(s, challenge, codes[0]); w.Code != 401 {
				t.Fatalf("reused backup: %d", w.Code)
			}
		})
	}
}

func TestChallengeVerifyThrottle(t *testing.T) {
	s := newTOTPLoginServer(t, time.Unix(1800000000, 0))
	s.loginThrottle.Base = time.Millisecond
	s.loginThrottle.Max = 2 * time.Millisecond
	r := httptest.NewRequest(http.MethodPost, "/coddy/auth/login/verify", strings.NewReader(`{"challenge":"unknown","code":"bad"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.RemoteAddr = "192.0.2.7:1234"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 || s.loginThrottle.Delay("192.0.2.7") <= 0 {
		t.Fatalf("verify throttle: %d", w.Code)
	}
}
