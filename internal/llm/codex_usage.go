package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const (
	codexUsageRequestTimeout = 5 * time.Second
	codexUsageBodyLimit      = 256 << 10
)

type CodexUsage struct {
	PlanType             string                     `json:"plan_type"`
	RateLimit            *CodexUsageRateLimit       `json:"rate_limit"`
	AdditionalRateLimits []CodexAdditionalRateLimit `json:"additional_rate_limits"`
}

type CodexUsageRateLimit struct {
	Allowed         *bool             `json:"allowed"`
	LimitReached    *bool             `json:"limit_reached"`
	PrimaryWindow   *CodexUsageWindow `json:"primary_window"`
	SecondaryWindow *CodexUsageWindow `json:"secondary_window"`
}

type CodexUsageWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int      `json:"limit_window_seconds"`
	ResetAfterSeconds  *int     `json:"reset_after_seconds"`
	ResetAt            int64    `json:"reset_at"`
}

type CodexAdditionalRateLimit struct {
	LimitName      string               `json:"limit_name"`
	MeteredFeature string               `json:"metered_feature"`
	RateLimit      *CodexUsageRateLimit `json:"rate_limit"`
}

func CodexUsageForProvider(ctx context.Context, provider config.ProviderConfig, authPath string) (*CodexUsage, error) {
	hc, err := HTTPClientForProviderProxy(provider.Proxy)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "invalid proxy configuration"}
	}
	// A redirect would forward the OAuth token to another host. The client
	// is shared per proxy key, so the redirect policy is set on a copy.
	client := *hc
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	// The budget covers the OAuth refresh and the usage read together: an
	// expired token must not get a second timeout on top of the first.
	ctx, cancel := context.WithTimeout(ctx, codexUsageRequestTimeout)
	defer cancel()
	auth := newManagedCodexAuthSource(authPath, &client)
	cred, err := auth.Credential(ctx)
	if err != nil {
		kind := ProviderUsageUnavailable
		if strings.Contains(strings.ToLower(err.Error()), "no oauth credentials") || strings.Contains(strings.ToLower(err.Error()), "no chatgpt tokens") || strings.Contains(strings.ToLower(err.Error()), "auth_mode") {
			kind = ProviderUsageUnauthorized
		}
		return nil, &ProviderUsageError{Kind: kind, Detail: "credential unavailable"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL(), nil)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "build request"}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	req.Header.Set("originator", "codex_cli_rs")
	if strings.TrimSpace(cred.AccountID) != "" {
		req.Header.Set("ChatGPT-Account-Id", cred.AccountID)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "request failed"}
	}
	defer func() { _ = resp.Body.Close() }()
	body, truncated, readErr := readCodexUsageBody(resp.Body)
	if readErr != nil {
		return nil, &ProviderUsageError{Status: resp.StatusCode, Kind: ProviderUsageUnavailable, Detail: "read response"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := ProviderUsageUnavailable
		if resp.StatusCode == http.StatusUnauthorized {
			kind = ProviderUsageUnauthorized
		}
		return nil, &ProviderUsageError{
			Status:     resp.StatusCode,
			Kind:       kind,
			RetryAfter: parseUsageRetryAfter(resp.Header.Get("Retry-After")),
			Detail:     "upstream error",
		}
	}
	if truncated {
		return nil, &ProviderUsageError{Status: resp.StatusCode, Kind: ProviderUsageInvalid, Detail: "payload too large"}
	}
	usage, err := decodeCodexUsage(body)
	if err != nil {
		return nil, &ProviderUsageError{Status: resp.StatusCode, Kind: ProviderUsageInvalid, Detail: err.Error()}
	}
	return usage, nil
}

