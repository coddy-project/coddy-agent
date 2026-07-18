package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Codex CLI ("codex login" / "codex auth") stores its credentials in
// ~/.codex/auth.json. In "chatgpt" mode the file holds OAuth tokens issued for a
// ChatGPT subscription; requests are routed through OpenAI's Codex backend using
// the access token as a bearer credential. This file reads those credentials and
// transparently refreshes the access token when it has expired.
const (
	// codexClientID is the public OAuth client id the Codex CLI registers with.
	codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	// codexTokenURL is the OAuth token endpoint used to refresh the access token.
	codexTokenURL = "https://auth.openai.com/oauth/token"
	// codexDefaultBaseURL is the Codex backend that serves the Responses API for
	// ChatGPT-authenticated sessions.
	codexDefaultBaseURL = "https://chatgpt.com/backend-api/codex"
	// codexAuthModeChatGPT is the auth_mode value for ChatGPT (OAuth) credentials.
	codexAuthModeChatGPT = "chatgpt"
	// codexRefreshSkew refreshes the access token this long before its expiry so a
	// request never starts with an about-to-expire token.
	codexRefreshSkew = 60 * time.Second
)

// codexTokens mirrors the "tokens" object in ~/.codex/auth.json.
type codexTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

// codexAuthFile mirrors the top-level structure of ~/.codex/auth.json.
type codexAuthFile struct {
	AuthMode     string      `json:"auth_mode"`
	OpenAIAPIKey *string     `json:"OPENAI_API_KEY"`
	Tokens       codexTokens `json:"tokens"`
	LastRefresh  string      `json:"last_refresh"`
}

// codexCredential is the resolved bearer credential for one request.
type codexCredential struct {
	AccessToken string
	AccountID   string
}

// codexHome returns the Codex home directory (~/.codex), honoring CODEX_HOME.
// Returns "" when the home directory cannot be determined.
func codexHome() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	h, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(h) == "" {
		return ""
	}
	return filepath.Join(h, ".codex")
}

// codexAuthPath returns the path to the Codex auth.json, honoring CODEX_HOME.
func codexAuthPath() string {
	home := codexHome()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "auth.json")
}

// codexModelsCachePath returns the path to the Codex models cache, honoring CODEX_HOME.
func codexModelsCachePath() string {
	home := codexHome()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "models_cache.json")
}

// codexAuthSource resolves (and refreshes) Codex ChatGPT credentials from disk.
// It is safe for concurrent use; refreshes are serialized so a burst of requests
// triggers at most one token exchange.
type codexAuthSource struct {
	path         string
	fallbackPath string
	httpClient   *http.Client
	tokenURL     string
	now          func() time.Time
}

// codexAuthMu serializes credential reads, refreshes, and rewrites across all
// provider instances in this process.
var codexAuthMu sync.Mutex

// newCodexAuthSource builds an auth source. An empty path defaults to
// codexAuthPath(); a nil httpClient defaults to http.DefaultClient.
func newCodexAuthSource(path string, httpClient *http.Client) *codexAuthSource {
	if strings.TrimSpace(path) == "" {
		path = codexAuthPath()
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &codexAuthSource{
		path:       path,
		httpClient: httpClient,
		tokenURL:   codexTokenURL,
		now:        time.Now,
	}
}

// newManagedCodexAuthSource prefers a Coddy-managed credential file and falls
// back to the user's Codex CLI login when the managed file does not exist.
func newManagedCodexAuthSource(path string, httpClient *http.Client) *codexAuthSource {
	s := newCodexAuthSource(path, httpClient)
	if strings.TrimSpace(path) != "" {
		fallback := codexAuthPath()
		if fallback != "" && filepath.Clean(fallback) != filepath.Clean(path) {
			s.fallbackPath = fallback
		}
	}
	return s
}

// Credential returns a usable ChatGPT credential, refreshing the access token in
// place (and rewriting auth.json) when it is expired or about to expire.
func (s *codexAuthSource) Credential(ctx context.Context) (codexCredential, error) {
	codexAuthMu.Lock()
	defer codexAuthMu.Unlock()

	auth, activePath, err := s.load()
	if err != nil {
		return codexCredential{}, err
	}
	if auth.AuthMode != "" && auth.AuthMode != codexAuthModeChatGPT {
		return codexCredential{}, fmt.Errorf("codex auth: only ChatGPT (OAuth) mode is supported, found auth_mode %q", auth.AuthMode)
	}
	if strings.TrimSpace(auth.Tokens.RefreshToken) == "" && strings.TrimSpace(auth.Tokens.AccessToken) == "" {
		return codexCredential{}, fmt.Errorf("codex auth: no ChatGPT tokens in %s (use Sign In or run `codex login`)", activePath)
	}

	if s.needsRefresh(auth.Tokens.AccessToken) {
		if strings.TrimSpace(auth.Tokens.RefreshToken) == "" {
			return codexCredential{}, fmt.Errorf("codex auth: access token expired and no refresh token available (run `codex login`)")
		}
		refreshed, rerr := s.refresh(ctx, auth.Tokens.RefreshToken)
		if rerr != nil {
			return codexCredential{}, rerr
		}
		auth.Tokens.AccessToken = refreshed.AccessToken
		if strings.TrimSpace(refreshed.IDToken) != "" {
			auth.Tokens.IDToken = refreshed.IDToken
		}
		if strings.TrimSpace(refreshed.RefreshToken) != "" {
			auth.Tokens.RefreshToken = refreshed.RefreshToken
		}
		if werr := s.save(activePath, auth); werr != nil {
			// A write failure must not break an otherwise usable token.
			return codexCredential{AccessToken: auth.Tokens.AccessToken, AccountID: auth.Tokens.AccountID}, nil
		}
	}

	return codexCredential{AccessToken: auth.Tokens.AccessToken, AccountID: auth.Tokens.AccountID}, nil
}

// load reads and parses the auth.json file.
func (s *codexAuthSource) load() (*codexAuthFile, string, error) {
	if strings.TrimSpace(s.path) == "" {
		return nil, "", fmt.Errorf("codex auth: could not locate auth.json (set CODEX_HOME)")
	}
	activePath := s.path
	data, err := os.ReadFile(activePath)
	if err != nil {
		if os.IsNotExist(err) && strings.TrimSpace(s.fallbackPath) != "" {
			activePath = s.fallbackPath
			data, err = os.ReadFile(activePath)
		}
		if err != nil && os.IsNotExist(err) {
			return nil, "", fmt.Errorf("codex auth: no OAuth credentials found (use Sign In or run `codex login`)")
		}
		if err != nil {
			return nil, "", fmt.Errorf("codex auth: read %s: %w", activePath, err)
		}
	}
	var auth codexAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, "", fmt.Errorf("codex auth: parse %s: %w", activePath, err)
	}
	return &auth, activePath, nil
}

