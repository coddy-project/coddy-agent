//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// v1LimitsBody is the answer of GET /v1/limits: the NeuralDeep hub's
// schema-1 shape every limits reader already understands, plus the coddy
// member carrying the whole provider usage update Coddy serves on its own
// surfaces (GET /coddy/providers/{name}/usage, the provider_usage events),
// so a Coddy client loses nothing of what the account says.
type v1LimitsBody struct {
	*llm.NeuralDeepUsage
	Coddy *acp.ProviderUsageUpdate `json:"coddy"`
}

// handleV1Limits answers GET /v1/limits: the account quota of a provider
// row, in the schema-1 shape NeuralDeep clients read. ?provider= names the
// row, ?model= the model whose row is meant; with neither the provider of
// the default model is used, or the single usage-capable row when the
// default has none. ?refresh=1 bypasses the cache like the coddy route.
func (s *Server) handleV1Limits(w http.ResponseWriter, r *http.Request) {
	c := s.activeCfg()
	if c == nil || s.mgr == nil {
		http.Error(w, "config unavailable", http.StatusInternalServerError)
		return
	}
	name, status, msg := limitsProviderName(c, r.URL.Query())
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	if name == "" {
		http.Error(w, "no usage-capable provider", http.StatusNotFound)
		return
	}
	refresh := false
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("refresh"))) {
	case "1", "true", "yes":
		refresh = true
	}
	usage, err := s.mgr.ProviderUsage(r.Context(), name, refresh)
	switch {
	case err != nil:
		http.Error(w, "the usage read failed", http.StatusBadGateway)
		return
	case usage == nil || usage.Unsupported:
		http.Error(w, "the provider has no usage source", http.StatusNotFound)
		return
	case usage.Error != "" && len(usage.Windows) == 0 && usage.Rate == nil && usage.Wallet == nil && !usage.Blocked:
		// A fetch that produced nothing at all has nothing stale to serve.
		http.Error(w, "the provider's usage upstream failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v1LimitsBody{
		NeuralDeepUsage: usageUpdateToNeuralDeepLimits(usage),
		Coddy:           usage,
	})
}

// limitsProviderName resolves which provider row /v1/limits answers for: an
// explicit ?provider= or ?model=, or the provider behind the default model,
// falling back to the one usage-capable row when the default has none.
func limitsProviderName(c *config.Config, q url.Values) (name string, status int, msg string) {
	switch {
	case strings.TrimSpace(q.Get("provider")) != "":
		name = strings.TrimSpace(q.Get("provider"))
		if c.FindProvider(name) == nil {
			return "", http.StatusNotFound, "unknown provider"
		}
		return name, 0, ""
	case strings.TrimSpace(q.Get("model")) != "":
		ent := c.FindModelEntry(strings.TrimSpace(q.Get("model")))
		if ent == nil {
			return "", http.StatusNotFound, "unknown model"
		}
		name = ent.ProviderName()
		if c.FindProvider(name) == nil {
			return "", http.StatusNotFound, "the model names no configured provider"
		}
		return name, 0, ""
	default:
		if ent := c.FindModelEntry(session.ResolveModelID(c, "")); ent != nil {
			name = ent.ProviderName()
		}
		if !session.ProviderUsageEnabled(c.FindProvider(name)) {
			var enabled []string
			for i := range c.Providers {
				if session.ProviderUsageEnabled(&c.Providers[i]) {
					enabled = append(enabled, c.Providers[i].Name)
				}
			}
			if len(enabled) == 1 {
				name = enabled[0]
			}
		}
		return name, 0, ""
	}
}