func CodexUsageFingerprint(provider config.ProviderConfig, authPath string) string {
	h := sha256.New()
	writeField := func(s string) {
		_, _ = io.WriteString(h, s)
		_, _ = h.Write([]byte{0})
	}
	writeField("type:codex")
	writeField("endpoint:" + codexUsageURL())
	writeField("proxy:" + strings.TrimSpace(provider.Proxy))
	codexAuthMu.Lock()
	defer codexAuthMu.Unlock()
	source, identity := codexUsageCredentialIdentityLocked(strings.TrimSpace(authPath))
	writeField("credential-source:" + source)
	writeField("credential:" + identity)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func codexUsageURL() string {
	base := strings.TrimRight(strings.TrimSpace(codexBaseURL()), "/")
	if strings.HasSuffix(base, "/codex") {
		base = strings.TrimRight(strings.TrimSuffix(base, "/codex"), "/")
	}
	return base + "/wham/usage"
}

func readCodexUsageBody(r io.Reader) ([]byte, bool, error) {
	limited := io.LimitReader(r, codexUsageBodyLimit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if len(body) > codexUsageBodyLimit {
		return body[:codexUsageBodyLimit], true, nil
	}
	return body, false, nil
}

func decodeCodexUsage(body []byte) (*CodexUsage, error) {
	var raw struct {
		PlanType             *string                     `json:"plan_type"`
		RateLimit            *codexUsageRateLimitPayload `json:"rate_limit"`
		AdditionalRateLimits []struct {
			LimitName      string                      `json:"limit_name"`
			MeteredFeature string                      `json:"metered_feature"`
			RateLimit      *codexUsageRateLimitPayload `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("undecodable payload")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing data after payload")
	}
	if raw.PlanType == nil || strings.TrimSpace(*raw.PlanType) == "" {
		return nil, fmt.Errorf("payload without plan_type")
	}
	out := &CodexUsage{PlanType: strings.TrimSpace(*raw.PlanType)}
	if raw.RateLimit != nil {
		rl, err := raw.RateLimit.toPublic()
		if err != nil {
			return nil, err
		}
		out.RateLimit = rl
	}
	if raw.AdditionalRateLimits != nil {
		out.AdditionalRateLimits = make([]CodexAdditionalRateLimit, 0, len(raw.AdditionalRateLimits))
		for i, add := range raw.AdditionalRateLimits {
			var rl *CodexUsageRateLimit
			if add.RateLimit != nil {
				converted, err := add.RateLimit.toPublic()
				if err != nil {
					return nil, fmt.Errorf("additional_rate_limits[%d]: %w", i, err)
				}
				rl = converted
			}
			out.AdditionalRateLimits = append(out.AdditionalRateLimits, CodexAdditionalRateLimit{
				LimitName:      add.LimitName,
				MeteredFeature: add.MeteredFeature,
				RateLimit:      rl,
			})
		}
	}
	return out, nil
}

type codexUsageRateLimitPayload struct {
	Allowed         *bool                    `json:"allowed"`
	LimitReached    *bool                    `json:"limit_reached"`
	PrimaryWindow   *codexUsageWindowPayload `json:"primary_window"`
	SecondaryWindow *codexUsageWindowPayload `json:"secondary_window"`
}

func (p *codexUsageRateLimitPayload) toPublic() (*CodexUsageRateLimit, error) {
	if p.Allowed == nil {
		return nil, fmt.Errorf("rate_limit without allowed")
	}
	if p.LimitReached == nil {
		return nil, fmt.Errorf("rate_limit without limit_reached")
	}
	out := &CodexUsageRateLimit{Allowed: p.Allowed, LimitReached: p.LimitReached}
	if p.PrimaryWindow != nil {
		w, err := p.PrimaryWindow.toPublic("primary_window")
		if err != nil {
			return nil, err
		}
		out.PrimaryWindow = w
	}
	if p.SecondaryWindow != nil {
		w, err := p.SecondaryWindow.toPublic("secondary_window")
		if err != nil {
			return nil, err
		}
		out.SecondaryWindow = w
	}
	return out, nil
}

type codexUsageWindowPayload struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *int     `json:"limit_window_seconds"`
	ResetAfterSeconds  *int     `json:"reset_after_seconds"`
	ResetAt            *int64   `json:"reset_at"`
}

func (p *codexUsageWindowPayload) toPublic(name string) (*CodexUsageWindow, error) {
	if p.LimitWindowSeconds == nil || *p.LimitWindowSeconds <= 0 {
		return nil, fmt.Errorf("%s without limit_window_seconds", name)
	}
	if p.UsedPercent == nil {
		return nil, fmt.Errorf("%s without used_percent", name)
	}
	if math.IsNaN(*p.UsedPercent) || math.IsInf(*p.UsedPercent, 0) || *p.UsedPercent < 0 || *p.UsedPercent > 100 {
		return nil, fmt.Errorf("%s used_percent out of range", name)
	}
	if p.ResetAfterSeconds != nil && *p.ResetAfterSeconds < 0 {
		return nil, fmt.Errorf("%s negative reset_after_seconds", name)
	}
	if p.ResetAt != nil && *p.ResetAt < 0 {
		return nil, fmt.Errorf("%s negative reset_at", name)
	}
	out := &CodexUsageWindow{
		UsedPercent:        p.UsedPercent,
		LimitWindowSeconds: *p.LimitWindowSeconds,
		ResetAfterSeconds:  p.ResetAfterSeconds,
	}
	if p.ResetAt != nil {
		out.ResetAt = *p.ResetAt
	}
	return out, nil
}

func codexUsageCredentialIdentityLocked(managedPath string) (string, string) {
	if managedPath == "" {
		managedPath = codexAuthPath()
	}
	if managedPath != "" {
		if data, err := os.ReadFile(managedPath); err == nil {
			return "managed:" + filepath.Clean(managedPath), codexUsageAuthIdentity(data)
		} else if !os.IsNotExist(err) {
			return "managed:" + filepath.Clean(managedPath), "read-error"
		}
	}
	cliPath := codexAuthPath()
	if cliPath != "" && (managedPath == "" || filepath.Clean(cliPath) != filepath.Clean(managedPath)) {
		if data, err := os.ReadFile(cliPath); err == nil {
			return "codex-cli:" + filepath.Clean(cliPath), codexUsageAuthIdentity(data)
		} else if !os.IsNotExist(err) {
			return "codex-cli:" + filepath.Clean(cliPath), "read-error"
		}
	}
	if managedPath != "" {
		return "missing-managed:" + filepath.Clean(managedPath), "missing"
	}
	if cliPath != "" {
		return "missing-codex-cli:" + filepath.Clean(cliPath), "missing"
	}
	return "missing", "missing"
}

func codexUsageAuthIdentity(data []byte) string {
	var auth codexAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return "malformed:" + hashSecret(data)
	}
	account := strings.TrimSpace(auth.Tokens.AccountID)
	// A JWT that names its user makes routine access-token rotation
	// irrelevant to pacing: the identity is the account, not the current
	// token bytes. Tokens without identity claims (an opaque string, a bare
	// test JWT) still track the raw material so a rotation invalidates.
	sub, user := codexUsageJWTIdentity(auth.Tokens.AccessToken)
	if sub == "" && user == "" {
		sub, user = codexUsageJWTIdentity(auth.Tokens.IDToken)
	}
	if sub != "" || user != "" {
		return hashSecret([]byte(strings.Join([]string{"jwt", account, sub, user}, "\x00")))
	}
	fields := []string{
		"auth_mode:" + strings.TrimSpace(auth.AuthMode),
		"account:" + account,
		"access:" + strings.TrimSpace(auth.Tokens.AccessToken),
		"refresh:" + strings.TrimSpace(auth.Tokens.RefreshToken),
		"id:" + strings.TrimSpace(auth.Tokens.IDToken),
	}
	if auth.OpenAIAPIKey != nil {
		fields = append(fields, "api_key:"+strings.TrimSpace(*auth.OpenAIAPIKey))
	}
	return hashSecret([]byte(strings.Join(fields, "\x00")))
}

// codexUsageJWTIdentity extracts the stable account identity of a JWT: the
// subject and the ChatGPT user id. Both empty means the token is not a JWT
// or carries no user claims.
func codexUsageJWTIdentity(token string) (sub, user string) {
	claims := jwtClaims(strings.TrimSpace(token))
	if claims == nil {
		return "", ""
	}
	sub, _ = claims["sub"].(string)
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		user, _ = auth["chatgpt_user_id"].(string)
	}
	return sub, user
}

func hashSecret(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
