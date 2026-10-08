package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The account usage of a model a remote Coddy shares: the client of
// GET <api_base>/coddy/llm/models/{alias}/usage (the projection of WireUsage).
// The mapping to the surface-facing update lives in internal/session, so this
// package keeps not importing internal/acp; every failure is a
// *ProviderUsageError, so the manager's existing failure path (stale numbers
// kept, sticky rejection of a refused credential, backoff from Retry-After)
// applies unchanged.

const (
	// coddyUsageLimit bounds a usage document: it is a few hundred bytes, and
	// the remote is untrusted as far as memory goes.
	coddyUsageLimit = 1 << 20
	// coddyUsageErrorLimit bounds how much of a refused read's answer is read.
	coddyUsageErrorLimit = 64 << 10
	// The bounds a document is cut to: the remote names a handful of windows
	// and blockers, and a client is not a place to keep more.
	coddyUsageMaxWindows  = 16
	coddyUsageMaxBlockers = 16
	coddyUsageTextRunes   = 64
	coddyUsageDetailRunes = 160
)

// coddyUsageTimeout bounds one usage read; a variable so a test can shorten it.
var coddyUsageTimeout = 15 * time.Second

// CoddyUsageForProvider reads the usage of the model a remote shares under
// alias, with the row's api_base (the remote's origin or a relay mount), its
// credential and its proxy setting, over the HTTP/1.1 transport every request
// of a coddy row takes. The answers map as follows:
//
//   - a usage document: returned, bounded and cleaned;
//   - supported: false, and a 404 whose JSON body carries code not_found (the
//     reserved route of a remote that predates the projection): a
//     &WireUsage{Supported: false} and no error;
//   - 401 and 403, or a body of kind auth: unauthorized;
//   - a 404 with the JSON code unknown_model: invalid, the one answer that
//     says the alias is gone, so the caller drops what it shows for it;
//   - everything else is unavailable and keeps the numbers a caller shows: a
//     relay's hop-error 404 ("no such node"), any other 404 (a page, plain
//     text), any other invalid answer (a 400), 408, 429, 5xx with their
//     Retry-After, a transport error, and a 200 that is not a usage document.
func CoddyUsageForProvider(ctx context.Context, in ProviderInput, alias string) (*WireUsage, error) {
	key := in.APIKey
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, coddyUsageError(0, ProviderUsageUnavailable, "no model alias to read the usage of", key)
	}
	base, err := coddyEndpoint(in.BaseURL, "")
	if err != nil {
		return nil, coddyUsageError(0, ProviderUsageUnavailable, err.Error(), key)
	}
	hc, err := httpClientForProviderType("coddy", in.ProxyURL)
	if err != nil {
		return nil, coddyUsageError(0, ProviderUsageUnavailable, err.Error(), key)
	}
	ctx, cancel := context.WithTimeout(ctx, coddyUsageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+CoddyUsagePath(alias), nil)
	if err != nil {
		return nil, coddyUsageError(0, ProviderUsageUnavailable, redactURLError(err).Error(), key)
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, coddyUsageError(0, ProviderUsageUnavailable, redactURLError(err).Error(), key)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, coddyUsageErrorLimit))
		return coddyUsageRefusal(resp.StatusCode, resp.Header, body, key)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, coddyUsageLimit+1))
	if err != nil {
		return nil, coddyUsageError(resp.StatusCode, ProviderUsageUnavailable, "read: "+err.Error(), key)
	}
	if len(body) > coddyUsageLimit {
		return nil, coddyUsageError(resp.StatusCode, ProviderUsageUnavailable, "the usage document is larger than 1 MiB", key)
	}
	// "supported" has to be there: an answer that is JSON but not a usage
	// document (a catch-all page of a server without the route) must not read
	// as a remote that reports no usage.
	var probe struct {
		Supported *bool `json:"supported"`
	}
	if json.Unmarshal(body, &probe) != nil || probe.Supported == nil {
		return nil, coddyUsageError(resp.StatusCode, ProviderUsageUnavailable, "the answer is not a usage document (is api_base a remote coddy serve or a swarm relay mount?)", key)
	}
	var doc WireUsage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, coddyUsageError(resp.StatusCode, ProviderUsageUnavailable, "the usage document is malformed", key)
	}
	if !doc.Supported {
		return &WireUsage{Supported: false}, nil
	}
	return boundedWireUsage(doc), nil
}

// coddyUsageRefusal maps a non-2xx answer: the shared classification of a
// coddy answer decides what the body is, the rules above decide what the
// usage reader does about it.
func coddyUsageRefusal(status int, header http.Header, body []byte, key string) (*WireUsage, error) {
	err := classifyCoddyAnswer(status, header, body)
	kind, code := CoddyErrorKind(err), CoddyErrorCode(err)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || kind == WireKindAuth:
		return nil, coddyUsageError(status, ProviderUsageUnauthorized, err.Error(), key)
	case status == http.StatusNotFound && kind == WireKindInvalid && code == WireCodeNotFound:
		return &WireUsage{Supported: false}, nil
	case status == http.StatusNotFound && kind == WireKindInvalid && code == WireCodeUnknownModel:
		return nil, coddyUsageError(status, ProviderUsageInvalid, err.Error(), key)
	}
	ue := coddyUsageError(status, ProviderUsageUnavailable, err.Error(), key)
	if d, ok := coddyRetryAfter(err); ok && d > 0 {
		ue.RetryAfter = d
	} else if d, ok := parseRetryAfterHeader(header); ok {
		ue.RetryAfter = d
	}
	return nil, ue
}

