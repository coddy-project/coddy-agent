package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// neuralDeepUsageFixture is the schema-1 payload of GET /v1/limits as the hub
// answered a pro wallet key on 2026-09-06 (abridged to the fields Coddy reads
// plus the ones it must ignore).
const neuralDeepUsageFixture = `{
  "schema": 1,
  "observed_at": "2026-09-06T17:47:02Z",
  "tier": "pro",
  "tier_expires_at": null,
  "unlimited_volume": false,
  "options": [{"code": "qwen_unlim", "title": "Qwen ∞", "models": ["qwen3.6-35b-a3b"],
               "rpm": {"limit": 60, "used": 0, "remaining": 60, "window": "1m"},
               "inflight_limit": 4, "unlimited_volume": true, "scope": "account"}],
  "bypass": false,
  "fair_use": true,
  "key": {"name": "coddy", "status": "ok", "billing_mode": "wallet", "cap": null},
  "decision": {"scope": "chat", "can_request": true, "blockers": [], "retry_after_sec": null},
  "chat": {
    "session": {"used": 407, "limit": 15000, "remaining": 14593, "reset_in_sec": 777,
                "resets_at": "2026-09-06T17:59:59Z", "window": "3h"},
    "week": {"used": 9981, "limit": 150000, "remaining": 140019, "reset_in_sec": 22378,
             "resets_at": "2026-09-07T00:00:00Z", "window": "iso-week"},
    "rpm": {"used": 2, "limit": 120, "remaining": 118, "reset_in_sec": 58},
    "cooldown_sec": 0,
    "scope": "account"
  },
  "vector": {"session": {"used": 0, "limit": 300000}, "rpm_limit": 120},
  "parallel_limit": 16,
  "abuse_cooldown_sec": 0,
  "daily_capacity": {"pct_used": 12.5, "exhausted": false, "resets_at": "2026-09-07T00:00:00+00:00"},
  "night": {"enabled": true, "active": false, "capacity_factor": 2, "window_start_msk": 0, "window_end_msk": 6},
  "wallet": {"balance_rub": -1229.244167, "spent_rub_30d": 2000.73518},
  "kimi": null
}`

