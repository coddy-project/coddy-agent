//go:build http

package httpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

func TestEnrollmentKeepsCookiePersistence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		remember bool
		hours    int
		maxAge   int
	}{
		{"browser session", false, 24, 0},
		{"persistent", true, 24, 24 * 60 * 60},
		{"zero duration", true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			login := configuredLogin(t)
			login.SessionTTLHours = tc.hours
			login.SessionTTLHoursSet = true
			s := newLoginServer(t, login)
			cfg := s.activeCfg()
			cfg.Providers = []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}}
			cfg.Paths.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
			raw, err := config.MarshalConfigYAMLForFile(cfg, cfg.Paths.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfg.Paths.ConfigPath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			current := sessionCookieOf(t, signInWithRemember(t, s, loginTestUser, loginTestPassword, tc.remember))
			const secret = "JBSWY3DPEHPK3PXP"
			code, err := webauth.TOTPCode(secret, s.authNow())
			if err != nil {
				t.Fatal(err)
			}
			steps := []struct{ path, body string }{
				{"/coddy/auth/totp/enable", fmt.Sprintf(`{"secret":%q,"code":%q}`, secret, code)},
				{"/coddy/auth/backup-codes/regenerate", `{}`},
				{"/coddy/auth/totp/disable", `{}`},
			}
			for _, step := range steps {
				r := httptest.NewRequest(http.MethodPost, step.path, strings.NewReader(step.body))
				r.Header.Set("Sec-Fetch-Site", "same-origin")
				r.AddCookie(current)
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status %d: %s", step.path, w.Code, w.Body.String())
				}
				current = sessionCookieOf(t, w)
				if current.MaxAge != tc.maxAge {
					t.Fatalf("%s: MaxAge = %d, want %d", step.path, current.MaxAge, tc.maxAge)
				}
				if tc.maxAge == 0 && !current.Expires.IsZero() {
					t.Fatalf("%s: session cookie has expiry %v", step.path, current.Expires)
				}
			}
		})
	}
}
