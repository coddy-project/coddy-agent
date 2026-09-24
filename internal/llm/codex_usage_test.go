package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestProviderUsageErrorMessageIsProviderNeutral(t *testing.T) {
	err := (&ProviderUsageError{Status: http.StatusTooManyRequests, Kind: ProviderUsageUnavailable, RetryAfter: 3 * time.Second, Detail: "retry later"}).Error()
	if !strings.Contains(err, "provider usage: unavailable (HTTP 429): retry later") {
		t.Fatalf("Error() = %q", err)
	}
	if strings.Contains(strings.ToLower(err), "codex") || strings.Contains(strings.ToLower(err), "neuraldeep") {
		t.Fatalf("provider-neutral error leaked provider name: %q", err)
	}
}

func TestCodexUsageHappyPayloadPreservesRawWindowsAndHeaders(t *testing.T) {
	authPath := writeCodexUsageAuth(t, t.TempDir(), "access-happy", "refresh-happy", "acct-happy")
	var gotPath, gotAuth, gotAccount, gotOriginator string
	var gotBody int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("ChatGPT-Account-Id")
		gotOriginator = r.Header.Get("originator")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"plan_type":"plus",
			"rate_limit":{
				"allowed":true,
				"limit_reached":false,
				"primary_window":{"used_percent":12.5,"limit_window_seconds":604800,"reset_after_seconds":3600,"reset_at":1893456000},
				"secondary_window":{"used_percent":0,"limit_window_seconds":86400,"reset_at":1893370000}
			},
			"additional_rate_limits":[{"limit_name":"weekly","metered_feature":"codex","rate_limit":{"allowed":false,"limit_reached":true}}]
		}`))
		atomic.AddInt64(&gotBody, 1)
	}))
	defer srv.Close()
	t.Setenv(EnvCodexBaseURL, srv.URL+"/backend-api/codex")

	usage, err := CodexUsageForProvider(context.Background(), config.ProviderConfig{
		Name:    "codex",
		Type:    "codex",
		APIBase: "https://attacker.invalid/ignored",
		APIKey:  "ignored-key",
	}, authPath, true)
	if err != nil {
		t.Fatalf("CodexUsageForProvider: %v", err)
	}
	if gotPath != "/backend-api/wham/usage" {
		t.Fatalf("request path = %q, want /backend-api/wham/usage", gotPath)
	}
	if gotAuth != "Bearer access-happy" || gotAccount != "acct-happy" || gotOriginator != "codex_cli_rs" {
		t.Fatalf("headers Authorization=%q Account=%q originator=%q", gotAuth, gotAccount, gotOriginator)
	}
	if got := atomic.LoadInt64(&gotBody); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	if usage.PlanType != "plus" {
		t.Fatalf("PlanType = %q", usage.PlanType)
	}
	if usage.RateLimit == nil || usage.RateLimit.PrimaryWindow == nil || usage.RateLimit.SecondaryWindow == nil {
		t.Fatalf("rate limit windows not decoded: %+v", usage.RateLimit)
	}
	if usage.RateLimit.Allowed == nil || !*usage.RateLimit.Allowed || usage.RateLimit.LimitReached == nil || *usage.RateLimit.LimitReached {
		t.Fatalf("allowed/limit_reached pointers not preserved: %+v", usage.RateLimit)
	}
	if usage.RateLimit.PrimaryWindow.UsedPercent == nil || *usage.RateLimit.PrimaryWindow.UsedPercent != 12.5 {
		t.Fatalf("primary used percent = %#v", usage.RateLimit.PrimaryWindow.UsedPercent)
	}
	if usage.RateLimit.PrimaryWindow.LimitWindowSeconds != 604800 {
		t.Fatalf("primary duration = %d", usage.RateLimit.PrimaryWindow.LimitWindowSeconds)
	}
	if usage.RateLimit.PrimaryWindow.ResetAfterSeconds == nil || *usage.RateLimit.PrimaryWindow.ResetAfterSeconds != 3600 {
		t.Fatalf("reset_after_seconds = %#v", usage.RateLimit.PrimaryWindow.ResetAfterSeconds)
	}
	if usage.RateLimit.SecondaryWindow.UsedPercent == nil || *usage.RateLimit.SecondaryWindow.UsedPercent != 0 {
		t.Fatalf("zero used_percent should be present pointer, got %#v", usage.RateLimit.SecondaryWindow.UsedPercent)
	}
	if len(usage.AdditionalRateLimits) != 1 || usage.AdditionalRateLimits[0].LimitName != "weekly" || usage.AdditionalRateLimits[0].MeteredFeature != "codex" {
		t.Fatalf("additional limits = %+v", usage.AdditionalRateLimits)
	}
}

func TestCodexUsagePlanOnlyAndNullWindowsAreValid(t *testing.T) {
	authPath := writeCodexUsageAuth(t, t.TempDir(), "access-plan", "refresh-plan", "")
	srv := codexUsageServer(t, `{"plan_type":"free","rate_limit":null,"additional_rate_limits":null}`)
	defer srv.Close()
	t.Setenv(EnvCodexBaseURL, srv.URL+"/prefix")

	usage, err := CodexUsageForProvider(context.Background(), config.ProviderConfig{Name: "codex", Type: "codex"}, authPath, true)
	if err != nil {
		t.Fatalf("CodexUsageForProvider: %v", err)
	}
	if usage.PlanType != "free" || usage.RateLimit != nil || usage.AdditionalRateLimits != nil {
		t.Fatalf("plan-only usage = %+v", usage)
	}
}

func TestCodexUsageManagedFallbackAndTransientRefreshFailure(t *testing.T) {
	cliHome := t.TempDir()
	t.Setenv("CODEX_HOME", cliHome)
	cliPath := writeCodexUsageAuth(t, cliHome, "cli-access", "cli-refresh", "acct-cli")
	managedDir := t.TempDir()
	managedPath := filepath.Join(managedDir, "auth.json")
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"plan_type":"team"}`))
	}))
	defer srv.Close()
	t.Setenv(EnvCodexBaseURL, srv.URL+"/backend-api/codex")

	usage, err := CodexUsageForProvider(context.Background(), config.ProviderConfig{Name: "codex", Type: "codex"}, managedPath, true)
	if err != nil {
		t.Fatalf("fallback CodexUsageForProvider: %v", err)
	}
	if usage.PlanType != "team" || gotAuth != "Bearer cli-access" {
		t.Fatalf("fallback usage=%+v auth=%q (cli path %s)", usage, gotAuth, cliPath)
	}

	// A managed file exists, so it wins over the CLI fallback even when refresh is
	// cut short. The failure is transient/unavailable, not a sticky unauthorized.
	writeCodexUsageAuth(t, managedDir, makeJWT(time.Now().Add(-time.Hour)), "managed-refresh", "acct-managed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = CodexUsageForProvider(ctx, config.ProviderConfig{Name: "codex", Type: "codex"}, managedPath, true)
	var ue *ProviderUsageError
	if !errors.As(err, &ue) || ue.Kind != ProviderUsageUnavailable {
		t.Fatalf("refresh cancellation error = %#v, want unavailable ProviderUsageError", err)
	}
}

