package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const codexDeviceLoginTimeout = 15 * time.Minute

// CodexDeviceLogin contains the public verification instructions plus the
// private device identifier needed by CompleteCodexDeviceLogin.
type CodexDeviceLogin struct {
	DeviceAuthID    string        `json:"-"`
	UserCode        string        `json:"user_code"`
	VerificationURL string        `json:"verification_url"`
	Interval        time.Duration `json:"-"`
}

// StartCodexDeviceLogin requests a device code from the official ChatGPT OAuth
// issuer. issuer is injectable so the HTTP integration can be tested locally.
func StartCodexDeviceLogin(ctx context.Context, issuer string, client *http.Client) (CodexDeviceLogin, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return CodexDeviceLogin{}, fmt.Errorf("codex auth: OAuth issuer is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	body, _ := json.Marshal(map[string]string{"client_id": codexClientID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/api/accounts/deviceauth/usercode", bytes.NewReader(body))
	if err != nil {
		return CodexDeviceLogin{}, fmt.Errorf("codex auth: build device request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return CodexDeviceLogin{}, fmt.Errorf("codex auth: request device code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CodexDeviceLogin{}, codexOAuthHTTPError("request device code", resp)
	}
	var raw struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		UserCodeAlt  string          `json:"usercode"`
		Interval     json.RawMessage `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return CodexDeviceLogin{}, fmt.Errorf("codex auth: decode device code: %w", err)
	}
	userCode := strings.TrimSpace(raw.UserCode)
	if userCode == "" {
		userCode = strings.TrimSpace(raw.UserCodeAlt)
	}
	if strings.TrimSpace(raw.DeviceAuthID) == "" || userCode == "" {
		return CodexDeviceLogin{}, fmt.Errorf("codex auth: device response is missing identifiers")
	}
	interval, err := parseCodexDeviceInterval(raw.Interval)
	if err != nil {
		return CodexDeviceLogin{}, err
	}
	return CodexDeviceLogin{
		DeviceAuthID:    raw.DeviceAuthID,
		UserCode:        userCode,
		VerificationURL: issuer + "/codex/device",
		Interval:        interval,
	}, nil
}

// CompleteCodexDeviceLogin waits for browser confirmation, exchanges the
// authorization code, and persists a Codex-compatible auth file at authPath.
func CompleteCodexDeviceLogin(ctx context.Context, issuer string, client *http.Client, login CodexDeviceLogin, authPath string) error {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" || strings.TrimSpace(authPath) == "" {
		return fmt.Errorf("codex auth: OAuth issuer and credential path are required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, codexDeviceLoginTimeout)
	defer cancel()

	deviceToken, err := pollCodexDeviceToken(ctx, issuer, client, login)
	if err != nil {
		return err
	}
	tokens, err := exchangeCodexDeviceToken(ctx, issuer, client, deviceToken)
	if err != nil {
		return err
	}
	accountID := codexAccountIDFromJWT(tokens.IDToken)
	auth := codexAuthFile{
		AuthMode:     codexAuthModeChatGPT,
		OpenAIAPIKey: nil,
		Tokens: codexTokens{
			IDToken:      tokens.IDToken,
			AccessToken:  tokens.AccessToken,
			RefreshToken: tokens.RefreshToken,
			AccountID:    accountID,
		},
		LastRefresh: time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return fmt.Errorf("codex auth: encode credentials: %w", err)
	}
	codexAuthMu.Lock()
	defer codexAuthMu.Unlock()
	if err := writePrivateFile(authPath, data); err != nil {
		return fmt.Errorf("codex auth: save credentials: %w", err)
	}
	return nil
}

type codexDeviceToken struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeChallenge     string `json:"code_challenge"`
	CodeVerifier      string `json:"code_verifier"`
}

func pollCodexDeviceToken(ctx context.Context, issuer string, client *http.Client, login CodexDeviceLogin) (codexDeviceToken, error) {
	interval := login.Interval
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	for {
		body, _ := json.Marshal(map[string]string{
			"device_auth_id": login.DeviceAuthID,
			"user_code":      login.UserCode,
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/api/accounts/deviceauth/token", bytes.NewReader(body))
		if err != nil {
			return codexDeviceToken{}, fmt.Errorf("codex auth: build device poll: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return codexDeviceToken{}, fmt.Errorf("codex auth: poll device login: %w", err)
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			_ = resp.Body.Close()
			select {
			case <-ctx.Done():
				return codexDeviceToken{}, fmt.Errorf("codex auth: device login timed out or was cancelled: %w", ctx.Err())
			case <-time.After(interval):
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			err := codexOAuthHTTPError("poll device login", resp)
			_ = resp.Body.Close()
			return codexDeviceToken{}, err
		}
		var token codexDeviceToken
		err = json.NewDecoder(resp.Body).Decode(&token)
		_ = resp.Body.Close()
		if err != nil {
			return codexDeviceToken{}, fmt.Errorf("codex auth: decode device token: %w", err)
		}
		if token.AuthorizationCode == "" || token.CodeVerifier == "" {
			return codexDeviceToken{}, fmt.Errorf("codex auth: device token response is incomplete")
		}
		return token, nil
	}
}

func exchangeCodexDeviceToken(ctx context.Context, issuer string, client *http.Client, device codexDeviceToken) (codexRefreshResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {device.AuthorizationCode},
		"redirect_uri":  {issuer + "/deviceauth/callback"},
		"client_id":     {codexClientID},
		"code_verifier": {device.CodeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return codexRefreshResponse{}, fmt.Errorf("codex auth: build token exchange: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return codexRefreshResponse{}, fmt.Errorf("codex auth: exchange device token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return codexRefreshResponse{}, codexOAuthHTTPError("exchange device token", resp)
	}
	var tokens codexRefreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return codexRefreshResponse{}, fmt.Errorf("codex auth: decode token exchange: %w", err)
	}
	if strings.TrimSpace(tokens.IDToken) == "" || strings.TrimSpace(tokens.AccessToken) == "" || strings.TrimSpace(tokens.RefreshToken) == "" {
		return codexRefreshResponse{}, fmt.Errorf("codex auth: token exchange response is incomplete")
	}
	return tokens, nil
}

func parseCodexDeviceInterval(raw json.RawMessage) (time.Duration, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 5 * time.Second, nil
	}
	var secondsString string
	if err := json.Unmarshal(raw, &secondsString); err == nil {
		seconds, err := strconv.ParseFloat(secondsString, 64)
		if err != nil || seconds < 0 {
			return 0, fmt.Errorf("codex auth: invalid device polling interval")
		}
		return time.Duration(seconds * float64(time.Second)), nil
	}
	var seconds float64
	if err := json.Unmarshal(raw, &seconds); err != nil || seconds < 0 {
		return 0, fmt.Errorf("codex auth: invalid device polling interval")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func codexAccountIDFromJWT(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	if accountID, ok := claims["chatgpt_account_id"].(string); ok {
		return strings.TrimSpace(accountID)
	}
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if accountID, ok := auth["chatgpt_account_id"].(string); ok {
			return strings.TrimSpace(accountID)
		}
	}
	return ""
}

func codexOAuthHTTPError(action string, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	message := strings.TrimSpace(string(snippet))
	if message == "" {
		return fmt.Errorf("codex auth: %s failed with HTTP %d", action, resp.StatusCode)
	}
	return fmt.Errorf("codex auth: %s failed with HTTP %d: %s", action, resp.StatusCode, message)
}
