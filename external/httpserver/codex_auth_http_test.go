//go:build http

package httpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func codexHTTPTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// TestCodexAuthDeviceLoginDrains pins that a sign-in still waiting for browser
// confirmation does not outlive the server: Drain must cancel it instead of
// leaving a goroutine that writes credentials into a torn-down home directory.
func TestCodexAuthDeviceLoginDrains(t *testing.T) {
	home := t.TempDir()
	var polls atomic.Int64
	authUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = fmt.Fprint(w, `{"device_auth_id":"device-drain","user_code":"DRAIN","interval":"0.01"}`)
		case "/api/accounts/deviceauth/token":
			// The user never confirms in the browser.
			polls.Add(1)
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer authUpstream.Close()

	cfg := &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	srv.codexAuthIssuer = authUpstream.URL
	ts := httptest.NewServer(srv.Handler())

	res, err := http.Post(ts.URL+"/coddy/providers/codex/codex-auth/device", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d", res.StatusCode)
	}
	deadline := time.Now().Add(2 * time.Second)
	for polls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("device login never polled the issuer")
		}
		time.Sleep(5 * time.Millisecond)
	}

	ts.Close()
	done := make(chan struct{})
	go func() {
		srv.Drain()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not return: a pending Codex sign-in blocks shutdown")
	}

	// After Drain the sign-in must be gone, not merely unobserved.
	settled := polls.Load()
	time.Sleep(200 * time.Millisecond)
	if got := polls.Load(); got != settled {
		t.Fatalf("pending Codex sign-in kept polling after Drain (%d -> %d)", settled, got)
	}
}

func TestCodexAuthDeviceHTTPFlow(t *testing.T) {
	home := t.TempDir()
	idToken := codexHTTPTestJWT(map[string]any{"chatgpt_account_id": "acct-http"})
	accessToken := codexHTTPTestJWT(map[string]any{"exp": 4_102_444_800})
	authUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = fmt.Fprint(w, `{"device_auth_id":"device-http","user_code":"HTTP-CODE","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			_, _ = fmt.Fprint(w, `{"authorization_code":"code-http","code_challenge":"challenge-http","code_verifier":"verifier-http"}`)
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token": idToken, "access_token": accessToken, "refresh_token": "refresh-http",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authUpstream.Close()

	cfg := &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), nil)
	srv := New(cfg, mgr, slog.Default(), t.TempDir())
	srv.codexAuthIssuer = authUpstream.URL
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	startRes, err := http.Post(ts.URL+"/coddy/providers/codex/codex-auth/device", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = startRes.Body.Close() }()
	if startRes.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d", startRes.StatusCode)
	}
	var start struct {
		LoginID         string `json:"login_id"`
		VerificationURL string `json:"verification_url"`
		UserCode        string `json:"user_code"`
	}
	if err := json.NewDecoder(startRes.Body).Decode(&start); err != nil {
		t.Fatal(err)
	}
	if start.LoginID == "" || start.UserCode != "HTTP-CODE" || start.VerificationURL != authUpstream.URL+"/codex/device" {
		t.Fatalf("unexpected start response: %+v", start)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		statusRes, err := http.Get(ts.URL + "/coddy/providers/codex/codex-auth/device/" + start.LoginID)
		if err != nil {
			t.Fatal(err)
		}
		var status struct {
			Status    string `json:"status"`
			Connected bool   `json:"connected"`
		}
		if err := json.NewDecoder(statusRes.Body).Decode(&status); err != nil {
			_ = statusRes.Body.Close()
			t.Fatal(err)
		}
		_ = statusRes.Body.Close()
		if status.Status == "completed" {
			if !status.Connected {
				t.Fatalf("completed status is not connected: %+v", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login did not complete, last status: %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	authPath := config.CodexAuthPath(home, "codex")
	if filepath.Dir(authPath) == home {
		t.Fatalf("auth path must be namespaced: %s", authPath)
	}
	statusRes, err := http.Get(ts.URL + "/coddy/providers/codex/codex-auth")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = statusRes.Body.Close() }()
	var status struct {
		Connected bool   `json:"connected"`
		Source    string `json:"source"`
	}
	if err := json.NewDecoder(statusRes.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.Connected || status.Source != "coddy" {
		t.Fatalf("saved status = %+v", status)
	}
}
