package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// makeJWT builds a minimal unsigned-looking JWT whose payload carries the given
// expiry, enough for jwtExpiry to parse.
func makeJWT(exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(
		`{"exp":` + strconv.FormatInt(exp.Unix(), 10) + `}`))
	return header + "." + payload + ".sig"
}

// TestCodexAuthNoticesReportCredentialState covers the startup report: it must
// stay silent unless codex is actually configured, and must say out loud when a
// credential is missing or no longer refreshable.
func TestCodexAuthNoticesReportCredentialState(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir()) // no Codex CLI login in reach

	noCodex := &config.Config{
		Paths:     config.Paths{Home: t.TempDir()},
		Providers: []config.ProviderConfig{{Name: "openai", Type: "openai", APIKey: "k"}},
	}
	if got := CodexAuthNotices(noCodex); len(got) != 0 {
		t.Fatalf("notices without a codex provider = %+v, want none", got)
	}

	home := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{{Name: "codex", Type: "codex"}},
	}
	notices := CodexAuthNotices(cfg)
	if len(notices) != 1 || !notices[0].Warning || notices[0].Provider != "codex" {
		t.Fatalf("missing-credential notices = %+v, want one warning for codex", notices)
	}
	// The hint names the command that signs this provider in today; the
	// codex-only alias it used to name is no longer what the docs teach.
	if !strings.Contains(notices[0].Message, "coddy providers login codex") {
		t.Errorf("missing-credential message %q should point at the sign-in command", notices[0].Message)
	}

	authPath := config.CodexAuthPath(home, "codex")
	write := func(access, refresh string) {
		auth := codexAuthFile{AuthMode: codexAuthModeChatGPT, Tokens: codexTokens{
			AccessToken: access, RefreshToken: refresh, AccountID: "acct-1",
		}}
		data, _ := json.MarshalIndent(auth, "", "  ")
		if err := writePrivateFile(authPath, data); err != nil {
			t.Fatalf("write credential: %v", err)
		}
	}

	write(makeJWT(time.Now().Add(time.Hour)), "rt")
	notices = CodexAuthNotices(cfg)
	if len(notices) != 1 || notices[0].Warning {
		t.Fatalf("valid credential notices = %+v, want one informational line", notices)
	}

	write(makeJWT(time.Now().Add(-time.Hour)), "rt")
	notices = CodexAuthNotices(cfg)
	if len(notices) != 1 || notices[0].Warning {
		t.Fatalf("expired-but-refreshable notices = %+v, want an informational line", notices)
	}
	if !strings.Contains(notices[0].Message, "refresh") {
		t.Errorf("expired-but-refreshable message %q should mention the refresh", notices[0].Message)
	}

	write(makeJWT(time.Now().Add(-time.Hour)), "")
	notices = CodexAuthNotices(cfg)
	if len(notices) != 1 || !notices[0].Warning {
		t.Fatalf("expired-without-refresh notices = %+v, want a warning", notices)
	}
	if !strings.Contains(notices[0].Message, "expired") {
		t.Errorf("expired-without-refresh message %q should say the token expired", notices[0].Message)
	}
}

func writeCodexAuth(t *testing.T, dir string, auth codexAuthFile) string {
	t.Helper()
	path := filepath.Join(dir, "auth.json")
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	return path
}

func TestCodexAuthCredentialValidToken(t *testing.T) {
	dir := t.TempDir()
	path := writeCodexAuth(t, dir, codexAuthFile{
		AuthMode: codexAuthModeChatGPT,
		Tokens: codexTokens{
			AccessToken:  makeJWT(time.Now().Add(time.Hour)),
			RefreshToken: "rt-1",
			AccountID:    "acct-42",
		},
	})

	src := newCodexAuthSource(path, http.DefaultClient)
	// Fail the test if refresh is attempted for a still-valid token.
	src.tokenURL = "http://127.0.0.1:0/should-not-be-called"

	cred, err := src.Credential(context.Background())
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred.AccountID != "acct-42" {
		t.Errorf("AccountID = %q, want acct-42", cred.AccountID)
	}
	if cred.AccessToken == "" {
		t.Error("AccessToken is empty")
	}
}

