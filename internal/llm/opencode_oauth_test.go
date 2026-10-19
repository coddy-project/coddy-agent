package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// An expired account login must refresh before discovery and inference, using
// the row's HTTPS CONNECT proxy for both hosts. Refresh rotation must survive
// a later discovery failure without depending on /api/user or /api/orgs.
func TestOpenCodeAccountRefreshThroughHTTPSProxy(t *testing.T) {
	var refreshes, requests atomic.Int32
	proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/console/auth/device/token" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["grant_type"] != "refresh_token" || body["refresh_token"] != "old-refresh" || body["client_id"] != "opencode-cli" {
				t.Error("unexpected refresh request")
			}
			refreshes.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":600,"org_id":"org-new"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer fresh-access" || r.Header.Get("x-org-id") != "org-new" {
			t.Error("discovery/inference did not use the refreshed account and organization")
			http.Error(w, "wrong credential", http.StatusUnauthorized)
			return
		}
		requests.Add(1)
		if !answerProviderRequest(w, r) {
			http.NotFound(w, r)
		}
	}))
	t.Setenv(EnvOpenCodeBaseURL, "")
	t.Setenv(EnvOpenCodeConsoleURL, "")
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"expired-access","refresh_token":"old-refresh","expires_at":1,"org_id":"org-old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	in := ProviderInput{Type: "opencode", Model: "glm-5.3", AuthPath: path, ProxyURL: proxy, RetryDisabled: true}
	if _, err := ListModels(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"glm-5.3", "gpt-6-luna", "claude-haiku-5-5", "gemini-4-argon-contributor"} {
		in.Model = model
		p, err := NewProvider(in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil, func(StreamChunk) {}); err != nil {
			t.Fatal(err)
		}
	}
	if refreshes.Load() != 1 || requests.Load() != 9 {
		t.Fatalf("refreshes=%d, requests=%d", refreshes.Load(), requests.Load())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil || saved["refresh_token"] != "rotated-refresh" || saved["org_id"] != "org-new" {
		t.Fatal("rotated credential was not persisted")
	}
}

func TestOpenCodeDeviceLoginThroughHTTPSProxy(t *testing.T) {
	var calls atomic.Int32
	proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host != "opencode.ai" || r.Header.Get("User-Agent") != opencodeUserAgent() {
			t.Error("wrong account host or user agent")
		}
		if strings.HasPrefix(r.URL.Path, "/console/api/") && r.Header.Get("Authorization") != "Bearer account-access" {
			t.Error("account discovery lost its bearer token")
		}
		if r.URL.Path == "/console/auth/device/code" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["client_id"] != "opencode-cli" || body["supports_org_scope"] != true {
				t.Error("device request does not match OpenCode")
			}
		}
		if !answerProviderRequest(w, r) {
			http.NotFound(w, r)
		}
	}))
	t.Setenv(EnvOpenCodeConsoleURL, "")
	login, err := StartOpenCodeDeviceLogin(context.Background(), proxy)
	if err != nil {
		t.Fatal(err)
	}
	if login.UserCode != "USER-CODE" || login.URL != "https://opencode.ai/console/device" {
		t.Fatal("wrong verification prompt")
	}
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := login.Wait(context.Background(), path, opencodeGoBaseURL); err != nil {
		t.Fatal(err)
	}
	saved, err := loadOpenCodeAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.OrgID != "org-test" || saved.OrgName != "Test" || saved.RefreshToken != "account-refresh" || calls.Load() != 4 {
		t.Fatal("device login did not store the selected account")
	}
}

func TestOpenCodeConcurrentRefreshAndExplicitKey(t *testing.T) {
	var calls atomic.Int32
	proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"new","refresh_token":"new-refresh","expires_in":600}`))
	}))
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := saveOpenCodeCredential(path, &opencodeAuthFile{RefreshToken: "old", ExpiresAt: 1, OrgID: "org", OrgName: "Name"}); err != nil {
		t.Fatal(err)
	}
	hc, err := HTTPClientForProviderProxy(proxy)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			key, org, err := openCodeCredential(context.Background(), "", path, hc)
			if err != nil || key != "new" || org != "org" {
				t.Error("concurrent refresh lost credentials")
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("refresh called %d times", calls.Load())
	}
	if key, org, err := openCodeCredential(context.Background(), "explicit-key", path, hc); err != nil || key != "explicit-key" || org != "" {
		t.Fatal("stored account overrides explicit key")
	}
}

func TestOpenCodeAccountFailures(t *testing.T) {
	for _, outcome := range []string{"denied", "expired", "malformed", "redirect", "cancelled", "missing-org"} {
		t.Run(outcome, func(t *testing.T) {
			var escaped atomic.Int32
			proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/console/auth/device/code":
					if outcome == "redirect" {
						http.Redirect(w, r, "https://other.example/steal", http.StatusTemporaryRedirect)
						return
					}
					_, _ = w.Write([]byte(`{"device_code":"private","user_code":"USER","verification_uri_complete":"/console/device","expires_in":600,"interval":0}`))
				case "/console/auth/device/token":
					switch outcome {
					case "denied":
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":"access_denied"}`))
					case "expired":
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":"expired_token"}`))
					case "malformed":
						_, _ = w.Write([]byte(`{"access_token":"secret-only"}`))
					default:
						_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":600,"org_id":"missing"}`))
					}
				case "/console/api/user":
					_, _ = w.Write([]byte(`{"id":"u","email":"u@example.com"}`))
				case "/console/api/orgs":
					_, _ = w.Write([]byte(`[{"id":"other","name":"Other"}]`))
				default:
					escaped.Add(1)
				}
			}))
			login, err := StartOpenCodeDeviceLogin(context.Background(), proxy)
			if outcome == "redirect" {
				if err == nil || escaped.Load() != 0 {
					t.Fatal("account redirect was followed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if outcome == "cancelled" {
				cancel()
			}
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := login.Wait(ctx, path, opencodeGoBaseURL); err == nil {
				t.Fatal("failed authorization was accepted")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("failed authorization changed credentials")
			}
		})
	}
}

func TestOpenCodeRefreshFailureDoesNotSendInference(t *testing.T) {
	var inference atomic.Int32
	proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/console/auth/device/token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","detail":"private-refresh-secret"}`))
			return
		}
		inference.Add(1)
		http.Error(w, "should not reach inference", http.StatusUnauthorized)
	}))
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := saveOpenCodeCredential(path, &opencodeAuthFile{AccessToken: "old", RefreshToken: "private-refresh-secret", ExpiresAt: 1}); err != nil {
		t.Fatal(err)
	}
	p, err := NewProvider(ProviderInput{Type: "opencode", Model: "glm-5.3", AuthPath: path, ProxyURL: proxy, RetryDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
	if err == nil || strings.Contains(err.Error(), "private-refresh-secret") || inference.Load() != 0 {
		t.Fatal("failed refresh leaked a secret or sent inference")
	}
	credential, err := loadOpenCodeAuth(path)
	if err != nil || credential.RefreshToken != "private-refresh-secret" {
		t.Fatal("failed refresh destroyed the credential")
	}
}
