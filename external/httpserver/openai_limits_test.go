//go:build http

package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func limitsTestConfig() *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "hub", Type: "neuraldeep"},
			{Name: "plain", Type: "anthropic"},
		},
		Models: []config.ModelEntry{
			{Model: "plain/model", MaxTokens: 1000, MaxContextTokens: 100000},
			{Model: "hub/model", MaxTokens: 1000, MaxContextTokens: 100000},
		},
		Agent: config.Agent{Model: "plain/model"},
	}
}

func TestLimitsProviderNameQuerySelection(t *testing.T) {
	c := limitsTestConfig()
	q := func(s string) url.Values {
		v, err := url.ParseQuery(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if name, status, _ := limitsProviderName(c, q("provider=hub")); status != 0 || name != "hub" {
		t.Fatalf("provider=hub: got (%q, %d)", name, status)
	}
	if _, status, _ := limitsProviderName(c, q("provider=nope")); status != http.StatusNotFound {
		t.Fatalf("unknown provider: status %d", status)
	}
	if name, status, _ := limitsProviderName(c, q("model=hub/model")); status != 0 || name != "hub" {
		t.Fatalf("model=hub/model: got (%q, %d)", name, status)
	}
	if _, status, _ := limitsProviderName(c, q("model=nope/m")); status != http.StatusNotFound {
		t.Fatalf("unknown model: status %d", status)
	}
	if _, status, _ := limitsProviderName(c, q("model=plain/model")); status != 0 {
		// A model whose provider has no source resolves fine; the 404 is the
		// usage read's, not the selector's.
		t.Fatalf("model=plain/model: status %d", status)
	}
}

func TestLimitsProviderNameFallsBackToTheSingleCapableRow(t *testing.T) {
	c := limitsTestConfig()
	// The default model's provider (plain, anthropic) has no usage source;
	// exactly one capable row remains and answers for a bare /v1/limits.
	name, status, _ := limitsProviderName(c, url.Values{})
	if status != 0 || name != "hub" {
		t.Fatalf("bare query: got (%q, %d), want the one capable row", name, status)
	}

	// With two capable rows nothing is guessed.
	c.Providers = append(c.Providers, config.ProviderConfig{Name: "other", Type: "openai", APIBase: "http://x", APIKey: "k"})
	if name, status, _ := limitsProviderName(c, url.Values{}); status != 0 || name != "plain" {
		t.Fatalf("two capable rows: got (%q, %d), want the default's own provider", name, status)
	}
}

func intp(v int) *int { return &v }

func TestUsageUpdateToNeuralDeepLimitsFullMapping(t *testing.T) {
	retry := 120
	u := &acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      "hub",
		ProviderType:  "neuraldeep",
		ObservedAt:    "2026-09-06T17:47:02Z",
		Plan:          "pro",
		KeyName:       "acc",
		Windows: []acp.UsageWindow{
			{ID: "session", Label: "3h", Used: intp(450), Limit: intp(15000), Remaining: intp(14550), UsedPercent: 3, ResetsAt: "2026-09-06T17:59:59Z", ResetInSec: 777},
			{ID: "week", Label: "week", Used: intp(9981), Limit: intp(150000), Remaining: intp(140019), UsedPercent: 6.65, ResetsAt: "2026-09-07T00:00:00Z", ResetInSec: 22378},
			{ID: "day", Label: "day", UsedPercent: 12.5, ResetsAt: "2026-09-07T00:00:00Z"},
		},
		Rate:            &acp.UsageRate{Used: 2, Limit: 120, Remaining: 118, ResetInSec: 58},
		CooldownSec:     5,
		Wallet:          &acp.UsageWallet{BalanceRub: -1229.244167, SpentRub30d: 2000.73518},
		Blocked:         true,
		Blockers:        []string{"session_exhausted"},
		RetryAt:         "2026-09-06T17:59:59Z",
		RetryInSec:      retry,
		UnlimitedModels: []string{"qwen"},
		BlockedModels: []acp.UsageBlockedModel{
			{Model: "m1", Blocker: "key_cap_blocked", RetryAt: "2026-09-06T18:00:00Z", RetryInSec: 60},
		},
	}
	v := usageUpdateToNeuralDeepLimits(u)
	if v.Schema != 1 || v.Tier != "pro" || v.ObservedAt != "2026-09-06T17:47:02Z" {
		t.Fatalf("head = %+v", v)
	}
	if v.Decision == nil || v.Decision.CanRequest || len(v.Decision.Blockers) != 1 || v.Decision.RetryAfterSec == nil || *v.Decision.RetryAfterSec != 120 {
		t.Fatalf("decision = %+v", v.Decision)
	}
	if v.Chat == nil || v.Chat.Session.Used == nil || *v.Chat.Session.Used != 450 || v.Chat.Session.Window != "3h" {
		t.Fatalf("chat.session = %+v", v.Chat.Session)
	}
	if v.Chat.Week.Used == nil || *v.Chat.Week.Used != 9981 {
		t.Fatalf("chat.week = %+v", v.Chat.Week)
	}
	if v.Chat.RPM.Limit == nil || *v.Chat.RPM.Limit != 120 {
		t.Fatalf("chat.rpm = %+v", v.Chat.RPM)
	}
	if v.Chat.CooldownSec == nil || *v.Chat.CooldownSec != 5 {
		t.Fatalf("cooldown = %+v", v.Chat.CooldownSec)
	}
	if v.DailyCapacity == nil || v.DailyCapacity.PctUsed != 12.5 {
		t.Fatalf("daily_capacity = %+v", v.DailyCapacity)
	}
	if v.Wallet == nil || v.Wallet.BalanceRub != -1229.244167 {
		t.Fatalf("wallet = %+v", v.Wallet)
	}
	if v.Key.Name != "acc" || v.Key.BillingMode != "wallet" || v.Key.Status != "blocked" {
		t.Fatalf("key = %+v", v.Key)
	}
	if len(v.BlockedModels) != 1 || v.BlockedModels[0].Model != "m1" || v.BlockedModels[0].ResetInSec == nil {
		t.Fatalf("blocked_models = %+v", v.BlockedModels)
	}
	if len(v.Options) != 1 || len(v.Options[0].Models) != 1 || v.Options[0].Models[0] != "qwen" {
		t.Fatalf("options = %+v", v.Options)
	}
}

func TestUsageUpdateToNeuralDeepLimitsMinimalUpdate(t *testing.T) {
	v := usageUpdateToNeuralDeepLimits(&acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      "x",
		ProviderType:  "openai",
	})
	// The schema-1 contract blocks are always emitted: a limits client
	// refuses an answer without tier, decision and chat.
	if v.Tier == "" || v.ObservedAt == "" || v.Decision == nil || v.Chat == nil {
		t.Fatalf("minimal update = %+v", v)
	}
	if !v.Decision.CanRequest {
		t.Fatal("an unblocked update reads as can_request")
	}
	if v.UnlimitedVolume {
		t.Fatal("unlimited must not appear for a metered update")
	}
}

func TestUsageUpdateToNeuralDeepLimitsOmitsEmptyBlocks(t *testing.T) {
	minimal, err := json.Marshal(usageUpdateToNeuralDeepLimits(&acp.ProviderUsageUpdate{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{`"key":`, `"session":`, `"week":`, `"rpm":`} {
		if bytes.Contains(minimal, []byte(member)) {
			t.Fatalf("empty %s member must be omitted: %s", member, minimal)
		}
	}

	keyNameOnly, err := json.Marshal(usageUpdateToNeuralDeepLimits(&acp.ProviderUsageUpdate{KeyName: "acc"}))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(keyNameOnly, []byte(`"key":`)) {
		t.Fatalf("keyName alone must not invent status and billing mode: %s", keyNameOnly)
	}

	withKey, err := json.Marshal(usageUpdateToNeuralDeepLimits(&acp.ProviderUsageUpdate{KeyName: "acc", Wallet: &acp.UsageWallet{BalanceRub: 1}}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(withKey, []byte(`"key":{"name":"acc","status":"ok","billing_mode":"wallet"}`)) {
		t.Fatalf("a key with wallet metadata must survive: %s", withKey)
	}
}