func TestCodexUsageErrorsStatusesRetryAndNoSecretLeak(t *testing.T) {
	authPath := writeCodexUsageAuth(t, t.TempDir(), "secret-access-token", "secret-refresh-token", "acct-secret")
	tests := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		wantKind   string
		wantRetry  bool
	}{
		{"unauthorized", http.StatusUnauthorized, `{"detail":"bad token secret-access-token"}`, "", ProviderUsageUnauthorized, false},
		{"forbiddenUnavailable", http.StatusForbidden, `challenge says secret-access-token`, "", ProviderUsageUnavailable, false},
		{"rateLimitRetry", http.StatusTooManyRequests, `slow down secret-access-token`, "2", ProviderUsageUnavailable, true},
		{"server", http.StatusBadGateway, `upstream secret-access-token`, "", ProviderUsageUnavailable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			t.Setenv(EnvCodexBaseURL, srv.URL+"/backend-api/codex")
			_, err := CodexUsageForProvider(context.Background(), config.ProviderConfig{Name: "codex", Type: "codex"}, authPath, true)
			var ue *ProviderUsageError
			if !errors.As(err, &ue) || ue.Status != tt.status || ue.Kind != tt.wantKind {
				t.Fatalf("error = %#v, want status %d kind %s", err, tt.status, tt.wantKind)
			}
			if tt.wantRetry && ue.RetryAfter <= 0 {
				t.Fatalf("RetryAfter = %v, want positive", ue.RetryAfter)
			}
			if strings.Contains(err.Error(), "secret-access-token") || strings.Contains(err.Error(), "secret-refresh-token") || strings.Contains(ue.Detail, "secret-access-token") {
				t.Fatalf("error leaked secret: %q detail=%q", err.Error(), ue.Detail)
			}
		})
	}
}

