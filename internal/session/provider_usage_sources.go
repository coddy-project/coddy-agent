package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// providerUsageFingerprint identifies the credential behind a provider row for
// the usage cache; cliLogin says whether the row may fall back to the
// machine-wide CLI login of its type (config.Config.CLILoginRow).
func providerUsageFingerprint(provider config.ProviderConfig, authPath string, cliLogin bool) string {
	switch strings.ToLower(strings.TrimSpace(provider.Type)) {
	case "neuraldeep":
		return llm.NeuralDeepUsageFingerprint(provider, authPath)
	case "codex":
		return llm.CodexUsageFingerprint(provider, authPath, cliLogin)
	case "devin":
		return llm.DevinUsageFingerprint(provider, authPath, cliLogin)
	case "kimi":
		var cred llm.KimiCredential
		state := llm.KimiCredentialMissing
		var err error
		if provider.KimiIdentity != "" && authPath == "" {
			err = errors.New("invalid kimi identity")
		} else if authPath != "" {
			cred, state, err = llm.NewKimiCredentialStore(authPath).Load(provider.Region)
		}
		cliToken := ""
		var cliErr error
		if err == nil && state == llm.KimiCredentialMissing && cliLogin && (provider.Mode == "" || provider.Mode == "subscription") {
			cliToken, cliErr = llm.ReadKimiCLIAccessToken(provider.Mode, provider.Region, false)
		}
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%v\x00%+v\x00%t\x00%s\x00%v", provider.KimiIdentity, authPath, provider.Mode, provider.Region, provider.Proxy, state, err, cred, cliLogin, cliToken, cliErr)))
		return fmt.Sprintf("%x", sum)
	case "openai":
		return llm.OpenAIUsageFingerprint(provider)
	default:
		// No usage source, no cache key: a caller that skipped the
		// providerUsageSource gate must not get NeuralDeep's fingerprint
		// for a different provider type.
		return ""
	}
}

// fetchProviderUsage keeps transport payloads below the session layer while
// every source shares the manager's scheduling and surface-facing snapshot.
func (m *Manager) fetchProviderUsage(ctx context.Context, provider config.ProviderConfig, authPath string, cliLogin bool) (acp.ProviderUsageUpdate, error) {
	u, _, err := m.fetchProviderUsageBound(ctx, provider, authPath, cliLogin)
	return u, err
}