// coddyUsageError builds a usage failure whose text is bounded, cleaned of
// what a terminal would act on, and free of the credential even when the
// remote echoed it.
func coddyUsageError(status int, kind, detail, key string) *ProviderUsageError {
	detail = cleanRemoteText(detail)
	if key = strings.TrimSpace(key); key != "" {
		detail = strings.ReplaceAll(detail, key, "***")
	}
	if runes := []rune(detail); len(runes) > coddyUsageDetailRunes {
		detail = string(runes[:coddyUsageDetailRunes]) + "..."
	}
	return &ProviderUsageError{Status: status, Kind: kind, Detail: detail}
}

// boundedWireUsage cuts a document a remote sent to what a client keeps: at
// most coddyUsageMaxWindows windows and coddyUsageMaxBlockers blockers, short
// clean texts, a percentage in 0..100, no negative or runaway time. A window
// without an id is dropped.
func boundedWireUsage(in WireUsage) *WireUsage {
	out := &WireUsage{
		Supported:   true,
		AccountWide: in.AccountWide,
		Stale:       in.Stale,
		Blocked:     in.Blocked,
		RetryInS:    boundedSeconds(in.RetryInS),
		Windows:     []WireUsageWindow{},
	}
	for _, w := range in.Windows {
		if len(out.Windows) == coddyUsageMaxWindows {
			break
		}
		id := usageText(w.ID)
		if id == "" {
			continue
		}
		label := usageText(w.Label)
		if label == "" {
			label = id
		}
		pct := w.UsedPercent
		if math.IsNaN(pct) {
			pct = 0
		}
		bw := WireUsageWindow{ID: id, Label: label, UsedPercent: math.Min(math.Max(pct, 0), 100), Exhausted: w.Exhausted}
		if w.ResetInS != nil && *w.ResetInS >= 0 {
			v := boundedSeconds(*w.ResetInS)
			bw.ResetInS = &v
		}
		out.Windows = append(out.Windows, bw)
	}
	for _, b := range in.Blockers {
		if len(out.Blockers) == coddyUsageMaxBlockers {
			break
		}
		if b = usageText(b); b != "" {
			out.Blockers = append(out.Blockers, b)
		}
	}
	return out
}

// coddyUsageSecondsCap bounds the reset and the retry a remote names in its usage
// projection. It is not coddyRetryAfterCap: a pause that is slept is cut to a day,
// but a usage window legitimately resets days ahead (a weekly window 3.5 days away
// is 301800 s) and must be shown as such; a year is only a guard against a hostile
// or broken number.
const coddyUsageSecondsCap = 366 * 24 * time.Hour

// boundedSeconds keeps a number of seconds a remote named inside 0 and
// coddyUsageSecondsCap.
func boundedSeconds(s int) int {
	return int(math.Min(math.Max(float64(s), 0), coddyUsageSecondsCap.Seconds()))
}

func usageText(s string) string {
	s = strings.TrimSpace(cleanRemoteText(s))
	if runes := []rune(s); len(runes) > coddyUsageTextRunes {
		s = string(runes[:coddyUsageTextRunes])
	}
	return s
}

// CoddyUsageFingerprint identifies the account and the alias a coddy row's
// usage is read for, without exposing the credential: a short hash of the
// type, the normalised api_base, the proxy route, the credential as configured
// (the literal key, the command line, the conventional environment value) and
// the alias. It changes when any of them does, so a rotated key, a repointed
// api_base or a renamed alias is a new subject, and it is never empty: a
// remote can be open on purpose, which leaves a row with no credential. It
// never runs api_key_command: a cache lookup must stay cheap, so the
// command's text stands in for its output.
func CoddyUsageFingerprint(provider config.ProviderConfig, alias string) string {
	h := sha256.New()
	writeField := func(s string) {
		_, _ = io.WriteString(h, s)
		_, _ = h.Write([]byte{0})
	}
	writeField(strings.TrimSpace(provider.Type))
	writeField(normalizedCoddyBase(provider.APIBase))
	writeField(normalizedProxyRoute(provider.Proxy))
	if v := strings.TrimSpace(provider.APIKey); v != "" {
		writeField("key:" + v)
	}
	if v := strings.TrimSpace(provider.APIKeyCommand); v != "" {
		writeField("cmd:" + v)
	}
	if env := config.ProviderAPIKeyEnvVarName(provider.Name); env != "" {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			writeField("env:" + v)
		}
	}
	writeField(strings.TrimSpace(alias))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// normalizedCoddyBase spells an api_base the way the endpoint is built from
// it: scheme and host in lower case, no trailing slash, no query and no
// fragment. The userinfo stays, it is part of the credential as configured.
func normalizedCoddyBase(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	if u.RawPath != "" {
		u.RawPath = strings.TrimRight(u.RawPath, "/")
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

// normalizedProxyRoute names the route a providers[].proxy setting chose, so
// that an empty value and "inherit", which are one route, are one fingerprint.
func normalizedProxyRoute(setting string) string {
	mode, u, err := config.ParseProxySetting(setting)
	switch {
	case err != nil:
		return strings.TrimSpace(setting)
	case mode == config.ProxyModeInherit:
		return ""
	case mode == config.ProxyModeNone:
		return config.ProxyNone
	}
	return u.String()
}