func TestCodexUsageRejectsMalformedPayloads(t *testing.T) {
	authPath := writeCodexUsageAuth(t, t.TempDir(), "access-invalid", "refresh-invalid", "")
	cases := map[string]string{
		"missingPlan":           `{"rate_limit":null}`,
		"emptyPlan":             `{"plan_type":"   "}`,
		"missingAllowed":        `{"plan_type":"plus","rate_limit":{"limit_reached":false}}`,
		"missingLimitReached":   `{"plan_type":"plus","rate_limit":{"allowed":true}}`,
		"missingWindowDuration": `{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":20}}}`,
		"nanPercent":            `{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":1e999,"limit_window_seconds":10}}}`,
		"negativePercent":       `{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":-1,"limit_window_seconds":10}}}`,
		"aboveHundredPercent":   `{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":101,"limit_window_seconds":10}}}`,
		"truncatedBody":         `{"plan_type":"plus"`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := codexUsageServer(t, body)
			defer srv.Close()
			t.Setenv(EnvCodexBaseURL, srv.URL+"/backend-api/codex")
			_, err := CodexUsageForProvider(context.Background(), config.ProviderConfig{Name: "codex", Type: "codex"}, authPath, true)
			var ue *ProviderUsageError
			if !errors.As(err, &ue) || ue.Kind != ProviderUsageInvalid {
				t.Fatalf("error = %#v, want invalid ProviderUsageError", err)
			}
		})
	}
}

