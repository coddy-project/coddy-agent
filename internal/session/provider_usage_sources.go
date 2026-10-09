package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// providerUsageFingerprint identifies the credential behind a provider row for
// the usage cache; cliLogin says whether the row may fall back to the
// machine-wide CLI login of its type (config.Config.CLILoginRow). alias names
// the model of a coddy row, whose usage is read per alias; every other type
// ignores it.
func providerUsageFingerprint(provider config.ProviderConfig, alias, authPath string, cliLogin bool) string {
	switch strings.ToLower(strings.TrimSpace(provider.Type)) {
	case "neuraldeep":
		return llm.NeuralDeepUsageFingerprint(provider, authPath)
	case "codex":
		return llm.CodexUsageFingerprint(provider, authPath, cliLogin)
	case "devin":
		return llm.DevinUsageFingerprint(provider, authPath, cliLogin)
	case "coddy":
		return llm.CoddyUsageFingerprint(provider, alias)
	default:
		// No usage source, no cache key: a caller that skipped the
		// providerUsageSource gate must not get NeuralDeep's fingerprint
		// for a different provider type.
		return ""
	}
}

// fetchProviderUsage keeps transport payloads below the session layer while
// every source shares the manager's scheduling and surface-facing snapshot.
// alias is the model a coddy row is read for and is ignored by the other types.
func (m *Manager) fetchProviderUsage(ctx context.Context, provider config.ProviderConfig, alias, authPath string, cliLogin bool) (acp.ProviderUsageUpdate, error) {
	switch strings.ToLower(strings.TrimSpace(provider.Type)) {
	case "neuraldeep":
		u, err := llm.NeuralDeepUsageForProvider(ctx, provider, authPath)
		if err != nil {
			return acp.ProviderUsageUpdate{}, err
		}
		return mapNeuralDeepUsage(u, provider.Name, m.usageNow()), nil
	case "codex":
		u, err := llm.CodexUsageForProvider(ctx, provider, authPath, cliLogin)
		if err != nil {
			return acp.ProviderUsageUpdate{}, err
		}
		return mapCodexUsage(u, provider.Name, m.usageNow()), nil
	case "devin":
		u, err := llm.DevinUsageForProvider(ctx, provider, authPath, cliLogin)
		if err != nil {
			return acp.ProviderUsageUpdate{}, err
		}
		return mapDevinUsage(u, provider.Name, m.usageNow()), nil
	case "coddy":
		key, helperErr := provider.EffectiveAPIKeyContextErr(ctx)
		if key == "" && helperErr != nil {
			// A remote can be open on purpose, so no key is not an error; a
			// helper that was cut short is, and must not be mistaken for it.
			return acp.ProviderUsageUpdate{}, &llm.ProviderUsageError{Kind: llm.ProviderUsageUnavailable, Detail: "credential helper cut short: " + helperErr.Error()}
		}
		w, err := llm.CoddyUsageForProvider(ctx, llm.ProviderInput{
			Name: provider.Name, Type: provider.Type, APIKey: key, BaseURL: provider.APIBase, ProxyURL: provider.Proxy, ClientTLS: m.Cfg().ProviderClientTLS(&provider),
		}, alias)
		if err != nil {
			return acp.ProviderUsageUpdate{}, err
		}
		return mapCoddyUsage(w, provider.Name, alias, m.usageNow()), nil
	default:
		return acp.ProviderUsageUpdate{}, fmt.Errorf("provider usage: unsupported source")
	}
}

func providerUsageFailure(err error) (string, time.Duration) {
	var usageErr *llm.ProviderUsageError
	if errors.As(err, &usageErr) {
		return usageErr.Kind, usageErr.RetryAfter
	}
	if nd, ok := llm.IsNeuralDeepUsageError(err); ok {
		return nd.Kind, nd.RetryAfter
	}
	return ProviderUsageErrorUnavailable, 0
}