func (m *Manager) fetchProviderUsageBound(ctx context.Context, provider config.ProviderConfig, authPath string, cliLogin bool) (acp.ProviderUsageUpdate, string, error) {
	switch strings.ToLower(strings.TrimSpace(provider.Type)) {
	case "neuraldeep":
		u, err := llm.NeuralDeepUsageForProvider(ctx, provider, authPath)
		if err != nil {
			return acp.ProviderUsageUpdate{}, "", err
		}
		return mapNeuralDeepUsage(u, provider.Name, m.usageNow()), "", nil
	case "codex":
		u, err := llm.CodexUsageForProvider(ctx, provider, authPath, cliLogin)
		if err != nil {
			return acp.ProviderUsageUpdate{}, "", err
		}
		return mapCodexUsage(u, provider.Name, m.usageNow()), "", nil
	case "devin":
		u, err := llm.DevinUsageForProvider(ctx, provider, authPath, cliLogin)
		if err != nil {
			return acp.ProviderUsageUpdate{}, "", err
		}
		return mapDevinUsage(u, provider.Name, m.usageNow()), "", nil
	case "kimi":
		if provider.KimiIdentity != "" && authPath == "" {
			return acp.ProviderUsageUpdate{}, "", errors.New("kimi usage: invalid credential identity")
		}
		var store *llm.KimiCredentialStore
		if authPath != "" {
			store = llm.NewKimiCredentialStore(authPath)
		}
		source, err := llm.NewKimiSubscriptionSource(store, provider.Mode, provider.Region, provider.Proxy, !cliLogin)
		if err != nil {
			return acp.ProviderUsageUpdate{}, "", err
		}
		var fingerprint string
		var u *llm.KimiUsage
		_, kind, err := source.WithCredential(ctx, func(token string) error {
			fingerprint = providerUsageFingerprint(provider, authPath, cliLogin)
			var cred llm.KimiCredential
			state := llm.KimiCredentialMissing
			var loadErr error
			if store != nil {
				cred, state, loadErr = store.Load(provider.Region)
			}
			if loadErr != nil {
				return errUsageSuperseded
			}
			switch state {
			case llm.KimiCredentialValid:
				if cred.AccessToken != token {
					return errUsageSuperseded
				}
			case llm.KimiCredentialMissing:
				current, cliErr := llm.ReadKimiCLIAccessToken(provider.Mode, provider.Region, !cliLogin)
				if cliErr != nil || current != token {
					return errUsageSuperseded
				}
			default:
				return errUsageSuperseded
			}
			if fingerprint != providerUsageFingerprint(provider, authPath, cliLogin) {
				return errUsageSuperseded
			}
			var fetchErr error
			u, fetchErr = llm.FetchKimiUsage(ctx, provider.Region, token, provider.Proxy)
			return fetchErr
		})
		if err != nil {
			if errors.Is(err, llm.ErrKimiCLILoginRequired) {
				return acp.ProviderUsageUpdate{}, fingerprint, fmt.Errorf("%w: %w", &llm.ProviderUsageError{Kind: llm.ProviderUsageUnauthorized, Detail: "credential unavailable"}, err)
			}
			if errors.Is(err, llm.ErrKimiCredentialMissing) || errors.Is(err, llm.ErrKimiCredentialUnauthorized) || errors.Is(err, llm.ErrKimiCredentialUncertain) {
				return acp.ProviderUsageUpdate{}, fingerprint, &llm.ProviderUsageError{Kind: llm.ProviderUsageUnauthorized, Detail: "credential unavailable"}
			}
			return acp.ProviderUsageUpdate{}, fingerprint, kimiUsageCredentialError(kind, err)
		}
		return mapKimiUsage(u, provider.Name, m.usageNow()), fingerprint, nil
	case "openai":
		payload, err := llm.OpenAIUsageForProvider(ctx, provider)
		if err != nil {
			var usageErr *llm.ProviderUsageError
			if errors.As(err, &usageErr) && (usageErr.Status == http.StatusNotFound || usageErr.Status == http.StatusMethodNotAllowed) {
				// The upstream has no limits endpoint: the row is marked
				// unsupported and asked no more until it changes.
				return acp.ProviderUsageUpdate{
					SessionUpdate: acp.UpdateTypeProviderUsage,
					Provider:      provider.Name,
					ProviderType:  provider.Type,
					Unsupported:   true,
				}, "", nil
			}
			return acp.ProviderUsageUpdate{}, "", err
		}
		if len(payload.Coddy) > 0 {
			// A Coddy upstream serves its whole usage update: the windows,
			// the wallet and the account's own provider type survive
			// untouched; the row the update belongs to is the local one.
			var update acp.ProviderUsageUpdate
			if err := json.Unmarshal(payload.Coddy, &update); err == nil && update.SessionUpdate == acp.UpdateTypeProviderUsage {
				update.Provider = provider.Name
				if update.ProviderType == "" {
					update.ProviderType = provider.Type
				}
				// The upstream has already age-corrected the relative durations
				// for its response. Rebase fetchedAt so the local delivery does
				// not subtract the upstream snapshot's age a second time.
				update.FetchedAt = m.usageNow().UTC().Format(time.RFC3339)
				return update, "", nil
			}
		}
		if payload.Neural != nil {
			return mapNeuralDeepUsage(payload.Neural, provider.Name, m.usageNow()), "", nil
		}
		return acp.ProviderUsageUpdate{}, "", &llm.ProviderUsageError{Kind: llm.ProviderUsageInvalid, Detail: "the limits answer carries no usable usage"}
	default:
		return acp.ProviderUsageUpdate{}, "", fmt.Errorf("provider usage: unsupported source")
	}
}

func kimiUsageCredentialError(kind llm.KimiSourceKind, err error) error {
	var usageErr *llm.ProviderUsageError
	if kind == llm.KimiSourceCLI && errors.As(err, &usageErr) && usageErr.Status == 401 {
		return fmt.Errorf("%w: %w", err, llm.ErrKimiCLILoginRequired)
	}
	return err
}

func mapKimiUsage(u *llm.KimiUsage, provider string, now time.Time) acp.ProviderUsageUpdate {
	out := acp.ProviderUsageUpdate{SessionUpdate: acp.UpdateTypeProviderUsage, Provider: provider, ProviderType: "kimi", FetchedAt: now.UTC().Format(time.RFC3339)}
	for _, item := range []struct {
		id, label string
		window    *llm.KimiUsageWindow
	}{{"5h", "5h", u.Limit5H}, {"7d", "7d", u.Limit7D}, {"month_total", "month total", u.MonthTotal}, {"month_code", "month code", u.MonthCode}} {
		if item.window == nil {
			continue
		}
		w := acp.UsageWindow{ID: item.id, Label: item.label, UsedPercent: clampPercent(item.window.UsedRatio * 100), Exhausted: item.window.UsedRatio >= 1}
		if item.window.ResetAt != nil {
			if at, err := time.Parse(time.RFC3339, strings.TrimSpace(*item.window.ResetAt)); err == nil {
				w.ResetsAt = at.UTC().Format(time.RFC3339)
				if seconds := int(at.Sub(now).Seconds()); seconds > 0 {
					w.ResetInSec = seconds
				}
			}
		}
		out.Windows = append(out.Windows, w)
	}
	return out
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
	used := 100 - int64(min(remaining, 100))
	w.UsedPercent = float64(used)
	w.Exhausted = remaining == 0
	at := time.Unix(resetAt, 0)
	w.ResetsAt = at.UTC().Format(time.RFC3339)
	if in := int(at.Sub(now).Seconds()); in > 0 {
		w.ResetInSec = in
	}
}