func TestCodexUsageBlocksRedirectsAndHonorsCancellation(t *testing.T) {
	authPath := writeCodexUsageAuth(t, t.TempDir(), "access-redirect", "refresh-redirect", "acct-redirect")
	var redirected int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&redirected, 1)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("redirect target received Authorization header %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"plan_type":"leaked"}`))
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
	}))
	defer redirector.Close()
	t.Setenv(EnvCodexBaseURL, redirector.URL+"/backend-api/codex")

	_, err := CodexUsageForProvider(context.Background(), config.ProviderConfig{Name: "codex", Type: "codex"}, authPath, true)
	var ue *ProviderUsageError
	if !errors.As(err, &ue) || ue.Kind != ProviderUsageUnavailable || ue.Status != http.StatusFound {
		t.Fatalf("redirect error = %#v", err)
	}
	if got := atomic.LoadInt64(&redirected); got != 0 {
		t.Fatalf("redirect target requests = %d, want 0", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = CodexUsageForProvider(ctx, config.ProviderConfig{Name: "codex", Type: "codex"}, authPath, true)
	if !errors.As(err, &ue) || ue.Kind != ProviderUsageUnavailable {
		t.Fatalf("cancellation error = %#v, want unavailable ProviderUsageError", err)
	}
}

func TestCodexUsageFingerprintTracksRelevantCredentialEndpointAndProxy(t *testing.T) {
	dir := t.TempDir()
	authPath := writeCodexUsageAuth(t, dir, "access-one", "refresh-one", "acct-one")
	t.Setenv(EnvCodexBaseURL, "https://example.test/backend-api/codex")
	provider := config.ProviderConfig{Name: "codex", Type: "codex", APIBase: "https://ignored.invalid", APIKey: "ignored", Proxy: "none"}
	base := CodexUsageFingerprint(provider, authPath, true)
	if base == "" {
		t.Fatal("fingerprint is empty")
	}
	if strings.Contains(base, "access-one") || strings.Contains(base, "refresh-one") || strings.Contains(base, "acct-one") {
		t.Fatalf("fingerprint exposes secret/account material: %q", base)
	}
	provider.APIBase = "https://changed-ignored.invalid"
	provider.APIKey = "changed-ignored"
	if got := CodexUsageFingerprint(provider, authPath, true); got != base {
		t.Fatalf("fingerprint changed for ignored api_base/api_key: %q vs %q", got, base)
	}

	writeCodexUsageAuthRaw(t, authPath, codexAuthFile{AuthMode: codexAuthModeChatGPT, LastRefresh: "later", Tokens: codexTokens{AccessToken: "access-one", RefreshToken: "refresh-one", AccountID: "acct-one"}})
	if got := CodexUsageFingerprint(provider, authPath, true); got != base {
		t.Fatalf("fingerprint changed for irrelevant last_refresh: %q vs %q", got, base)
	}
	writeCodexUsageAuthRaw(t, authPath, codexAuthFile{AuthMode: codexAuthModeChatGPT, Tokens: codexTokens{AccessToken: "access-two", RefreshToken: "refresh-one", AccountID: "acct-one"}})
	if got := CodexUsageFingerprint(provider, authPath, true); got == base {
		t.Fatalf("fingerprint did not change after access credential rotation")
	}
	writeCodexUsageAuthRaw(t, authPath, codexAuthFile{AuthMode: codexAuthModeChatGPT, Tokens: codexTokens{AccessToken: "access-one", RefreshToken: "refresh-one", AccountID: "acct-two"}})
	if got := CodexUsageFingerprint(provider, authPath, true); got == base {
		t.Fatalf("fingerprint did not change after account id change")
	}
	provider.Proxy = "inherit"
	if got := CodexUsageFingerprint(provider, authPath, true); got == base {
		t.Fatalf("fingerprint did not change after proxy change")
	}
	t.Setenv(EnvCodexBaseURL, "https://example.test/other")
	if got := CodexUsageFingerprint(config.ProviderConfig{Name: "codex", Type: "codex", Proxy: "none"}, authPath, true); got == base {
		t.Fatalf("fingerprint did not change after effective endpoint change")
	}
}

func TestCodexUsageFingerprintManagedFallbackAndMissingFiles(t *testing.T) {
	cliHome := t.TempDir()
	t.Setenv("CODEX_HOME", cliHome)
	managedPath := filepath.Join(t.TempDir(), "auth.json")
	missing := CodexUsageFingerprint(config.ProviderConfig{Name: "codex", Type: "codex"}, managedPath, true)
	if missing == "" {
		t.Fatal("missing credential fingerprint should still be deterministic")
	}
	writeCodexUsageAuth(t, cliHome, "cli-access", "cli-refresh", "acct-cli")
	fallback := CodexUsageFingerprint(config.ProviderConfig{Name: "codex", Type: "codex"}, managedPath, true)
	if fallback == missing {
		t.Fatalf("fingerprint did not change when CLI fallback appeared")
	}
	if err := os.MkdirAll(filepath.Dir(managedPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, []byte(`{malformed`), 0o600); err != nil {
		t.Fatal(err)
	}
	managedMalformed := CodexUsageFingerprint(config.ProviderConfig{Name: "codex", Type: "codex"}, managedPath, true)
	if managedMalformed == fallback || managedMalformed == missing {
		t.Fatalf("managed malformed file should win over fallback and missing: missing=%q fallback=%q managed=%q", missing, fallback, managedMalformed)
	}
}

func codexUsageServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func writeCodexUsageAuth(t *testing.T, dir, access, refresh, account string) string {
	t.Helper()
	path := filepath.Join(dir, "auth.json")
	writeCodexUsageAuthRaw(t, path, codexAuthFile{AuthMode: codexAuthModeChatGPT, Tokens: codexTokens{AccessToken: access, RefreshToken: refresh, AccountID: account}})
	return path
}

func writeCodexUsageAuthRaw(t *testing.T, path string, auth codexAuthFile) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
}

func TestCodexUsageErrorDetailsNeverEchoUpstream(t *testing.T) {
	const opaque = "sk-x9Q7p6a2 acct-31A95 person@example.test"
	authPath := writeCodexUsageAuth(t, t.TempDir(), "usable", "renew", "workspace")
	for _, body := range []string{opaque, `{"detail":"` + opaque + `"}`, `{"error":{"message":"` + opaque + `"}}`} {
		t.Run(body, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			t.Setenv(EnvCodexBaseURL, srv.URL)
			_, err := CodexUsageForProvider(context.Background(), config.ProviderConfig{}, authPath, true)
			var ue *ProviderUsageError
			if !errors.As(err, &ue) || ue.Status != 429 || ue.Kind != ProviderUsageUnavailable || ue.RetryAfter != 3*time.Second {
				t.Fatalf("error = %#v", err)
			}
			if ue.Detail != "upstream error" || strings.Contains(err.Error(), opaque) {
				t.Fatalf("unsafe detail: %q", err)
			}
		})
	}
	for _, stage := range []string{"proxy", "request"} {
		t.Run(stage, func(t *testing.T) {
			provider := config.ProviderConfig{Proxy: "invalid-" + opaque}
			if stage == "request" {
				provider = codexUsageTestTransport(t, codexUsageRoundTripper(func(r *http.Request) (*http.Response, error) {
					return nil, errors.New(opaque)
				}))
			}
			t.Setenv(EnvCodexBaseURL, "https://example.test/"+url.PathEscape(opaque))
			_, err := CodexUsageForProvider(context.Background(), provider, authPath, true)
			var ue *ProviderUsageError
			if !errors.As(err, &ue) || ue.Kind != ProviderUsageUnavailable {
				t.Fatalf("error = %#v", err)
			}
			want := "request failed"
			if stage == "proxy" {
				want = "invalid proxy configuration"
			}
			if ue.Detail != want {
				t.Fatalf("unsafe %s detail: %q", stage, ue.Detail)
			}
		})
	}
}

