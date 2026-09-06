package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// NeuralDeep account usage: the hub's GET /v1/limits (schema 1) answers with
// the live remainders of the key's account, read-only. This file fetches and
// decodes it; the mapping to the surface-facing update lives in
// internal/session, so this package keeps not importing internal/acp.
//
// The endpoint's own policy binds Coddy too: no dollar figures ever appear in
// the payload, money is the account's own rubles, subscription volume is
// counters and percentages.

// Failure kinds of a usage fetch. Transport and credential failures are
// reported here; policy denials (a blocked user, 403) are a kind of their
// own so the collector can turn them into a Blocked update instead of an
// error row.
const (
	NeuralDeepUsageUnauthorized = "unauthorized"
	NeuralDeepUsageForbidden    = "forbidden"
	NeuralDeepUsageUnavailable  = "unavailable"
	NeuralDeepUsageInvalid      = "invalid"
)

// neuralDeepUsageSchema is the payload version this decoder understands.
// Agents write parsers against it; a different number means the fields may
// have been renamed, and painting wrong percentages is worse than none.
const neuralDeepUsageSchema = 1

// neuralDeepUsageRequestTimeout bounds the HTTP read of a fetch. The
// credential helper that may run first keeps its own, longer budget
// (config.apiKeyCommandTimeout): a helper that asks a person for a
// biometric prompt must not be cut short by a network budget.
const neuralDeepUsageRequestTimeout = 5 * time.Second

// NeuralDeepUsageError is a failed usage fetch. RetryAfter carries the pause
// the hub asked for on 429 and 503 (Retry-After as seconds or an HTTP-date),
// zero when it sent none.
type NeuralDeepUsageError struct {
	Status     int
	Kind       string
	RetryAfter time.Duration
	Detail     string
}

