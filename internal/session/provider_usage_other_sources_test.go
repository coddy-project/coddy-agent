package session

// Regression guards for the usage sources that predate the coddy one
// (neuraldeep, codex, devin): the per-alias subject, the unsupported memory,
// the follow-up read and the drop on a vanished alias are written for a coddy
// row only, and none of them may change what the other types answer. Each
// expectation below was produced by the code as it stood before the coddy
// source existed.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

const (
	wantNeuralDeepMapped = `{"sessionUpdate":"provider_usage","provider":"neuraldeep","providerType":"neuraldeep","observedAt":"2026-09-06T17:47:02Z","fetchedAt":"2026-09-06T17:47:10Z","plan":"pro","keyName":"coddy","windows":[{"id":"session","label":"3h","used":407,"limit":15000,"remaining":14593,"usedPercent":2.7133333333333334,"resetsAt":"2026-09-06T17:59:59Z","resetInSec":777},{"id":"week","label":"week","used":9981,"limit":150000,"remaining":140019,"usedPercent":6.654,"resetsAt":"2026-09-07T00:00:00Z","resetInSec":22378},{"id":"day","label":"day","usedPercent":12.5,"resetsAt":"2026-09-07T00:00:00Z","resetInSec":22378}],"rate":{"used":2,"limit":120,"remaining":118,"resetInSec":58},"wallet":{"balanceRub":-1229.244167,"spentRub30d":2000.73518},"blocked":false,"unlimitedModels":["qwen3.6-35b-a3b"]}`
	wantCodexMapped      = `{"sessionUpdate":"provider_usage","provider":"work","providerType":"codex","fetchedAt":"2026-09-06T17:47:10Z","plan":"plus","windows":[{"id":"session","label":"5h","usedPercent":100,"exhausted":true,"resetsAt":"2026-09-06T17:33:20Z","resetInSec":900},{"id":"week","label":"week","usedPercent":100,"exhausted":true,"resetsAt":"2026-09-06T17:50:00Z","resetInSec":1800},{"id":"codex:0:session","label":"Fast model · 5h","usedPercent":40,"resetsAt":"2026-09-06T17:41:40Z","resetInSec":300}],"blocked":true,"blockers":["quota_exhausted"],"retryAt":"2026-09-06T17:50:00Z","retryInSec":1800}`
	wantDevinMapped      = `{"sessionUpdate":"provider_usage","provider":"work","providerType":"devin","fetchedAt":"2026-09-06T17:47:10Z","plan":"Pro","windows":[{"id":"day","label":"day","usedPercent":77,"resetsAt":"2026-09-06T17:33:20Z"},{"id":"week","label":"week","usedPercent":13,"resetsAt":"2026-09-10T00:26:40Z","resetInSec":283170},{"id":"acu","label":"ACU","usedPercent":50}],"blocked":false}`

	codexMappedFixture = `{"plan_type":"plus","rate_limit":{"allowed":false,"limit_reached":true,` +
		`"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_at":1788716000,"reset_after_seconds":900},` +
		`"secondary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_at":1788717000,"reset_after_seconds":1800}},` +
		`"additional_rate_limits":[{"limit_name":"Fast model","metered_feature":"fast","rate_limit":{"allowed":true,"limit_reached":false,` +
		`"primary_window":{"used_percent":40,"limit_window_seconds":18000,"reset_at":1788716500,"reset_after_seconds":300}}}]}`
)

