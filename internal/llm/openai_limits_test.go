package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// openAILimitsFixture is the schema-1 answer plus the coddy member, as
// Coddy's own /v1/limits serves it.
const openAILimitsFixture = `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro",
 "key":{"name":"acc","status":"ok","billing_mode":"wallet"},
 "decision":{"can_request":true,"blockers":[]},
 "chat":{"session":{"used":450,"limit":15000,"remaining":14550,"reset_in_sec":777,
                    "resets_at":"2026-09-06T17:59:59Z","window":"3h"},
         "week":{"used":9981,"limit":150000,"remaining":140019,"reset_in_sec":22378,
                 "resets_at":"2026-09-07T00:00:00Z","window":"iso-week"},
         "rpm":{"used":0,"limit":120,"remaining":120,"reset_in_sec":58},"cooldown_sec":0},
 "wallet":{"balance_rub":-1229.244167,"spent_rub_30d":2000.73518},
 "coddy":{"sessionUpdate":"provider_usage","provider":"upstream","providerType":"neuraldeep",
   "plan":"pro","windows":[{"id":"session","label":"3h","used":450,"limit":15000,"remaining":14550,"usedPercent":3}],
   "wallet":{"balanceRub":-1229.244167}}}`

const openAILimitsFixtureNoCoddy = `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro",
 "key":{"name":"acc","status":"ok","billing_mode":"subscription"},
 "decision":{"can_request":true,"blockers":[]},
 "chat":{"session":{"used":450,"limit":15000,"remaining":14550,"reset_in_sec":777,
                    "resets_at":"2026-09-06T17:59:59Z","window":"3h"},
         "week":{"used":9981,"limit":150000,"remaining":140019,"reset_in_sec":22378,
                 "resets_at":"2026-09-07T00:00:00Z","window":"iso-week"},
         "rpm":{"used":0,"limit":120,"remaining":120,"reset_in_sec":58},"cooldown_sec":0}}`

func newLimitsStand(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestFetchOpenAILimitsSendsBearerAndDecodesBothMembers(t *testing.T) {
	var auth string
	srv, _ := newLimitsStand(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/limits" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, openAILimitsFixture)
	})
	payload, err := FetchOpenAILimits(context.Background(), srv.URL+"/v1", "sk-key", srv.Client())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if auth != "Bearer sk-key" {
		t.Fatalf("Authorization = %q, want the row's key", auth)
	}
	if payload.Neural == nil || payload.Neural.Tier != "pro" {
		t.Fatalf("Neural = %+v", payload.Neural)
	}
	if !strings.Contains(string(payload.Coddy), `"providerType":"neuraldeep"`) {
		t.Fatalf("Coddy member missing: %s", payload.Coddy)
	}
}

func TestFetchOpenAILimitsOmitsAuthorizationWithEmptyKey(t *testing.T) {
	var auth string
	srv, _ := newLimitsStand(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, openAILimitsFixture)
	})
	if _, err := FetchOpenAILimits(context.Background(), srv.URL, "", srv.Client()); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if auth != "" {
		t.Fatalf("Authorization = %q, want none for an empty key", auth)
	}
}

func TestFetchOpenAILimitsNeuralOnlyAnswer(t *testing.T) {
	srv, _ := newLimitsStand(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, openAILimitsFixtureNoCoddy)
	})
	payload, err := FetchOpenAILimits(context.Background(), srv.URL, "k", srv.Client())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if payload.Neural == nil || payload.Neural.Chat == nil || payload.Neural.Chat.Session.Used == nil || *payload.Neural.Chat.Session.Used != 450 {
		t.Fatalf("Neural = %+v", payload.Neural)
	}
	if len(payload.Coddy) != 0 {
		t.Fatalf("Coddy = %s, want empty without the member", payload.Coddy)
	}
}