func (e *NeuralDeepUsageError) Error() string {
	msg := "neuraldeep usage: " + e.Kind
	if e.Status > 0 {
		msg += " (HTTP " + strconv.Itoa(e.Status) + ")"
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// NeuralDeepUsage is the decoded schema-1 payload. Pointers stand for
// fields the hub sends as null: a window that does not apply to the key
// (wallet and bypass keys, unlimited options) has null counters.
type NeuralDeepUsage struct {
	Schema          int    `json:"schema"`
	ObservedAt      string `json:"observed_at"`
	Tier            string `json:"tier"`
	UnlimitedVolume bool   `json:"unlimited_volume"`
	Bypass          bool   `json:"bypass"`
	// FairUse is nil when the hub omits it; only an explicit false means the
	// session and week windows do not apply to the key.
	FairUse *bool                   `json:"fair_use"`
	Options []NeuralDeepUsageOption `json:"options"`
	Key     NeuralDeepUsageKey      `json:"key"`
	// Decision and Chat are required blocks of the schema: a payload without
	// them is not a limits answer, whatever its schema number says.
	Decision      *NeuralDeepUsageDecision `json:"decision"`
	Chat          *NeuralDeepUsageChat     `json:"chat"`
	DailyCapacity *NeuralDeepUsageDaily    `json:"daily_capacity"`
	Wallet        *NeuralDeepUsageWallet   `json:"wallet"`
}

// NeuralDeepUsageOption is an option enabled on the key (Qwen ∞): its models
// bypass the session and week windows.
type NeuralDeepUsageOption struct {
	Code   string   `json:"code"`
	Title  string   `json:"title"`
	Models []string `json:"models"`
}

// NeuralDeepUsageKey describes the presented key: its hub name, status
// (ok, blocked, cap_blocked) and billing mode (subscription, wallet).
type NeuralDeepUsageKey struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	BillingMode string `json:"billing_mode"`
}

// NeuralDeepUsageDecision is the hub's own verdict on whether a chat request
// would pass now, with the blockers and the wait for the timed ones.
type NeuralDeepUsageDecision struct {
	CanRequest    bool     `json:"can_request"`
	Blockers      []string `json:"blockers"`
	RetryAfterSec *int     `json:"retry_after_sec"`
}

// NeuralDeepUsageChat holds the chat-class windows, the live minute and the
// cooldown of the account.
type NeuralDeepUsageChat struct {
	Session     NeuralDeepUsageGate `json:"session"`
	Week        NeuralDeepUsageGate `json:"week"`
	RPM         NeuralDeepUsageRPM  `json:"rpm"`
	CooldownSec *int                `json:"cooldown_sec"`
}

// NeuralDeepUsageGate is one volume window (session or week).
type NeuralDeepUsageGate struct {
	Used       *int   `json:"used"`
	Limit      *int   `json:"limit"`
	Remaining  *int   `json:"remaining"`
	ResetInSec *int   `json:"reset_in_sec"`
	ResetsAt   string `json:"resets_at"`
	Window     string `json:"window"`
}

// NeuralDeepUsageRPM is the live requests-per-minute window.
type NeuralDeepUsageRPM struct {
	Used       *int `json:"used"`
	Limit      *int `json:"limit"`
	Remaining  *int `json:"remaining"`
	ResetInSec *int `json:"reset_in_sec"`
}

// NeuralDeepUsageDaily is the key's daily capacity, percent only: the hub
// computes round(min(spend/budget, 1) * 100, 1) and keeps the money to
// itself.
type NeuralDeepUsageDaily struct {
	PctUsed   float64 `json:"pct_used"`
	Exhausted bool    `json:"exhausted"`
	ResetsAt  string  `json:"resets_at"`
}

// NeuralDeepUsageWallet is the account's own money in rubles; the balance
// may be negative on post-paid accounts.
type NeuralDeepUsageWallet struct {
	BalanceRub  float64 `json:"balance_rub"`
	SpentRub30d float64 `json:"spent_rub_30d"`
}

// FetchNeuralDeepUsage reads GET {apiBase}/limits with the bearer key. The
// HTTP read is bounded by the request timeout on top of ctx, whose
// cancellation (a logout, a config swap) aborts it. The error is always a
// *NeuralDeepUsageError, with the key redacted from any upstream text.
func FetchNeuralDeepUsage(ctx context.Context, apiBase, key string, hc *http.Client) (*NeuralDeepUsage, error) {
	if hc == nil {
		hc = &http.Client{}
	}
	ctx, cancel := context.WithTimeout(ctx, neuralDeepUsageRequestTimeout)
	defer cancel()
	url := strings.TrimRight(strings.TrimSpace(apiBase), "/") + "/limits"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &NeuralDeepUsageError{Kind: NeuralDeepUsageUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &NeuralDeepUsageError{Kind: NeuralDeepUsageUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if readErr != nil {
		return nil, &NeuralDeepUsageError{Status: resp.StatusCode, Kind: NeuralDeepUsageUnavailable, Detail: redactNeuralDeepSecrets(readErr.Error())}
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, &NeuralDeepUsageError{Status: resp.StatusCode, Kind: NeuralDeepUsageUnauthorized, Detail: neuralDeepUsageDetail(body)}
	case resp.StatusCode == http.StatusForbidden:
		return nil, &NeuralDeepUsageError{Status: resp.StatusCode, Kind: NeuralDeepUsageForbidden, Detail: neuralDeepUsageDetail(body)}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, &NeuralDeepUsageError{
			Status:     resp.StatusCode,
			Kind:       NeuralDeepUsageUnavailable,
			RetryAfter: parseUsageRetryAfter(resp.Header.Get("Retry-After")),
			Detail:     neuralDeepUsageDetail(body),
		}
	}
	var usage NeuralDeepUsage
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, &NeuralDeepUsageError{Status: resp.StatusCode, Kind: NeuralDeepUsageInvalid, Detail: "undecodable payload"}
	}
	if usage.Schema != neuralDeepUsageSchema {
		return nil, &NeuralDeepUsageError{Status: resp.StatusCode, Kind: NeuralDeepUsageInvalid, Detail: fmt.Sprintf("schema %d, want %d", usage.Schema, neuralDeepUsageSchema)}
	}
	if err := usage.validate(); err != nil {
		return nil, &NeuralDeepUsageError{Status: resp.StatusCode, Kind: NeuralDeepUsageInvalid, Detail: err.Error()}
	}
	return &usage, nil
}

// validate checks the fields every schema-1 answer carries. A renamed or
// dropped block must read as invalid, not as an account with nothing left
// (a missing fair_use would otherwise read as unlimited and a missing
// decision as blocked).
func (u *NeuralDeepUsage) validate() error {
	switch {
	case u == nil:
		return errors.New("empty payload")
	case strings.TrimSpace(u.Tier) == "":
		return errors.New("payload without tier")
	case strings.TrimSpace(u.ObservedAt) == "":
		return errors.New("payload without observed_at")
	case u.Decision == nil:
		return errors.New("payload without decision")
	case u.Chat == nil:
		return errors.New("payload without chat")
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(u.ObservedAt)); err != nil {
		return errors.New("observed_at is not RFC3339")
	}
	return nil
}

// neuralDeepUsageDetail extracts a short, redacted description from an
// error body (the hub answers {"detail": "..."}).
func neuralDeepUsageDetail(body []byte) string {
	var env struct {
		Detail string `json:"detail"`
	}
	detail := ""
	if json.Unmarshal(body, &env) == nil {
		detail = env.Detail
	}
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}
	if runes := []rune(detail); len(runes) > 160 {
		detail = string(runes[:160]) + "…"
	}
	return redactNeuralDeepSecrets(detail)
}

// parseUsageRetryAfter reads Retry-After as delay seconds or an HTTP-date;
// unparsable or non-positive values mean no pause was requested.
func parseUsageRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil {
		if secs > 0 {
			return time.Duration(secs * float64(time.Second))
		}
		return 0
	}
	if when, err := http.ParseTime(raw); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}