// save writes the tokens and last_refresh back to auth.json while preserving any
// other fields present in the original file.
func (s *codexAuthSource) save(path string, auth *codexAuthFile) error {
	raw := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	tokensJSON, err := json.Marshal(auth.Tokens)
	if err != nil {
		return err
	}
	raw["tokens"] = tokensJSON
	if _, ok := raw["auth_mode"]; !ok {
		raw["auth_mode"] = json.RawMessage(fmt.Sprintf("%q", codexAuthModeChatGPT))
	}
	lastRefresh := s.now().UTC().Format(time.RFC3339Nano)
	raw["last_refresh"] = json.RawMessage(fmt.Sprintf("%q", lastRefresh))

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, out)
}

// CodexAuthStatus is the non-secret connection status exposed to the settings UI.
type CodexAuthStatus struct {
	Connected bool   `json:"connected"`
	Source    string `json:"source,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

// InspectCodexAuth reports whether a usable Coddy-managed credential exists. If
// it does not, the current Codex CLI login is reported as a compatibility fallback.
func InspectCodexAuth(path string) (CodexAuthStatus, error) {
	codexAuthMu.Lock()
	defer codexAuthMu.Unlock()

	paths := []struct {
		path   string
		source string
	}{{strings.TrimSpace(path), "coddy"}}
	if cliPath := codexAuthPath(); cliPath != "" && (path == "" || filepath.Clean(cliPath) != filepath.Clean(path)) {
		paths = append(paths, struct {
			path   string
			source string
		}{cliPath, "codex_cli"})
	}
	for _, candidate := range paths {
		if candidate.path == "" {
			continue
		}
		data, err := os.ReadFile(candidate.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return CodexAuthStatus{}, fmt.Errorf("codex auth: read %s: %w", candidate.path, err)
		}
		var auth codexAuthFile
		if err := json.Unmarshal(data, &auth); err != nil {
			return CodexAuthStatus{}, fmt.Errorf("codex auth: parse %s: %w", candidate.path, err)
		}
		connected := (auth.AuthMode == "" || auth.AuthMode == codexAuthModeChatGPT) &&
			(strings.TrimSpace(auth.Tokens.AccessToken) != "" || strings.TrimSpace(auth.Tokens.RefreshToken) != "")
		return CodexAuthStatus{Connected: connected, Source: candidate.source, AccountID: auth.Tokens.AccountID}, nil
	}
	return CodexAuthStatus{}, nil
}

// RemoveCodexAuth deletes a Coddy-managed credential without touching the
// Codex CLI fallback. It is serialized with reads and refresh writes.
func RemoveCodexAuth(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("codex auth: credential path is empty")
	}
	codexAuthMu.Lock()
	defer codexAuthMu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// needsRefresh reports whether the access token is missing, unparsable, or within
// codexRefreshSkew of its expiry.
func (s *codexAuthSource) needsRefresh(accessToken string) bool {
	if strings.TrimSpace(accessToken) == "" {
		return true
	}
	exp, ok := jwtExpiry(accessToken)
	if !ok {
		// Opaque or unparsable token: refresh only if we can; treat as usable here
		// and let the backend reject it if truly invalid.
		return false
	}
	return s.now().Add(codexRefreshSkew).After(exp)
}

// codexRefreshResponse is the subset of the OAuth token response we consume.
type codexRefreshResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// refresh exchanges a refresh token for a new access token via the OAuth endpoint.
func (s *codexAuthSource) refresh(ctx context.Context, refreshToken string) (*codexRefreshResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"client_id":     codexClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"scope":         "openid profile email",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("codex auth: build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex auth: refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("codex auth: token refresh failed with status %d (run `codex login`)", resp.StatusCode)
	}
	var out codexRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("codex auth: decode refresh response: %w", err)
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return nil, fmt.Errorf("codex auth: token refresh returned no access token")
	}
	return &out, nil
}

// jwtExpiry extracts the "exp" claim from a JWT access token. The second return
// value is false when the token is not a parsable JWT with an exp claim.
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}
