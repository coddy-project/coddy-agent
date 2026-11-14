package session

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// openaiLimitsStand answers GET {api_base}/limits like a Coddy deployment:
// the schema-1 shape plus the coddy member.
func openaiLimitsStand(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/limits" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const openaiUsageBody = `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro",
 "key":{"name":"acc","status":"ok","billing_mode":"wallet"},
 "decision":{"can_request":true,"blockers":[]},
 "chat":{"session":{"used":450,"limit":15000,"remaining":14550,"reset_in_sec":777,
                    "resets_at":"2026-09-06T17:59:59Z","window":"3h"},
         "week":{"used":9981,"limit":150000,"remaining":140019,"reset_in_sec":22378,
                 "resets_at":"2026-09-07T00:00:00Z","window":"iso-week"},
         "rpm":{"used":0,"limit":120,"remaining":120,"reset_in_sec":58},"cooldown_sec":0},
 "wallet":{"balance_rub":-1229.244167,"spent_rub_30d":2000.73518},
 "coddy":{"sessionUpdate":"provider_usage","provider":"upstream-hub","providerType":"neuraldeep",
   "observedAt":"2026-09-06T17:47:02Z","fetchedAt":"2026-09-06T17:47:03Z","plan":"pro","keyName":"acc",
   "windows":[{"id":"session","label":"3h","used":450,"limit":15000,"remaining":14550,"usedPercent":3}],
   "wallet":{"balanceRub":-1229.244167}}}`

const openaiUsageBodyNoCoddy = `{"schema":1,"observed_at":"2026-09-06T17:47:02Z","tier":"pro",
 "key":{"name":"acc","status":"ok","billing_mode":"subscription"},
 "decision":{"can_request":true,"blockers":[]},
 "chat":{"session":{"used":450,"limit":15000,"remaining":14550,"reset_in_sec":777,
                    "resets_at":"2026-09-06T17:59:59Z","window":"3h"},
         "week":{"used":9981,"limit":150000,"remaining":140019,"reset_in_sec":22378,
                 "resets_at":"2026-09-07T00:00:00Z","window":"iso-week"},
         "rpm":{"used":0,"limit":120,"remaining":120,"reset_in_sec":58},"cooldown_sec":0}}`

func newOpenAIUsageManager(t *testing.T, apiBase string) *Manager {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: t.TempDir()},
		Providers: []config.ProviderConfig{
			{Name: "hub", Type: "openai", APIBase: apiBase, APIKey: "sk-test"},
		},
		Models: []config.ModelEntry{
			{Model: "hub/model", MaxTokens: 1000, MaxContextTokens: 100000},
		},
		Agent: config.Agent{Model: "hub/model"},
	}
	noAuto := false
	cfg.Rules.AutoDiscover = &noAuto
	runner := func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	return NewManager(cfg, &usageCapture{}, runner, slog.New(slog.DiscardHandler), cfg.Paths.CWD, nil)
}

func TestProviderUsageOpenaiReadsCoddyMember(t *testing.T) {
	srv := openaiLimitsStand(t, openaiUsageBody)
	m := newOpenAIUsageManager(t, srv.URL+"/v1")
	defer m.ShutdownProviderUsage(0)

	u, err := m.ProviderUsage(context.Background(), "hub", false)
	if err != nil {
		t.Fatalf("ProviderUsage: %v", err)
	}
	if u == nil || u.Unsupported {
		t.Fatalf("update = %+v", u)
	}
	if u.Provider != "hub" {
		t.Fatalf("provider = %q, want the local row", u.Provider)
	}
	if u.ProviderType != "neuraldeep" {
		t.Fatalf("providerType = %q, want the upstream account's type", u.ProviderType)
	}
	if u.Plan != "pro" || u.Wallet == nil {
		t.Fatalf("update = %+v", u)
	}
}

func TestProviderUsageOpenaiRebasesCoddySnapshotFetchTime(t *testing.T) {
	body := strings.Replace(openaiUsageBody, `"usedPercent":3}`, `"usedPercent":3,"resetInSec":777}`, 1)
	srv := openaiLimitsStand(t, body)
	m := newOpenAIUsageManager(t, srv.URL+"/v1")
	defer m.ShutdownProviderUsage(0)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)

	u, err := m.ProviderUsage(context.Background(), "hub", false)
	if err != nil {
		t.Fatalf("ProviderUsage: %v", err)
	}
	if u.FetchedAt != clock.Now().UTC().Format(time.RFC3339) {
		t.Fatalf("fetchedAt = %q, want the local fetch time %q", u.FetchedAt, clock.Now().UTC().Format(time.RFC3339))
	}
	if len(u.Windows) != 1 || u.Windows[0].ResetInSec != 777 {
		t.Fatalf("windows = %+v, want the upstream's already-corrected 777 seconds", u.Windows)
	}
}

func TestProviderUsageOpenaiNeuralOnlyUpstream(t *testing.T) {
	srv := openaiLimitsStand(t, openaiUsageBodyNoCoddy)
	m := newOpenAIUsageManager(t, srv.URL+"/v1")
	defer m.ShutdownProviderUsage(0)

	u, err := m.ProviderUsage(context.Background(), "hub", false)
	if err != nil {
		t.Fatalf("ProviderUsage: %v", err)
	}
	if u == nil || u.Unsupported {
		t.Fatalf("update = %+v", u)
	}
	if u.Plan != "pro" || len(u.Windows) == 0 {
		t.Fatalf("update = %+v", u)
	}
}

func TestProviderUsageOpenaiUpstreamWithoutLimitsStaysUnsupported(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	m := newOpenAIUsageManager(t, srv.URL+"/v1")
	defer m.ShutdownProviderUsage(0)

	for i := 0; i < 2; i++ {
		u, err := m.ProviderUsage(context.Background(), "hub", false)
		if err != nil {
			t.Fatalf("ProviderUsage %d: %v", i, err)
		}
		if u == nil || !u.Unsupported {
			t.Fatalf("read %d: update = %+v, want unsupported", i, u)
		}
	}
	if calls != 1 {
		t.Fatalf("the upstream was probed %d times, want 1", calls)
	}
}

func TestProviderUsageOpenaiRefreshReprobesUpstream(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	m := newOpenAIUsageManager(t, srv.URL+"/v1")
	defer m.ShutdownProviderUsage(0)

	// A refresh defers only to the pacing floor: once it has passed, the
	// upstream is probed again even while the answer stays unsupported.
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)

	if _, err := m.ProviderUsage(context.Background(), "hub", false); err != nil {
		t.Fatalf("ProviderUsage: %v", err)
	}
	clock.advance(2 * time.Minute)
	if _, err := m.ProviderUsage(context.Background(), "hub", true); err != nil {
		t.Fatalf("ProviderUsage refresh: %v", err)
	}
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("refresh must probe once more, got %d calls", calls)
	}
}

func TestProviderUsageEnabledForOpenaiRow(t *testing.T) {
	row := &config.ProviderConfig{Name: "hub", Type: "openai", APIBase: "http://x", APIKey: "k"}
	if !ProviderUsageEnabled(row) {
		t.Fatal("an openai row is a usage source")
	}
	off := false
	row.UsageLimitsPanel = &off
	if ProviderUsageEnabled(row) {
		t.Fatal("the row's panel switch must still gate the probe")
	}
}
