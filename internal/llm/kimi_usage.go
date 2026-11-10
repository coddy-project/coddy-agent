package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// KimiUsage holds independent subscription windows. A nil window is unknown.
type KimiUsage struct {
	Limit5H, Limit7D, MonthTotal, MonthCode *KimiUsageWindow
}

// KimiUsageWindow carries the upstream fraction and an optional reset string.
type KimiUsageWindow struct {
	UsedRatio float64
	ResetAt   *string
}

func kimiUsageEndpoint(region string) (string, error) {
	switch region {
	case "", "global":
		return "https://api.kimi.ai/coding/v1/usages", nil
	case "china":
		return "https://api.kimi.com/coding/v1/usages", nil
	default:
		return "", errors.New("kimi usage: invalid region")
	}
}

// FetchKimiUsage reads subscription usage through the configured provider proxy.
// Booster wallet data is deliberately omitted because its currency is not rubles.
func FetchKimiUsage(ctx context.Context, region, accessToken, proxy string) (*KimiUsage, error) {
	endpoint, err := kimiUsageEndpoint(region)
	if err != nil {
		return nil, err
	}
	client, err := HTTPClientForProviderProxy(proxy)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "invalid proxy configuration"}
	}
	return fetchKimiUsage(ctx, accessToken, endpoint, client)
}

func fetchKimiUsage(ctx context.Context, token, endpoint string, client *http.Client) (*KimiUsage, error) {
	if client == nil {
		return nil, errors.New("kimi usage: HTTP client required")
	}
	if strings.TrimSpace(token) == "" {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnauthorized, Detail: "credential unavailable"}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "invalid request"}
	}
	if err := setKimiIdentityHeaders(req, ""); err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "cannot set identity headers"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	safe := *client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safe.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "request failed"}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		kind := ProviderUsageUnavailable
		if resp.StatusCode == http.StatusUnauthorized {
			kind = ProviderUsageUnauthorized
		}
		return nil, &ProviderUsageError{Kind: kind, Status: resp.StatusCode, RetryAfter: parseUsageRetryAfter(resp.Header.Get("Retry-After")), Detail: "upstream error"}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &ProviderUsageError{Kind: ProviderUsageUnavailable, Detail: "read response"}
	}
	if len(body) > 256<<10 {
		return nil, &ProviderUsageError{Kind: ProviderUsageInvalid, Detail: "payload too large"}
	}
	usage, err := decodeKimiUsage(body)
	if err != nil {
		return nil, &ProviderUsageError{Kind: ProviderUsageInvalid, Detail: "invalid payload"}
	}
	return usage, nil
}

func decodeKimiUsage(body []byte) (*KimiUsage, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil || root == nil {
		return nil, errors.New("invalid payload")
	}
	var windows map[string]json.RawMessage
	if json.Unmarshal(root["usages"], &windows) != nil || windows == nil {
		return nil, errors.New("invalid usages")
	}
	return &KimiUsage{Limit5H: kimiUsageWindow(windows["limit_5h"]), Limit7D: kimiUsageWindow(windows["limit_7d"]), MonthTotal: kimiUsageWindow(windows["limit_month_total"]), MonthCode: kimiUsageWindow(windows["limit_month_code"])}, nil
}

func kimiUsageWindow(raw json.RawMessage) *KimiUsageWindow {
	var entry map[string]json.RawMessage
	if json.Unmarshal(raw, &entry) != nil || entry == nil {
		return nil
	}
	value := entry["used_ratio"]
	var number json.Number
	if len(value) > 0 && value[0] == '"' {
		var text string
		if json.Unmarshal(value, &text) != nil || strings.TrimSpace(text) == "" {
			return nil
		}
		number = json.Number(strings.TrimSpace(text))
	} else {
		number = json.Number(string(value))
	}
	ratio, err := strconv.ParseFloat(string(number), 64)
	if err != nil || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 {
		return nil
	}
	window := &KimiUsageWindow{UsedRatio: ratio}
	var reset string
	if json.Unmarshal(entry["reset_time"], &reset) == nil && strings.TrimSpace(reset) != "" {
		window.ResetAt = &reset
	}
	return window
}
