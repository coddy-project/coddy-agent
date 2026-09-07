package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func codexTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(claims)
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// TestCodexDeviceSignInPromptsThenPersists covers the one-call sign-in used by
// the CLI: the verification instructions must reach the caller BEFORE the flow
// blocks on confirmation, and the credential lands at authPath.
func TestCodexDeviceSignInPromptsThenPersists(t *testing.T) {
	idToken := codexTestJWT(map[string]any{"chatgpt_account_id": "acct-cli"})
	accessToken := codexTestJWT(map[string]any{"exp": 4_102_444_800})
	prompted := make(chan CodexDeviceLogin, 1)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = fmt.Fprint(w, `{"device_auth_id":"device-cli","user_code":"CLI-CODE","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			// The device token is only issued once the caller has been prompted,
			// mirroring a user who opens the page after seeing the code.
			select {
			case login := <-prompted:
				prompted <- login
			case <-r.Context().Done():
				return
			}
			_, _ = fmt.Fprint(w, `{"authorization_code":"code-cli","code_challenge":"challenge-cli","code_verifier":"verifier-cli"}`)
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token": idToken, "access_token": accessToken, "refresh_token": "refresh-cli",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	authPath := filepath.Join(t.TempDir(), "providers", "codex", "codex-auth.json")
	err := CodexDeviceSignIn(context.Background(), upstream.URL, upstream.Client(), authPath,
		func(login CodexDeviceLogin) { prompted <- login })
	if err != nil {
		t.Fatalf("CodexDeviceSignIn: %v", err)
	}

	login := <-prompted
	if login.UserCode != "CLI-CODE" || login.VerificationURL != upstream.URL+"/codex/device" {
		t.Fatalf("prompt = %+v, want the user code and verification URL", login)
	}
	status, err := InspectCodexAuth(authPath)
	if err != nil {
		t.Fatalf("InspectCodexAuth: %v", err)
	}
	if !status.Connected || status.Source != "coddy" || status.AccountID != "acct-cli" {
		t.Fatalf("status = %+v, want a connected coddy credential", status)
	}
}

func TestCodexDeviceLoginExchangesAndPersistsTokens(t *testing.T) {
	idToken := codexTestJWT(map[string]any{"chatgpt_account_id": "acct-web"})
	accessToken := codexTestJWT(map[string]any{"exp": 4_102_444_800})

	var gotExchange url.Values
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			if r.Method != http.MethodPost {
				t.Fatalf("usercode method = %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"device_auth_id": "device-1",
				"user_code":      "ABCD-EFGH",
				"interval":       "0",
			})
		case "/api/accounts/deviceauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_code": "code-1",
				"code_challenge":     "challenge-1",
				"code_verifier":      "verifier-1",
			})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			gotExchange = r.PostForm
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token":      idToken,
				"access_token":  accessToken,
				"refresh_token": "refresh-web",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	login, err := StartCodexDeviceLogin(context.Background(), upstream.URL, upstream.Client())
	if err != nil {
		t.Fatalf("StartCodexDeviceLogin: %v", err)
	}
	if login.VerificationURL != upstream.URL+"/codex/device" || login.UserCode != "ABCD-EFGH" {
		t.Fatalf("unexpected login: %+v", login)
	}

	authPath := filepath.Join(t.TempDir(), "provider", "auth.json")
	if err := CompleteCodexDeviceLogin(context.Background(), upstream.URL, upstream.Client(), login, authPath); err != nil {
		t.Fatalf("CompleteCodexDeviceLogin: %v", err)
	}

	if gotExchange.Get("grant_type") != "authorization_code" || gotExchange.Get("code") != "code-1" {
		t.Fatalf("unexpected exchange form: %v", gotExchange)
	}
	if gotExchange.Get("client_id") != codexClientID || gotExchange.Get("code_verifier") != "verifier-1" {
		t.Fatalf("missing OAuth fields: %v", gotExchange)
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read saved auth: %v", err)
	}
	var saved codexAuthFile
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("parse saved auth: %v", err)
	}
	if saved.AuthMode != codexAuthModeChatGPT || saved.Tokens.AccountID != "acct-web" {
		t.Fatalf("saved auth metadata = %+v", saved)
	}
	if saved.Tokens.AccessToken != accessToken || saved.Tokens.RefreshToken != "refresh-web" {
		t.Fatal("saved OAuth tokens do not match exchange response")
	}
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat saved auth: %v", err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("saved auth permissions = %o, want 600", got)
	}

	status, err := InspectCodexAuth(authPath)
	if err != nil {
		t.Fatalf("InspectCodexAuth: %v", err)
	}
	if !status.Connected || status.Source != "coddy" {
		t.Fatalf("status = %+v, want connected coddy credentials", status)
	}
}

func TestCodexDeviceLoginTreatsForbiddenAsPending(t *testing.T) {
	polls := 0
	idToken := codexTestJWT(map[string]any{"chatgpt_account_id": "acct"})
	accessToken := codexTestJWT(map[string]any{"exp": 4_102_444_800})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = fmt.Fprint(w, `{"device_auth_id":"device","user_code":"CODE","interval":"0"}`)
		case "/api/accounts/deviceauth/token":
			polls++
			if polls == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = fmt.Fprint(w, `{"authorization_code":"code","code_challenge":"challenge","code_verifier":"verifier"}`)
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token": idToken, "access_token": accessToken, "refresh_token": "refresh",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	login, err := StartCodexDeviceLogin(context.Background(), upstream.URL, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := CompleteCodexDeviceLogin(context.Background(), upstream.URL, upstream.Client(), login, filepath.Join(t.TempDir(), "auth.json")); err != nil {
		t.Fatal(err)
	}
	if polls != 2 {
		t.Fatalf("polls = %d, want 2", polls)
	}
}