func TestCodexUsageStrictPayloadAndWindowValidation(t *testing.T) {
	for _, suffix := range []string{"junk", `{}`, `null`} {
		if _, err := decodeCodexUsage([]byte(`{"plan_type":"plus"}` + suffix)); err == nil {
			t.Errorf("accepted trailing data %q", suffix)
		}
	}
	for _, window := range []string{
		`{"limit_window_seconds":60}`,
		`{"limit_window_seconds":60,"used_percent":null}`,
		`{"limit_window_seconds":60,"used_percent":0,"reset_at":-1}`,
		`{"limit_window_seconds":60,"used_percent":0,"reset_after_seconds":-1}`,
	} {
		for _, field := range []string{"primary_window", "secondary_window"} {
			limit := `{"allowed":true,"limit_reached":false,"` + field + `":` + window + `}`
			for _, payload := range []string{
				`{"plan_type":"plus","rate_limit":` + limit + `}`,
				`{"plan_type":"plus","additional_rate_limits":[{"rate_limit":` + limit + `}]}`,
			} {
				if _, err := decodeCodexUsage([]byte(payload)); err == nil {
					t.Errorf("accepted invalid window: %s", payload)
				}
			}
		}
	}
	for _, reset := range []string{"", `,"reset_at":null,"reset_after_seconds":null`, `,"reset_at":0,"reset_after_seconds":0`} {
		payload := `{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"limit_window_seconds":60,"used_percent":0` + reset + `}}}`
		if _, err := decodeCodexUsage([]byte(payload)); err != nil {
			t.Errorf("optional/zero resets rejected: %v", err)
		}
	}
}

type codexUsageRoundTripper func(*http.Request) (*http.Response, error)

func (f codexUsageRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func codexUsageTestTransport(t *testing.T, rt http.RoundTripper) config.ProviderConfig {
	t.Helper()
	key := "http://usage-test.invalid/" + url.PathEscape(t.Name())
	transportsMu.Lock()
	transports[key] = rt
	transportsMu.Unlock()
	t.Cleanup(func() {
		transportsMu.Lock()
		delete(transports, key)
		transportsMu.Unlock()
	})
	return config.ProviderConfig{Type: "codex", Proxy: key}
}

func TestCodexUsageRefreshAndUsageShareDeadline(t *testing.T) {
	authPath := writeCodexUsageAuth(t, t.TempDir(), makeJWT(time.Now().Add(-time.Hour)), "renew", "workspace")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = w.Write([]byte(`{"access_token":"renewed"}`))
			return
		}
		_, _ = w.Write([]byte(`{"plan_type":"plus"}`))
	}))
	defer srv.Close()
	local, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var deadlines []time.Time
	provider := codexUsageTestTransport(t, codexUsageRoundTripper(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > codexUsageRequestTimeout {
			t.Errorf("%s lacks bounded usage deadline: %v", r.URL.Path, deadline)
		}
		deadlines = append(deadlines, deadline)
		clone := r.Clone(r.Context())
		clone.URL.Scheme, clone.URL.Host = local.Scheme, local.Host
		return srv.Client().Transport.RoundTrip(clone)
	}))
	if _, err := CodexUsageForProvider(context.Background(), provider, authPath, true); err != nil {
		t.Fatal(err)
	}
	if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("refresh and usage deadlines differ: %v", deadlines)
	}
}