func mapCodexUsage(u *llm.CodexUsage, provider string, fetchedAt time.Time) acp.ProviderUsageUpdate {
	out := acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage, Provider: provider, ProviderType: "codex",
		Plan: strings.TrimSpace(u.PlanType), FetchedAt: fetchedAt.UTC().Format(time.RFC3339),
	}
	if rate := u.RateLimit; rate != nil {
		out.Windows = codexUsageWindows(rate, fetchedAt)
		out.Blocked = rate.Allowed != nil && !*rate.Allowed || rate.LimitReached != nil && *rate.LimitReached
		if out.Blocked {
			out.Blockers = []string{"quota_exhausted"}
			setSubscriptionRetry(&out)
		}
	}
	// Additional limits belong to a metered feature, not necessarily every
	// model of this account. Show their scope without widening their denial.
	for i, extra := range u.AdditionalRateLimits {
		name := strings.TrimSpace(extra.LimitName)
		if name == "" {
			name = strings.TrimSpace(extra.MeteredFeature)
		}
		if name == "" {
			continue
		}
		for _, w := range codexUsageWindows(extra.RateLimit, fetchedAt) {
			w.ID = "codex:" + strconv.Itoa(i) + ":" + w.ID
			w.Label = name + " · " + w.Label
			out.Windows = append(out.Windows, w)
		}
	}
	return out
}

func codexUsageWindows(rate *llm.CodexUsageRateLimit, now time.Time) []acp.UsageWindow {
	if rate == nil {
		return nil
	}
	var windows []acp.UsageWindow
	for _, raw := range []*llm.CodexUsageWindow{rate.PrimaryWindow, rate.SecondaryWindow} {
		if raw == nil || raw.UsedPercent == nil {
			continue
		}
		id, label := subscriptionWindowPeriod(raw.LimitWindowSeconds)
		if len(windows) > 0 && windows[0].ID == id {
			id += "-secondary"
		}
		w := acp.UsageWindow{ID: id, Label: label, UsedPercent: clampPercent(*raw.UsedPercent), Exhausted: *raw.UsedPercent >= 100}
		if raw.ResetAt > 0 {
			at := time.Unix(raw.ResetAt, 0)
			w.ResetsAt = at.UTC().Format(time.RFC3339)
			if in := int(at.Sub(now).Seconds()); in > 0 {
				w.ResetInSec = in
			}
		}
		if raw.ResetAfterSeconds != nil && *raw.ResetAfterSeconds > 0 {
			// The upstream's relative countdown wins over the reset_at one
			// (no local clock in the loop), but a zero or negative value
			// must not clobber a valid reset_at countdown.
			w.ResetInSec = *raw.ResetAfterSeconds
		}
		windows = append(windows, w)
	}
	return windows
}

// A primary window can be weekly (for example on Free). Its position never
// supplies its name, and unfamiliar durations remain visible as durations.
func subscriptionWindowPeriod(seconds int) (string, string) {
	switch seconds {
	case 7 * 24 * 3600:
		return "week", "week"
	case 24 * 3600:
		return "day", "day"
	}
	id := "session"
	if seconds > 24*3600 {
		id = "window-" + strconv.Itoa(seconds)
	}
	switch {
	case seconds%3600 == 0:
		return id, strconv.Itoa(seconds/3600) + "h"
	case seconds%60 == 0:
		return id, strconv.Itoa(seconds/60) + "m"
	default:
		return id, strconv.Itoa(seconds) + "s"
	}
}

// All exhausted gates must reset before the account becomes available. A
// missing reset leaves that moment unknown rather than promising an early one.
func setSubscriptionRetry(out *acp.ProviderUsageUpdate) {
	var last *acp.UsageWindow
	for i := range out.Windows {
		w := &out.Windows[i]
		if !w.Exhausted {
			continue
		}
		if w.ResetInSec <= 0 || w.ResetsAt == "" {
			return
		}
		if last == nil || w.ResetInSec > last.ResetInSec {
			last = w
		}
	}
	if last != nil {
		out.RetryAt, out.RetryInSec = last.ResetsAt, last.ResetInSec
	}
}

