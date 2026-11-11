//go:build http

package httpserver

import (
	"encoding/json"
	"testing"
	"time"
)

func TestChallengeUsesStricterCurrentLoginTTL(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hours int
		want  int
	}{{"session only", 0, 0}, {"shortened", 12, 12 * 60 * 60}} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1800000000, 0)
			s := newTOTPLoginServer(t, now)
			w := signInWithRemember(t, s, loginTestUser, loginTestPassword, true)
			var answer map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
				t.Fatal(err)
			}
			if answer["challenge"] == "" {
				t.Fatalf("missing challenge: %s", w.Body.String())
			}
			next := *s.activeCfg()
			next.HTTPServer.Login.SessionTTLHours = tc.hours
			next.HTTPServer.Login.SessionTTLHoursSet = true
			s.ReplaceConfig(&next)
			cookie := sessionCookieOf(t, challengeVerify(s, answer["challenge"], codeAt(t, now)))
			if cookie.MaxAge != tc.want {
				t.Fatalf("MaxAge = %d, want %d", cookie.MaxAge, tc.want)
			}
		})
	}
}

func TestZeroLoginTTLAlwaysIssuesSessionCookie(t *testing.T) {
	for _, withTOTP := range []bool{false, true} {
		t.Run(map[bool]string{false: "password", true: "totp"}[withTOTP], func(t *testing.T) {
			var s *Server
			now := time.Unix(1800000000, 0)
			if withTOTP {
				s = newTOTPLoginServer(t, now)
				next := *s.activeCfg()
				next.HTTPServer.Login.SessionTTLHours = 0
				next.HTTPServer.Login.SessionTTLHoursSet = true
				s.ReplaceConfig(&next)
			} else {
				login := configuredLogin(t)
				login.SessionTTLHours = 0
				login.SessionTTLHoursSet = true
				s = newLoginServer(t, login)
			}
			w := signInWithRemember(t, s, loginTestUser, loginTestPassword, true)
			if withTOTP {
				var answer map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
					t.Fatal(err)
				}
				if answer["challenge"] == "" {
					t.Fatalf("missing TOTP challenge: %s", w.Body.String())
				}
				w = challengeVerify(s, answer["challenge"], codeAt(t, now))
			}
			cookie := sessionCookieOf(t, w)
			if cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
				t.Fatalf("persistent cookie with zero TTL: MaxAge=%d Expires=%v", cookie.MaxAge, cookie.Expires)
			}
		})
	}
}