func TestCodexUsageRefreshHonorsShortContextAndCancellation(t *testing.T) {
	for _, cancelNow := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelNow), func(t *testing.T) {
			authPath := writeCodexUsageAuth(t, t.TempDir(), makeJWT(time.Now().Add(-time.Hour)), "renew", "workspace")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			provider := codexUsageTestTransport(t, codexUsageRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != codexTokenURL {
					t.Errorf("unexpected request: %s", r.URL)
				}
				if cancelNow {
					cancel()
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			}))
			started := time.Now()
			_, err := CodexUsageForProvider(ctx, provider, authPath, true)
			var ue *ProviderUsageError
			if !errors.As(err, &ue) || ue.Kind != ProviderUsageUnavailable || ue.Detail != "credential unavailable" {
				t.Fatalf("error = %#v", err)
			}
			if time.Since(started) > time.Second {
				t.Fatal("refresh did not stop promptly")
			}
		})
	}
}

func TestCodexUsageFingerprintStableJWTIdentity(t *testing.T) {
	jwt := func(sub, user string, expiry int) string {
		claims, err := json.Marshal(map[string]any{"sub": sub, "exp": expiry, "https://api.openai.com/auth": map[string]string{"chatgpt_user_id": user}})
		if err != nil {
			t.Fatal(err)
		}
		return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".c2ln"
	}
	for _, identity := range []struct{ sub, user string }{{"subject-a", ""}, {"", "user-a"}, {"subject-a", "user-a"}} {
		t.Run(identity.sub+identity.user, func(t *testing.T) {
			dir := t.TempDir()
			path := writeCodexUsageAuth(t, dir, jwt(identity.sub, identity.user, 100), "renew-one", "workspace-a")
			provider := config.ProviderConfig{Type: "codex"}
			base := CodexUsageFingerprint(provider, path, true)
			writeCodexUsageAuthRaw(t, path, codexAuthFile{AuthMode: codexAuthModeChatGPT, Tokens: codexTokens{AccessToken: jwt(identity.sub, identity.user, 200), RefreshToken: "renew-two", IDToken: "rotated-id", AccountID: "workspace-a"}})
			if got := CodexUsageFingerprint(provider, path, true); got != base {
				t.Error("JWT rotation changed stable identity")
			}
			writeCodexUsageAuth(t, dir, jwt(identity.sub, identity.user, 100), "renew-one", "workspace-b")
			if got := CodexUsageFingerprint(provider, path, true); got == base {
				t.Error("account change kept identity")
			}
			for _, changed := range []string{jwt("subject-b", identity.user, 100), jwt(identity.sub, "user-b", 100)} {
				writeCodexUsageAuth(t, dir, changed, "renew-one", "workspace-a")
				if got := CodexUsageFingerprint(provider, path, true); got == base {
					t.Error("user change kept identity in same workspace")
				}
			}
		})
	}
}

func TestCodexUsageFingerprintJWTWithoutIdentityStillTracksRotation(t *testing.T) {
	dir := t.TempDir()
	path := writeCodexUsageAuth(t, dir, makeJWT(time.Unix(100, 0)), "renew", "workspace")
	base := CodexUsageFingerprint(config.ProviderConfig{}, path, true)
	writeCodexUsageAuth(t, dir, makeJWT(time.Unix(200, 0)), "renew", "workspace")
	if CodexUsageFingerprint(config.ProviderConfig{}, path, true) == base {
		t.Fatal("JWT without user claims must track rotation")
	}
}