func TestFetchOpenAILimitsStatusMapping(t *testing.T) {
	cases := []struct {
		status   int
		wantKind string
	}{
		{http.StatusUnauthorized, ProviderUsageUnauthorized},
		{http.StatusForbidden, NeuralDeepUsageForbidden},
		{http.StatusNotFound, ProviderUsageUnavailable},
		{http.StatusMethodNotAllowed, ProviderUsageUnavailable},
		{http.StatusBadGateway, ProviderUsageUnavailable},
	}
	for _, tc := range cases {
		srv, _ := newLimitsStand(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", tc.status)
		})
		_, err := FetchOpenAILimits(context.Background(), srv.URL, "k", srv.Client())
		var ue *ProviderUsageError
		if !errors.As(err, &ue) {
			t.Fatalf("status %d: err = %v, want *ProviderUsageError", tc.status, err)
		}
		if ue.Status != tc.status || ue.Kind != tc.wantKind {
			t.Fatalf("status %d: got {Status:%d Kind:%s}, want {%d %s}", tc.status, ue.Status, ue.Kind, tc.status, tc.wantKind)
		}
	}
}

func TestFetchOpenAILimitsInvalidAnswers(t *testing.T) {
	for _, body := range []string{
		`not json`,
		`{"schema":2,"tier":"pro"}`,
		`{"schema":1,"tier":"pro"}`, // neither a full neural body nor a coddy member
	} {
		srv, _ := newLimitsStand(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, body)
		})
		_, err := FetchOpenAILimits(context.Background(), srv.URL, "k", srv.Client())
		var ue *ProviderUsageError
		if !errors.As(err, &ue) || ue.Kind != ProviderUsageInvalid {
			t.Fatalf("body %s: err = %v, want invalid", body, err)
		}
	}
}

func TestFetchOpenAILimitsNilClientRejected(t *testing.T) {
	if _, err := FetchOpenAILimits(context.Background(), "http://example.invalid", "k", nil); err == nil {
		t.Fatal("nil client must be rejected, never a direct fallback")
	}
}

func TestOpenAIUsageForProviderNoBase(t *testing.T) {
	seen := make(chan string, 1)
	proxy := newProviderHTTPSProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Host + r.URL.Path
		http.NotFound(w, r)
	}))
	_, err := OpenAIUsageForProvider(context.Background(), config.ProviderConfig{Name: "x", Type: "openai", Proxy: proxy})
	var ue *ProviderUsageError
	if !errors.As(err, &ue) || ue.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want the default endpoint's 404", err)
	}
	if got := <-seen; got != "api.openai.com/v1/limits" {
		t.Fatalf("request = %q, want api.openai.com/v1/limits", got)
	}
}

func TestOpenAIUsageFingerprintFollowsEndpointAndCredential(t *testing.T) {
	row := config.ProviderConfig{Name: "hub", Type: "openai", APIBase: "http://a/v1", APIKey: "k1"}
	base := OpenAIUsageFingerprint(row)
	if base == "" {
		t.Fatal("empty fingerprint")
	}
	if got := OpenAIUsageFingerprint(row); got != base {
		t.Fatal("fingerprint not stable")
	}
	row.APIKey = "k2"
	if got := OpenAIUsageFingerprint(row); got == base {
		t.Fatal("fingerprint did not follow the credential")
	}
	row.APIKey = "k1"
	row.APIBase = "http://b/v1"
	if got := OpenAIUsageFingerprint(row); got == base {
		t.Fatal("fingerprint did not follow the endpoint")
	}
}

func TestOpenAIUsageFingerprintNoCredentialStillDistinct(t *testing.T) {
	a := OpenAIUsageFingerprint(config.ProviderConfig{Name: "a", Type: "openai", APIBase: "http://a/v1"})
	b := OpenAIUsageFingerprint(config.ProviderConfig{Name: "b", Type: "openai", APIBase: "http://b/v1"})
	if a == "" || a == b {
		t.Fatalf("credential-less rows need distinct fingerprints, got %q %q", a, b)
	}
}