func TestCodexAuthCredentialRefreshesExpiredToken(t *testing.T) {
	dir := t.TempDir()
	oldToken := makeJWT(time.Now().Add(-time.Hour)) // expired
	newToken := makeJWT(time.Now().Add(time.Hour))
	path := writeCodexAuth(t, dir, codexAuthFile{
		AuthMode: codexAuthModeChatGPT,
		Tokens: codexTokens{
			AccessToken:  oldToken,
			RefreshToken: "rt-old",
			AccountID:    "acct-7",
		},
	})

	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(codexRefreshResponse{
			AccessToken:  newToken,
			RefreshToken: "rt-new",
			IDToken:      "id-new",
		})
	}))
	defer srv.Close()

	src := newCodexAuthSource(path, http.DefaultClient)
	src.tokenURL = srv.URL

	cred, err := src.Credential(context.Background())
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred.AccessToken != newToken {
		t.Errorf("AccessToken not refreshed: got %q", cred.AccessToken)
	}
	if gotBody["grant_type"] != "refresh_token" || gotBody["refresh_token"] != "rt-old" {
		t.Errorf("unexpected refresh body: %+v", gotBody)
	}
	if gotBody["client_id"] != codexClientID {
		t.Errorf("client_id = %q, want %q", gotBody["client_id"], codexClientID)
	}

	// The refreshed tokens must be persisted back to auth.json.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var saved codexAuthFile
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("parse back: %v", err)
	}
	if saved.Tokens.AccessToken != newToken {
		t.Errorf("saved access token = %q, want refreshed", saved.Tokens.AccessToken)
	}
	if saved.Tokens.RefreshToken != "rt-new" {
		t.Errorf("saved refresh token = %q, want rt-new", saved.Tokens.RefreshToken)
	}
}

func TestCodexAuthRejectsNonChatGPTMode(t *testing.T) {
	dir := t.TempDir()
	path := writeCodexAuth(t, dir, codexAuthFile{
		AuthMode: "apikey",
		Tokens:   codexTokens{AccessToken: makeJWT(time.Now().Add(time.Hour))},
	})
	src := newCodexAuthSource(path, http.DefaultClient)
	if _, err := src.Credential(context.Background()); err == nil {
		t.Fatal("expected error for non-chatgpt auth_mode, got nil")
	}
}

func TestCodexAuthMissingFile(t *testing.T) {
	src := newCodexAuthSource(filepath.Join(t.TempDir(), "nope.json"), http.DefaultClient)
	if _, err := src.Credential(context.Background()); err == nil {
		t.Fatal("expected error for missing auth.json, got nil")
	}
}

func TestJWTExpiry(t *testing.T) {
	exp := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	got, ok := jwtExpiry(makeJWT(exp))
	if !ok {
		t.Fatal("jwtExpiry returned ok=false for valid token")
	}
	if !got.Equal(exp) {
		t.Errorf("exp = %v, want %v", got, exp)
	}
	if _, ok := jwtExpiry("not-a-jwt"); ok {
		t.Error("jwtExpiry returned ok=true for non-JWT")
	}
}

// codexConfigTestBackend serves one Codex model catalog and points the process
// at itself for the duration of the test.
func codexConfigTestBackend(t *testing.T, catalog string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalog))
	}))
	t.Cleanup(srv.Close)
	t.Setenv(EnvCodexBaseURL, srv.URL)
}

// codexConfigTestHome writes a config.yaml plus a Codex credential and returns
// the loaded config and the credential path.
func codexConfigTestHome(t *testing.T, yaml string) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	authPath := config.CodexAuthPath(home, "codex")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		t.Fatal(err)
	}
	token := makeJWT(time.Now().Add(time.Hour))
	auth := `{"auth_mode":"chatgpt","tokens":{"id_token":"` + token +
		`","access_token":"` + token + `","refresh_token":"rt","account_id":"acct"}}`
	if err := os.WriteFile(authPath, []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg, authPath
}