// usageUpdateToNeuralDeepLimits renders a ProviderUsageUpdate in the
// NeuralDeep hub's schema-1 shape. Windows that schema-1 cannot express
// (ids beyond session/week/day, per-blocker retry times, the percent-only
// windows) survive only in the coddy member of the same answer.
func usageUpdateToNeuralDeepLimits(u *acp.ProviderUsageUpdate) *llm.NeuralDeepUsage {
	observed := strings.TrimSpace(u.ObservedAt)
	if observed == "" {
		observed = strings.TrimSpace(u.FetchedAt)
	}
	if observed == "" {
		observed = time.Now().UTC().Format(time.RFC3339)
	}
	tier := strings.TrimSpace(u.Plan)
	if tier == "" {
		tier = "unknown"
	}
	v := &llm.NeuralDeepUsage{
		Schema:          neuralDeepLimitsSchema,
		ObservedAt:      observed,
		Tier:            tier,
		UnlimitedVolume: u.Unlimited,
		Decision: &llm.NeuralDeepUsageDecision{
			CanRequest: !u.Blocked,
			Blockers:   u.Blockers,
		},
		Chat: &llm.NeuralDeepUsageChat{},
	}
	if u.RetryInSec > 0 {
		sec := u.RetryInSec
		v.Decision.RetryAfterSec = &sec
	}
	if u.Wallet != nil || u.Blocked {
		v.Key = llm.NeuralDeepUsageKey{Name: u.KeyName, Status: "ok", BillingMode: "subscription"}
		if u.Wallet != nil {
			v.Key.BillingMode = "wallet"
		}
		if u.Blocked {
			v.Key.Status = "blocked"
		}
	}
	for i := range u.Windows {
		w := &u.Windows[i]
		switch w.ID {
		case "session":
			v.Chat.Session = usageWindowToGate(w)
		case "week":
			v.Chat.Week = usageWindowToGate(w)
		case "day":
			v.DailyCapacity = &llm.NeuralDeepUsageDaily{
				PctUsed:   w.UsedPercent,
				Exhausted: w.Exhausted,
				ResetsAt:  w.ResetsAt,
			}
		}
	}
	if u.Rate != nil {
		used, limit, remaining, sec := u.Rate.Used, u.Rate.Limit, u.Rate.Remaining, u.Rate.ResetInSec
		v.Chat.RPM = llm.NeuralDeepUsageRPM{Used: &used, Limit: &limit, Remaining: &remaining, ResetInSec: &sec}
	}
	if u.CooldownSec > 0 {
		sec := u.CooldownSec
		v.Chat.CooldownSec = &sec
	}
	if u.Wallet != nil {
		v.Wallet = &llm.NeuralDeepUsageWallet{BalanceRub: u.Wallet.BalanceRub, SpentRub30d: u.Wallet.SpentRub30d}
	}
	for _, bm := range u.BlockedModels {
		m := llm.NeuralDeepUsageBlockedModel{Model: bm.Model, Blocker: bm.Blocker, ResetsAt: bm.RetryAt}
		if bm.RetryInSec > 0 {
			sec := bm.RetryInSec
			m.ResetInSec = &sec
		}
		v.BlockedModels = append(v.BlockedModels, m)
	}
	if len(u.UnlimitedModels) > 0 {
		v.Options = append(v.Options, llm.NeuralDeepUsageOption{
			Code: "unlimited_models", Title: "Unlimited models", Models: u.UnlimitedModels,
		})
	}
	return v
}

// usageWindowToGate maps a counted window to the schema-1 gate shape. A
// window with no counters and no reset leaves the gate empty, which the
// omitzero tag of Chat.Session/Week then drops from the payload; a
// percent-only window survives only in the coddy member (or in
// daily_capacity for the "day" id).
func usageWindowToGate(w *acp.UsageWindow) llm.NeuralDeepUsageGate {
	g := llm.NeuralDeepUsageGate{
		Used:      w.Used,
		Limit:     w.Limit,
		Remaining: w.Remaining,
		ResetsAt:  w.ResetsAt,
		Window:    w.Label,
	}
	if w.ResetInSec > 0 {
		sec := w.ResetInSec
		g.ResetInSec = &sec
	}
	return g
}

// neuralDeepLimitsSchema is the payload version of the limits shape.
const neuralDeepLimitsSchema = 1