func TestMappedUsageOfTheOtherSourcesIsUnchanged(t *testing.T) {
	at := time.Date(2026, 9, 6, 17, 47, 10, 0, time.UTC)
	asJSON := func(v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := asJSON(mapNeuralDeepUsage(decodeUsageFixture(t, usageFixture(407)), "neuraldeep", at)); got != wantNeuralDeepMapped {
		t.Errorf("neuraldeep mapping moved:\n got %s\nwant %s", got, wantNeuralDeepMapped)
	}
	var cx llm.CodexUsage
	if err := json.Unmarshal([]byte(codexMappedFixture), &cx); err != nil {
		t.Fatal(err)
	}
	if got := asJSON(mapCodexUsage(&cx, "work", at)); got != wantCodexMapped {
		t.Errorf("codex mapping moved:\n got %s\nwant %s", got, wantCodexMapped)
	}
	consumed, limit := 4.0, 8.0
	dv := &llm.DevinUsage{
		PlanName: "Pro", BillingStrategy: llm.DevinUsageQuotaBillingStrategy,
		DailyRemainingPercent: 23, WeeklyRemainingPercent: 87, DailyResetAt: 1788716000, WeeklyResetAt: 1789000000,
		ACUConsumed: &consumed, ACULimit: &limit,
	}
	if got := asJSON(mapDevinUsage(dv, "work", at)); got != wantDevinMapped {
		t.Errorf("devin mapping moved:\n got %s\nwant %s", got, wantDevinMapped)
	}
}

// The fingerprint a row is cached under is its type's own, whatever alias a
// caller passes along (the alias belongs to the coddy source alone).
func TestUsageFingerprintOfTheOtherTypesIsTheirOwn(t *testing.T) {
	t.Setenv("NEURALDEEP_API_KEY", "")
	t.Setenv("WORK_API_KEY", "")
	home := t.TempDir()
	authPath := config.NeuralDeepAuthPath(home, "nd")
	nd := config.ProviderConfig{Name: "nd", Type: "neuraldeep", APIKey: "sk-nd-fingerprint-key"}
	cx := config.ProviderConfig{Name: "work", Type: "codex"}
	dv := config.ProviderConfig{Name: "work", Type: "devin", APIKey: "sk-devin"}
	for _, alias := range []string{"", "ignored"} {
		if got, want := providerUsageFingerprint(nd, alias, authPath, false), llm.NeuralDeepUsageFingerprint(nd, authPath); got != want {
			t.Errorf("neuraldeep fingerprint (alias %q) = %q, want %q", alias, got, want)
		}
		if got, want := providerUsageFingerprint(cx, alias, authPath, true), llm.CodexUsageFingerprint(cx, authPath, true); got != want {
			t.Errorf("codex fingerprint (alias %q) = %q, want %q", alias, got, want)
		}
		if got, want := providerUsageFingerprint(dv, alias, authPath, true), llm.DevinUsageFingerprint(dv, authPath, true); got != want {
			t.Errorf("devin fingerprint (alias %q) = %q, want %q", alias, got, want)
		}
	}
	if got := providerUsageFingerprint(config.ProviderConfig{Name: "p", Type: "openai", APIKey: "k"}, "x", authPath, false); got != "" {
		t.Errorf("a type without a usage source has no fingerprint, got %q", got)
	}
}

// A provider that is not a coddy row keeps one entry under its name: a
// selector names the same entry whatever follows the slash, the answer
// carries no model, and the entry's subject has none.
func TestUsageOfOtherTypesStaysOneEntryPerProvider(t *testing.T) {
	stand := newUsageStand(t)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()

	first, err := m.ProviderUsage(ctx, "neuraldeep", false)
	if err != nil || first == nil || first.Model != "" || stand.calls.Load() != 1 {
		t.Fatalf("by name: err=%v update=%+v calls=%d", err, first, stand.calls.Load())
	}
	for _, selector := range []string{"neuraldeep/qwen3.8-27b", "neuraldeep/not-a-configured-row", " neuraldeep/x "} {
		u, err := m.ProviderUsage(ctx, selector, false)
		if err != nil || u == nil || u.Provider != "neuraldeep" || u.Model != "" || stand.calls.Load() != 1 {
			t.Fatalf("selector %q: err=%v update=%+v calls=%d", selector, err, u, stand.calls.Load())
		}
	}
	// A type without a source answers unsupported under a selector too, with
	// nothing remembered for it.
	u, err := m.ProviderUsage(ctx, "stub/model", false)
	if err != nil || u == nil || !u.Unsupported || u.Model != "" {
		t.Fatalf("stub selector: err=%v update=%+v", err, u)
	}
	m.usage.mu.Lock()
	defer m.usage.mu.Unlock()
	if len(m.usage.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(m.usage.entries))
	}
	e := m.usage.entries["neuraldeep"]
	if e == nil || e.subject != (usageSubject{provider: "neuraldeep"}) {
		t.Fatalf("entry under the provider name = %+v", e)
	}
}

// Only a coddy row drops its numbers on an "invalid" failure: the other
// sources keep the last numbers, marked stale, as they always did.
func TestUsageInvalidAnswerStillKeepsTheStaleNumbersOfOtherTypes(t *testing.T) {
	stand := newUsageStand(t)
	m := newUsageManager(t, stand, &usageCapture{}, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	ctx := context.Background()

	if _, err := m.ProviderUsage(ctx, "neuraldeep", false); err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Second)
	stand.set(http.StatusOK, `{"schema": 2, "unexpected": true}`, nil)
	u, err := m.ProviderUsage(ctx, "neuraldeep", true)
	if err != nil || u == nil || u.Error != ProviderUsageErrorInvalid || !u.Stale || findWindow(*u, "session") == nil || *findWindow(*u, "session").Used != 407 {
		t.Fatalf("invalid answer must keep the stale numbers: err=%v update=%+v", err, u)
	}
	if stand.calls.Load() != 2 {
		t.Fatalf("calls = %d", stand.calls.Load())
	}
}

// A turn end of a session on another type arms no follow-up read: the
// deferred refresh machinery stays what it was.
func TestTurnEndOfOtherTypesArmsNoFollowUp(t *testing.T) {
	stand := newUsageStand(t)
	sender := &usageCapture{}
	m := newUsageManager(t, stand, sender, nil)
	clock := newFakeUsageClock()
	m.SetProviderUsageClock(clock.Now, clock.After)
	id := newUsageSession(t, m, "")
	if err := usagePrompt(t, m, id, sender, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.WaitProviderUsageIdle(3 * time.Second); err != nil {
		t.Fatal(err)
	}
	if stand.calls.Load() != 1 || clock.pending() != 0 {
		t.Fatalf("calls=%d pending timers=%d, want one read and no follow-up", stand.calls.Load(), clock.pending())
	}
	updates, _ := sender.snapshot()
	if len(updates) != 1 || updates[0].RefreshPending {
		t.Fatalf("updates = %+v", updates)
	}
}