// TestApplyCodexLoginKeepsWhatTheOperatorAlreadyChose: the login publishes the
// catalog, it does not take the config over. An existing provider row, an
// already listed model, and a chosen agent.model all survive untouched.
func TestApplyCodexLoginKeepsWhatTheOperatorAlreadyChose(t *testing.T) {
	codexConfigTestBackend(t, `{"models":[
      {"slug":"gpt-6-astra","display_name":"GPT-6-Astra","visibility":"list","priority":1},
      {"slug":"gpt-5.6-sol","display_name":"GPT-5.6-Sol","visibility":"list","priority":6}
    ]}`)
	cfg, authPath := codexConfigTestHome(t, "providers:\n  - name: codex\n    type: codex\n"+
		"models:\n  - model: codex/gpt-5.6-sol\n    max_tokens: 4242\n"+
		"agent:\n  model: codex/gpt-5.6-sol\n")

	added, err := ApplyCodexLoginToConfig(context.Background(), cfg, "codex", authPath, "")
	if err != nil {
		t.Fatalf("ApplyCodexLoginToConfig: %v", err)
	}
	joined := strings.Join(added, ",")
	if strings.Contains(joined, "provider codex") {
		t.Errorf("an existing provider row must not be re-added: %v", added)
	}
	if !strings.Contains(joined, "model codex/gpt-6-astra") {
		t.Errorf("the missing catalog model should be added: %v", added)
	}
	reloaded, err := config.LoadFromCLI(config.CLIPaths{Home: cfg.Paths.Home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Agent.Model != "codex/gpt-5.6-sol" {
		t.Errorf("agent.model = %q, want the operator's choice codex/gpt-5.6-sol", reloaded.Agent.Model)
	}
	kept := reloaded.FindModelEntry("codex/gpt-5.6-sol")
	if kept == nil || kept.MaxTokens != 4242 {
		t.Errorf("the existing model entry lost its settings: %+v", kept)
	}
}

// TestApplyCodexLoginSkipsUnsafeModelIDs: catalog slugs are interpolated into
// UCI config paths, so a backend must not be able to smuggle selector syntax
// through the model list.
func TestApplyCodexLoginSkipsUnsafeModelIDs(t *testing.T) {
	codexConfigTestBackend(t, `{"models":[
      {"slug":"gpt-5.6-sol","display_name":"ok","visibility":"list","priority":6},
      {"slug":"evil]x.hack=1","display_name":"bad","visibility":"list","priority":2},
      {"slug":"also/bad","display_name":"bad","visibility":"list","priority":3}
    ]}`)
	cfg, authPath := codexConfigTestHome(t, "providers: []\nmodels: []\n")

	added, err := ApplyCodexLoginToConfig(context.Background(), cfg, "codex", authPath, "")
	if err != nil {
		t.Fatalf("ApplyCodexLoginToConfig: %v", err)
	}
	joined := strings.Join(added, ",")
	if !strings.Contains(joined, "model codex/gpt-5.6-sol") {
		t.Fatalf("safe model missing from %q", joined)
	}
	if strings.Contains(joined, "evil") || strings.Contains(joined, "also/bad") {
		t.Fatalf("unsafe model ids must be skipped, got %q", joined)
	}
	reloaded, err := config.LoadFromCLI(config.CLIPaths{Home: cfg.Paths.Home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	// The top-priority slug is unsafe, so the default falls to the best usable one.
	if reloaded.Agent.Model != "codex/gpt-5.6-sol" {
		t.Errorf("agent.model = %q, want codex/gpt-5.6-sol", reloaded.Agent.Model)
	}
}

// TestApplyCodexLoginReportsACatalogFailure: a sign-in whose catalog cannot be
// read must say so rather than write a provider row with no models behind it.
func TestApplyCodexLoginReportsACatalogFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv(EnvCodexBaseURL, srv.URL)
	cfg, authPath := codexConfigTestHome(t, "providers: []\nmodels: []\n")

	if _, err := ApplyCodexLoginToConfig(context.Background(), cfg, "codex", authPath, ""); err == nil {
		t.Fatal("expected an error when the catalog cannot be fetched, got nil")
	}
	reloaded, err := config.LoadFromCLI(config.CLIPaths{Home: cfg.Paths.Home})
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.FindProvider("codex") != nil {
		t.Error("a failed catalog fetch must not leave a provider row behind")
	}
}