// NeuralDeepUsageForProvider fetches the account usage behind a neuraldeep
// provider row with the same credential and endpoint rules requests use: an
// explicit api_key (or command, or env) wins, the stored hub login fills in,
// the api_base selects the deployment, providers[].proxy applies. Without
// any credential it returns an unauthorized error without a request. The
// credential helper runs under ctx (a cancellation ends it) with its usual
// budget; a helper that ran out of time or was cancelled answers
// unavailable, not unauthorized, so a slow helper never sticks as a
// rejected key.
func NeuralDeepUsageForProvider(ctx context.Context, provider config.ProviderConfig, authPath string) (*NeuralDeepUsage, error) {
	explicit, helperErr := provider.EffectiveAPIKeyContextErr(ctx)
	key := neuralDeepEffectiveKey(explicit, authPath)
	if strings.TrimSpace(key) == "" {
		if helperErr != nil {
			return nil, &NeuralDeepUsageError{Kind: NeuralDeepUsageUnavailable, Detail: "credential helper cut short: " + helperErr.Error()}
		}
		return nil, &NeuralDeepUsageError{Kind: NeuralDeepUsageUnauthorized, Detail: "no credential: sign in with coddy providers login " + provider.Name}
	}
	hc, err := HTTPClientForOptionalProxy(provider.Proxy)
	if err != nil {
		return nil, &NeuralDeepUsageError{Kind: NeuralDeepUsageUnavailable, Detail: redactNeuralDeepSecrets(err.Error())}
	}
	return FetchNeuralDeepUsage(ctx, neuralDeepAPIBase(provider.APIBase), key, hc)
}

// NeuralDeepUsageFingerprint identifies the account a provider row talks
// to without exposing it: a short hash of the deployment, the proxy and the
// credential as configured (the literal key, the command line, the env
// value, and the stored login file's content). It changes when any of them
// does and is empty when no credential source exists at all. It never runs
// api_key_command: a cache lookup must stay cheap, so the command's text
// stands in for its output.
func NeuralDeepUsageFingerprint(provider config.ProviderConfig, authPath string) string {
	h := sha256.New()
	writeField := func(s string) {
		_, _ = io.WriteString(h, s)
		_, _ = h.Write([]byte{0})
	}
	writeField(neuralDeepAPIBase(provider.APIBase))
	writeField(strings.TrimSpace(provider.Proxy))
	hasCredential := false
	if v := strings.TrimSpace(provider.APIKey); v != "" {
		hasCredential = true
		writeField("key:" + v)
	}
	if v := strings.TrimSpace(provider.APIKeyCommand); v != "" {
		hasCredential = true
		writeField("cmd:" + v)
	}
	if env := config.ProviderAPIKeyEnvVarName(provider.Name); env != "" {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			hasCredential = true
			writeField("env:" + v)
		}
	}
	if strings.TrimSpace(authPath) != "" {
		if data, err := os.ReadFile(authPath); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			hasCredential = true
			writeField("login:" + string(data))
		}
	}
	if !hasCredential {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// IsNeuralDeepUsageError reports the failure kind of err when it is a usage
// fetch error.
func IsNeuralDeepUsageError(err error) (*NeuralDeepUsageError, bool) {
	var ue *NeuralDeepUsageError
	if errors.As(err, &ue) {
		return ue, true
	}
	return nil, false
}
