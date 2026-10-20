package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The client id every OpenCode grant is minted under. Written out rather than
// read from openCodeClientID, so renaming the constant cannot quietly rename
// what Coddy announces to the gateway.
const wantOpenCodeClientID = "coddy"

// requireOpenCodeClientID fails the test unless a device-flow request carries
// the client id and grant type the gateway expects, and hands the body back so
// a stand can check the rest of it. The body is put back on the request, so a
// stand and the shared answer handler can both look at the same grant. Every
// stand that serves /auth/device/* runs it, so a grant that stops sending the
// id cannot pass any of them.
func requireOpenCodeClientID(t *testing.T, r *http.Request, wantGrant string) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("%s: read body: %v", r.URL.Path, err)
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Errorf("%s: decode body: %v", r.URL.Path, err)
		return nil
	}
	if body["client_id"] != wantOpenCodeClientID {
		t.Errorf("%s: client_id = %v, want %q", r.URL.Path, body["client_id"], wantOpenCodeClientID)
	}
	if wantGrant != "" {
		if got, _ := body["grant_type"].(string); got != wantGrant {
			t.Errorf("%s: grant_type = %q, want %q", r.URL.Path, got, wantGrant)
		}
	}
	return body
}

// TestOpenCodeClientIDIsCoddy pins what Coddy announces to the gateway: the
// three grants send the constant, and the stands above hold them to "coddy".
func TestOpenCodeClientIDIsCoddy(t *testing.T) {
	if openCodeClientID != wantOpenCodeClientID {
		t.Fatalf("openCodeClientID = %q, want %q", openCodeClientID, wantOpenCodeClientID)
	}
}

// An expired account login must refresh before discovery and inference, using
// the row's HTTPS CONNECT proxy for both hosts. Refresh rotation must survive
// a later discovery failure without depending on /api/user or /api/orgs.
func TestOpenCodeAccountRefreshThroughHTTPSProxy(t *testing.T) {
	var refreshes, requests atomic.Int32
	proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/console/auth/device/token" {
			body := requireOpenCodeClientID(t, r, "refresh_token")
			if body["refresh_token"] != "old-refresh" {
				t.Errorf("refresh_token = %v, want old-refresh", body["refresh_token"])
			}
			refreshes.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":600,"org_id":"org-new"}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/console/api/") {
			// The best-effort account discovery after the refresh rotated the
			// organization carries the refreshed bearer token; the org header
			// belongs to inference requests only.
			if r.Header.Get("Authorization") != "Bearer fresh-access" {
				t.Error("account discovery did not use the refreshed token")
				http.Error(w, "wrong credential", http.StatusUnauthorized)
				return
			}
		} else if r.Header.Get("Authorization") != "Bearer fresh-access" || r.Header.Get("x-org-id") != "org-new" {
			t.Error("inference did not use the refreshed account and organization")
			http.Error(w, "wrong credential", http.StatusUnauthorized)
			return
		}
		requests.Add(1)
		if !answerProviderRequest(t, w, r) {
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
	if refreshes.Load() != 1 || requests.Load() != 11 {
		// 9 inference/catalog requests plus the best-effort account discovery
		// after the refresh rotated the organization (its name is not in the
		// token). The stand's org list has no org-new, so discovery fails and
		// inference still proceeds.
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
	// The raw organization id must never stand in for its display name.
	if name, _ := saved["org_name"].(string); name == "org-new" {
		t.Fatal("organization id was stored as its display name")
	}
}

// A refresh that rotates the organization must refill the display name from
// the account instead of storing the raw organization id.
func TestOpenCodeRefreshRefillsOrganizationName(t *testing.T) {
	proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/console/auth/device/token":
			_, _ = w.Write([]byte(`{"access_token":"fresh","refresh_token":"rotated","expires_in":600,"org_id":"org-two"}`))
		case "/console/api/user":
			_, _ = w.Write([]byte(`{"email":"u@example.com"}`))
		case "/console/api/orgs":
			_, _ = w.Write([]byte(`[{"id":"org-one","name":"One"},{"id":"org-two","name":"Two"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := saveOpenCodeCredential(path, &opencodeAuthFile{AccessToken: "old", RefreshToken: "old-refresh", ExpiresAt: 1, OrgID: "org-one", OrgName: "One"}); err != nil {
		t.Fatal(err)
	}
	hc, err := HTTPClientForProviderProxy(proxy)
	if err != nil {
		t.Fatal(err)
	}
	key, org, err := openCodeCredential(context.Background(), "", path, hc)
	if err != nil || key != "fresh" || org != "org-two" {
		t.Fatalf("key=%q org=%q err=%v", key, org, err)
	}
	saved, err := loadOpenCodeAuth(path)
	if err != nil || saved.OrgID != "org-two" || saved.OrgName != "Two" {
		t.Fatalf("organization name was not refilled after rotation: %+v", saved)
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
			body := requireOpenCodeClientID(t, r, "")
			if body["supports_org_scope"] != true {
				t.Error("device request does not ask for organization scope")
			}
		}
		if r.URL.Path == "/console/auth/device/token" {
			requireOpenCodeClientID(t, r, "urn:ietf:params:oauth:grant-type:device_code")
		}
		if !answerProviderRequest(t, w, r) {
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
		requireOpenCodeClientID(t, r, "refresh_token")
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
					requireOpenCodeClientID(t, r, "")
					_, _ = w.Write([]byte(`{"device_code":"private","user_code":"USER","verification_uri_complete":"/console/device","expires_in":600,"interval":0}`))
				case "/console/auth/device/token":
					requireOpenCodeClientID(t, r, "urn:ietf:params:oauth:grant-type:device_code")
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
			requireOpenCodeClientID(t, r, "refresh_token")
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