// mapDevinUsage turns a Devin GetUserStatus answer into the shared snapshot.
// The server reports remaining percents; the snapshot reports used percents.
// Quota windows appear only for the quota billing strategy, when the plan
// does not hide them, and when their reset timestamp is known - a credits or
// unknown plan stays a plan name alone, never a guessed meter.
func mapDevinUsage(u *llm.DevinUsage, provider string, fetchedAt time.Time) acp.ProviderUsageUpdate {
	out := acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage, Provider: provider, ProviderType: "devin",
		Plan: strings.TrimSpace(u.PlanName), FetchedAt: fetchedAt.UTC().Format(time.RFC3339),
	}
	if u.BillingStrategy == llm.DevinUsageQuotaBillingStrategy {
		if !u.HideDailyQuota && u.DailyResetAt > 0 {
			w := acp.UsageWindow{ID: "day", Label: "day"}
			devinQuotaWindow(&w, u.DailyRemainingPercent, u.DailyResetAt, fetchedAt)
			out.Windows = append(out.Windows, w)
		}
		if !u.HideWeeklyQuota && u.WeeklyResetAt > 0 {
			w := acp.UsageWindow{ID: "week", Label: "week"}
			devinQuotaWindow(&w, u.WeeklyRemainingPercent, u.WeeklyResetAt, fetchedAt)
			out.Windows = append(out.Windows, w)
		}
	}
	// ACU consumption is a percentage-only window: no reset clock and no
	// request counts are ever invented for it.
	if u.ACUConsumed != nil && u.ACULimit != nil && *u.ACULimit > 0 {
		used := clampPercent(*u.ACUConsumed / *u.ACULimit * 100)
		out.Windows = append(out.Windows, acp.UsageWindow{
			ID: "acu", Label: "ACU", UsedPercent: used, Exhausted: used >= 100,
		})
	}
	if out.Windows != nil {
		exhausted := true
		for _, w := range out.Windows {
			if !w.Exhausted {
				exhausted = false
			}
		}
		if exhausted {
			// An ACU-only account carries no reset clock, so setSubscriptionRetry
			// legitimately leaves RetryAt empty and the surfaces report the block
			// without a time - a dead-end block is the honest state, the
			// subscription page is where it lifts.
			out.Blocked = true
			out.Blockers = []string{"quota_exhausted"}
			setSubscriptionRetry(&out)
		}
	}
	return out
}

// devinQuotaWindow fills a quota window from the remaining percent the server
// reported and the reset timestamp it is bound to.
func devinQuotaWindow(w *acp.UsageWindow, remaining uint64, resetAt int64, now time.Time) {
	used := 100 - int64(remaining)
	if used < 0 {
		used = 0
	}
	w.UsedPercent = float64(used)
	w.Exhausted = remaining == 0
	at := time.Unix(resetAt, 0)
	w.ResetsAt = at.UTC().Format(time.RFC3339)
	if in := int(at.Sub(now).Seconds()); in > 0 {
		w.ResetInSec = in
	}
}

// mapCoddyUsage turns the projection a remote Coddy answers with into the
// surface-facing update of the alias. The remote's document is all this reads:
// no plan, key name, wallet or rate exists on the wire, so none is invented,
// and nothing absolute is taken from it either, since the two hosts' clocks
// differ. A window's reset arrives as seconds from the moment the remote
// answered; the update anchors it at the receipt on this host (the delivery
// then ages the countdown like every source's). A window without reset_in_s
// has no reset clock and gets neither field; 0 is a clock that has run out.
// An answer that says unsupported is that and nothing else.
func mapCoddyUsage(w *llm.WireUsage, provider, alias string, fetchedAt time.Time) acp.ProviderUsageUpdate {
	out := acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage, Provider: provider, ProviderType: "coddy", Model: alias,
		FetchedAt: fetchedAt.UTC().Format(time.RFC3339),
	}
	if w == nil || !w.Supported {
		out.Unsupported = true
		return out
	}
	out.Stale = w.Stale
	for _, raw := range w.Windows {
		label := raw.Label
		if label == "" {
			label = raw.ID
		}
		win := acp.UsageWindow{ID: raw.ID, Label: label, UsedPercent: clampPercent(raw.UsedPercent), Exhausted: raw.Exhausted}
		if raw.ResetInS != nil {
			in := *raw.ResetInS
			if in < 0 {
				in = 0
			}
			win.ResetInSec = in
			win.ResetsAt = fetchedAt.Add(time.Duration(in) * time.Second).UTC().Format(time.RFC3339)
		}
		out.Windows = append(out.Windows, win)
	}
	out.Blocked = w.Blocked
	if len(w.Blockers) > 0 {
		out.Blockers = append([]string(nil), w.Blockers...)
	}
	if out.Blocked && w.RetryInS > 0 {
		out.RetryInSec = w.RetryInS
		out.RetryAt = fetchedAt.Add(time.Duration(w.RetryInS) * time.Second).UTC().Format(time.RFC3339)
	}
	return out
}
