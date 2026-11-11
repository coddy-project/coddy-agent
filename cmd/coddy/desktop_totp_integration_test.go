//go:build desktop && http && ui

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

const desktopTOTPTestSecret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

func TestDesktopAuthStillWorksWithTOTP(t *testing.T) {
	hash, err := webauth.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	withLogin := func(cfg *config.Config) {
		cfg.HTTPServer.Login = config.HTTPLoginConfig{
			User: "operator", PasswordHash: hash, TOTPSecret: desktopTOTPTestSecret,
		}
	}
	for _, attached := range []bool{false, true} {
		mode := "standalone"
		if attached {
			mode = "attached"
		}
		t.Run(mode, func(t *testing.T) {
			t.Run("ordinary window skips both factors", func(t *testing.T) {
				base := startDesktopSecurityStand(t, attached, true, withLogin)
				res, err := http.Get(base + "/coddy/auth/me")
				if err != nil {
					t.Fatal(err)
				}
				var me map[string]any
				if err := json.NewDecoder(res.Body).Decode(&me); err != nil {
					t.Fatal(err)
				}
				if err := res.Body.Close(); err != nil {
					t.Fatal(err)
				}
				if me["login_required"] != false || me["auth_required"] != false {
					t.Fatalf("the ordinary desktop drew a sign-in form with TOTP configured: %v", me)
				}
				if _, present := me["second_factor"]; present {
					t.Fatalf("the ordinary desktop drew the second-factor step: %v", me)
				}
			})

			t.Run("auth keeps password and one-time code", func(t *testing.T) {
				base := startDesktopSecurityStand(t, attached, false, withLogin)
				jar, err := cookiejar.New(nil)
				if err != nil {
					t.Fatal(err)
				}
				browser := &http.Client{Jar: jar}
				login := desktopAuthJSON(t, browser, http.MethodPost, base+"/coddy/auth/login", map[string]any{
					"user": "operator", "password": "correct horse",
				})
				if login["second_factor"] != "totp" || login["challenge"] == "" {
					t.Fatalf("the password did not open a TOTP challenge through the desktop bridge: %v", login)
				}
				res, err := browser.Get(base + "/v1/models")
				if err != nil {
					t.Fatal(err)
				}
				_ = res.Body.Close()
				if res.StatusCode != http.StatusUnauthorized {
					t.Fatalf("the password alone signed the browser in with TOTP enabled: %s", res.Status)
				}
				code, err := webauth.TOTPCode(desktopTOTPTestSecret, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				desktopAuthJSON(t, browser, http.MethodPost, base+"/coddy/auth/login/verify", map[string]any{
					"challenge": login["challenge"], "code": code,
				})
				res, err = browser.Get(base + "/v1/models")
				if err != nil {
					t.Fatal(err)
				}
				_ = res.Body.Close()
				if res.StatusCode != http.StatusOK {
					t.Fatalf("the TOTP sign-in did not carry its session through the desktop bridge: %s", res.Status)
				}
			})
		})
	}
}

func desktopAuthJSON(t *testing.T, client *http.Client, method, url string, body map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s %s answered %s", method, url, res.Status)
	}
	var result map[string]any
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