func neuralDeepUsageServer(t *testing.T, key string, status int, body string, headers map[string]string) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	var calls atomic.Int32
	var lastAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		lastAuth.Store(r.Header.Get("Authorization"))
		if r.URL.Path != "/limits" {
			http.NotFound(w, r)
			return
		}
		if key != "" && r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"unknown key"}`))
			return
		}
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &lastAuth
}

func intPtrValue(t *testing.T, p *int, name string) int {
	t.Helper()
	if p == nil {
		t.Fatalf("%s is nil", name)
	}
	return *p
}

func TestFetchNeuralDeepUsageParsesThePayload(t *testing.T) {
	srv, calls, lastAuth := neuralDeepUsageServer(t, "sk-test-key-0123456789abcdef", http.StatusOK, neuralDeepUsageFixture, nil)
	u, err := FetchNeuralDeepUsage(context.Background(), srv.URL, "sk-test-key-0123456789abcdef", srv.Client())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if calls.Load() != 1 || lastAuth.Load() != "Bearer sk-test-key-0123456789abcdef" {
		t.Fatalf("calls=%d auth=%v, want one bearer request", calls.Load(), lastAuth.Load())
	}
	if u.Schema != 1 || u.Tier != "pro" || u.ObservedAt != "2026-09-06T17:47:02Z" {
		t.Fatalf("header fields = %+v", u)
	}
	if u.FairUse == nil || !*u.FairUse || u.Bypass || u.UnlimitedVolume {
		t.Fatalf("flags = fair_use %v bypass %v unlimited %v", u.FairUse, u.Bypass, u.UnlimitedVolume)
	}
	if u.Key.Name != "coddy" || u.Key.Status != "ok" || u.Key.BillingMode != "wallet" {
		t.Fatalf("key = %+v", u.Key)
	}
	if !u.Decision.CanRequest || len(u.Decision.Blockers) != 0 || u.Decision.RetryAfterSec != nil {
		t.Fatalf("decision = %+v", u.Decision)
	}
	s := u.Chat.Session
	if intPtrValue(t, s.Used, "session.used") != 407 || intPtrValue(t, s.Limit, "session.limit") != 15000 ||
		intPtrValue(t, s.Remaining, "session.remaining") != 14593 || intPtrValue(t, s.ResetInSec, "session.reset_in_sec") != 777 ||
		s.ResetsAt != "2026-09-06T17:59:59Z" || s.Window != "3h" {
		t.Fatalf("session = %+v", s)
	}
	if w := u.Chat.Week; intPtrValue(t, w.Used, "week.used") != 9981 || w.Window != "iso-week" {
		t.Fatalf("week = %+v", w)
	}
	if r := u.Chat.RPM; intPtrValue(t, r.Used, "rpm.used") != 2 || intPtrValue(t, r.Limit, "rpm.limit") != 120 || intPtrValue(t, r.ResetInSec, "rpm.reset") != 58 {
		t.Fatalf("rpm = %+v", r)
	}
	if u.DailyCapacity == nil || u.DailyCapacity.PctUsed != 12.5 || u.DailyCapacity.Exhausted || u.DailyCapacity.ResetsAt == "" {
		t.Fatalf("daily = %+v", u.DailyCapacity)
	}
	if u.Wallet == nil || u.Wallet.BalanceRub > -1229 || u.Wallet.SpentRub30d < 2000 {
		t.Fatalf("wallet = %+v", u.Wallet)
	}
	if len(u.Options) != 1 || len(u.Options[0].Models) != 1 || u.Options[0].Models[0] != "qwen3.6-35b-a3b" {
		t.Fatalf("options = %+v", u.Options)
	}
}

func TestFetchNeuralDeepUsageNullWindowsAndMissingBlocks(t *testing.T) {
	body := `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro","bypass":true,"fair_use":false,
	 "key":{"name":"primary","status":"ok","billing_mode":"wallet"},
	 "decision":{"scope":"chat","can_request":false,"blockers":["wallet_empty"],"retry_after_sec":null},
	 "chat":{"session":{"used":null,"limit":null,"remaining":null,"reset_in_sec":null,"resets_at":null,"window":"3h"},
	         "week":{"used":null,"limit":null,"remaining":null,"reset_in_sec":null,"resets_at":null,"window":"iso-week"},
	         "rpm":{"used":null,"limit":null,"remaining":null,"reset_in_sec":null},"cooldown_sec":null},
	 "wallet":null,"kimi":null,"daily_capacity":null}`
	srv, _, _ := neuralDeepUsageServer(t, "", http.StatusOK, body, nil)
	u, err := FetchNeuralDeepUsage(context.Background(), srv.URL, "sk-x", srv.Client())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !u.Bypass || u.FairUse == nil || *u.FairUse || u.Chat.Session.Limit != nil || u.Chat.Session.Used != nil || u.Chat.RPM.Limit != nil {
		t.Fatalf("null windows must decode as nil pointers: %+v", u.Chat)
	}
	if u.Wallet != nil || u.DailyCapacity != nil || len(u.Options) != 0 {
		t.Fatalf("missing blocks must stay nil: wallet %+v daily %+v options %+v", u.Wallet, u.DailyCapacity, u.Options)
	}
	if u.Decision.CanRequest || len(u.Decision.Blockers) != 1 || u.Decision.Blockers[0] != "wallet_empty" {
		t.Fatalf("decision = %+v", u.Decision)
	}
}

func TestFetchNeuralDeepUsageErrorKinds(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		headers    map[string]string
		wantKind   string
		wantRetry  time.Duration
		wantStatus int
	}{
		{"unauthorized", http.StatusUnauthorized, `{"detail":"unknown key"}`, nil, NeuralDeepUsageUnauthorized, 0, 401},
		{"forbidden", http.StatusForbidden, `{"detail":"user blocked"}`, nil, NeuralDeepUsageForbidden, 0, 403},
		{"unavailable with seconds", http.StatusServiceUnavailable, `{"detail":"counter store unreachable"}`, map[string]string{"Retry-After": "5"}, NeuralDeepUsageUnavailable, 5 * time.Second, 503},
		{"rate limited with seconds", http.StatusTooManyRequests, `{"detail":"slow down"}`, map[string]string{"Retry-After": "30"}, NeuralDeepUsageUnavailable, 30 * time.Second, 429},
		{"server error", http.StatusInternalServerError, `boom`, nil, NeuralDeepUsageUnavailable, 0, 500},
		{"not json", http.StatusOK, `<html>proxy</html>`, nil, NeuralDeepUsageInvalid, 0, 200},
		{"schema two", http.StatusOK, `{"schema":2,"tier":"pro"}`, nil, NeuralDeepUsageInvalid, 0, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := neuralDeepUsageServer(t, "", tc.status, tc.body, tc.headers)
			_, err := FetchNeuralDeepUsage(context.Background(), srv.URL, "sk-secret-value-abcdef0123456789", srv.Client())
			var ue *NeuralDeepUsageError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want *NeuralDeepUsageError", err)
			}
			if ue.Kind != tc.wantKind || ue.Status != tc.wantStatus {
				t.Fatalf("kind/status = %s/%d, want %s/%d", ue.Kind, ue.Status, tc.wantKind, tc.wantStatus)
			}
			if ue.RetryAfter != tc.wantRetry {
				t.Fatalf("retry-after = %s, want %s", ue.RetryAfter, tc.wantRetry)
			}
			if strings.Contains(err.Error(), "sk-secret-value") {
				t.Fatalf("error text leaks the key: %q", err.Error())
			}
		})
	}
}

func TestFetchNeuralDeepUsageRetryAfterHTTPDate(t *testing.T) {
	when := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	srv, _, _ := neuralDeepUsageServer(t, "", http.StatusServiceUnavailable, `{"detail":"later"}`, map[string]string{"Retry-After": when})
	_, err := FetchNeuralDeepUsage(context.Background(), srv.URL, "sk-x", srv.Client())
	var ue *NeuralDeepUsageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v", err)
	}
	if ue.RetryAfter < 80*time.Second || ue.RetryAfter > 91*time.Second {
		t.Fatalf("retry-after from an HTTP-date = %s, want about 90s", ue.RetryAfter)
	}
}

func TestFetchNeuralDeepUsageNetworkFailureIsUnavailable(t *testing.T) {
	srv, _, _ := neuralDeepUsageServer(t, "", http.StatusOK, neuralDeepUsageFixture, nil)
	url := srv.URL
	srv.Close()
	_, err := FetchNeuralDeepUsage(context.Background(), url, "sk-x", &http.Client{})
	var ue *NeuralDeepUsageError
	if !errors.As(err, &ue) || ue.Kind != NeuralDeepUsageUnavailable {
		t.Fatalf("err = %v, want unavailable", err)
	}
}

func TestNeuralDeepUsageForProviderResolvesKeyAndBase(t *testing.T) {
	const key = "sk-stored-key-0123456789abcdef"
	srv, calls, lastAuth := neuralDeepUsageServer(t, key, http.StatusOK, neuralDeepUsageFixture, nil)
	t.Setenv(EnvNeuralDeepBaseURL, srv.URL)
	t.Setenv("NEURALDEEP_API_KEY", "")
	home := t.TempDir()
	authPath := config.NeuralDeepAuthPath(home, "neuraldeep")
	if err := SaveNeuralDeepAuth(authPath, key, "https://hub.example", NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	prov := config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep"}
	u, err := NeuralDeepUsageForProvider(context.Background(), prov, authPath)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if u.Tier != "pro" || calls.Load() != 1 || lastAuth.Load() != "Bearer "+key {
		t.Fatalf("tier=%q calls=%d auth=%v", u.Tier, calls.Load(), lastAuth.Load())
	}

	// An explicit api_key wins over the stored login, as it does for requests.
	explicit := config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep", APIKey: "sk-explicit-key-0123456789abcd"}
	_, err = NeuralDeepUsageForProvider(context.Background(), explicit, authPath)
	var ue *NeuralDeepUsageError
	if !errors.As(err, &ue) || ue.Kind != NeuralDeepUsageUnauthorized || lastAuth.Load() != "Bearer sk-explicit-key-0123456789abcd" {
		t.Fatalf("explicit key: err=%v auth=%v", err, lastAuth.Load())
	}

	// No credential at all: unauthorized without a request.
	before := calls.Load()
	_, err = NeuralDeepUsageForProvider(context.Background(), prov, filepath.Join(home, "missing", "auth.json"))
	if !errors.As(err, &ue) || ue.Kind != NeuralDeepUsageUnauthorized || calls.Load() != before {
		t.Fatalf("missing credential: err=%v calls=%d (before %d)", err, calls.Load(), before)
	}
}

func TestNeuralDeepUsageFingerprint(t *testing.T) {
	home := t.TempDir()
	authPath := config.NeuralDeepAuthPath(home, "neuraldeep")
	if err := SaveNeuralDeepAuth(authPath, "sk-first-key-0123456789abcdefg", "https://hub.example", NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	prov := config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep"}
	first := NeuralDeepUsageFingerprint(prov, authPath)
	again := NeuralDeepUsageFingerprint(prov, authPath)
	if first == "" || first != again {
		t.Fatalf("fingerprint must be stable: %q vs %q", first, again)
	}
	if strings.Contains(first, "sk-first") || len(first) > 32 {
		t.Fatalf("fingerprint must not carry the key: %q", first)
	}
	if err := SaveNeuralDeepAuth(authPath, "sk-second-key-0123456789abcdef", "https://hub.example", NeuralDeepClientID, "coddy"); err != nil {
		t.Fatal(err)
	}
	if rotated := NeuralDeepUsageFingerprint(prov, authPath); rotated == first {
		t.Fatalf("a rotated key must change the fingerprint")
	}
	mirror := config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep", APIBase: "https://api.neuraldeep.tech/v1"}
	if NeuralDeepUsageFingerprint(mirror, authPath) == NeuralDeepUsageFingerprint(prov, authPath) {
		t.Fatalf("a different deployment must change the fingerprint")
	}
	if NeuralDeepUsageFingerprint(prov, filepath.Join(home, "none.json")) != "" {
		t.Fatalf("no credential must give an empty fingerprint")
	}
}

func TestFetchNeuralDeepUsageRejectsAPayloadWithoutTheRequiredBlocks(t *testing.T) {
	cases := map[string]string{
		"bare schema":      `{"schema":1}`,
		"no decision":      `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro","chat":{"session":{},"week":{},"rpm":{}}}`,
		"no chat":          `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro","decision":{"can_request":true}}`,
		"no tier":          `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","decision":{"can_request":true},"chat":{}}`,
		"broken timestamp": `{"schema":1,"observed_at":"yesterday","tier":"pro","decision":{"can_request":true},"chat":{}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _, _ := neuralDeepUsageServer(t, "", http.StatusOK, body, nil)
			_, err := FetchNeuralDeepUsage(context.Background(), srv.URL, "sk-x", srv.Client())
			var ue *NeuralDeepUsageError
			if !errors.As(err, &ue) || ue.Kind != NeuralDeepUsageInvalid {
				t.Fatalf("err = %v, want invalid", err)
			}
		})
	}
}

func TestNeuralDeepUsageForProviderBoundsTheCredentialHelper(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep binary")
	}
	srv, calls, _ := neuralDeepUsageServer(t, "", http.StatusOK, neuralDeepUsageFixture, nil)
	t.Setenv(EnvNeuralDeepBaseURL, srv.URL)
	t.Setenv("HUNG_API_KEY", "")
	prov := config.ProviderConfig{Name: "hung", Type: "neuraldeep", APIKeyCommand: "sleep 30"}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := NeuralDeepUsageForProvider(ctx, prov, filepath.Join(t.TempDir(), "none.json"))
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("a hung credential helper held the fetch for %s", elapsed)
	}
	var ue *NeuralDeepUsageError
	if !errors.As(err, &ue) || ue.Kind != NeuralDeepUsageUnauthorized || calls.Load() != 0 {
		t.Fatalf("err = %v calls = %d, want unauthorized without a request", err, calls.Load())
	}
}
