package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// kimiStatusBodyLimit bounds how much of a refused answer travels in the
// error. Kimi reports what is wrong with a request in the body, and without
// it a 400 says only that something was refused.
const kimiStatusBodyLimit = 2048

// kimiStatusError turns a non-2xx inference answer into an httpStatusError
// carrying the provider's own explanation, and closes the body.
func kimiStatusError(label string, resp *http.Response) error {
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, kimiStatusBodyLimit))
	_ = resp.Body.Close()
	err := &httpStatusError{label: label, status: resp.StatusCode, header: resp.Header.Clone(), body: streamErrorSnippet(snippet)}
	err.retryAfter, _ = kimiBodyRetryDelay(err.body)
	return err
}

// statusError adds the reset of a subscription quota answer when neither the
// response nor its body names one. The usage request uses the same credential
// and proxy-built client as the inference request that was refused.
func (t *KimiInferenceTransport) statusError(ctx context.Context, label string, resp *http.Response, token string) error {
	err := kimiStatusError(label, resp)
	if t.usageEndpoint == "" {
		return err
	}
	if _, named := serverRetryDelay(err); named || !kimiQuotaStatusError(err) {
		return err
	}
	usage, fetchErr := fetchKimiUsage(ctx, token, t.usageEndpoint, t.client)
	if fetchErr != nil {
		return err
	}
	var raw *httpStatusError
	if !errors.As(err, &raw) {
		return err
	}
	if delay, ok := kimiUsageResetDelay(usage, raw.body); ok {
		raw.retryAfter = delay
	}
	return err
}

func kimiQuotaStatusError(err error) bool {
	var raw *httpStatusError
	if !errors.As(err, &raw) || raw.status != http.StatusTooManyRequests {
		return false
	}
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(raw.body), &envelope) != nil {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(envelope.Error.Type))
	if strings.Contains(kind, "quota") || strings.Contains(kind, "usage_limit") {
		return true
	}
	message := strings.ToLower(envelope.Error.Message)
	return strings.Contains(message, "usage limit") ||
		strings.Contains(message, "quota will reset") ||
		strings.Contains(message, "quota has been reached") ||
		(strings.Contains(message, "reached your") && strings.Contains(message, "limit"))
}

var kimiAbsoluteResetRE = regexp.MustCompile(`(?i)\b(?:quota|limit)\b[^.;]{0,80}?\bresets?\s+at:?\s*([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2}))`)

func kimiBodyRetryDelay(body string) (time.Duration, bool) {
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &root) == nil && root != nil {
		objects := make([]map[string]json.RawMessage, 0, 2)
		var nested map[string]json.RawMessage
		if json.Unmarshal(root["error"], &nested) == nil && nested != nil {
			objects = append(objects, nested)
		}
		objects = append(objects, root)
		for _, object := range objects {
			if delay, ok := kimiJSONDelay(object["retry_after_ms"], time.Millisecond); ok {
				return delay, true
			}
			if delay, ok := kimiJSONDelay(object["retry_after"], time.Second); ok {
				return delay, true
			}
			for _, key := range []string{"reset_time", "reset_at"} {
				var reset string
				if json.Unmarshal(object[key], &reset) == nil {
					if delay, ok := kimiResetDelay(reset); ok {
						return delay, true
					}
				}
			}
		}
	}
	match := kimiAbsoluteResetRE.FindStringSubmatch(body)
	if len(match) == 2 {
		return kimiResetDelay(match[1])
	}
	return 0, false
}

func kimiJSONDelay(raw json.RawMessage, unit time.Duration) (time.Duration, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	text := string(raw)
	if raw[0] == '"' {
		if json.Unmarshal(raw, &text) != nil {
			return 0, false
		}
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || value <= 0 || value > float64(1<<63-1)/float64(unit) {
		return 0, false
	}
	return time.Duration(value * float64(unit)), true
}

func kimiResetDelay(value string) (time.Duration, bool) {
	resetAt, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	delay := time.Until(resetAt) + 200*time.Millisecond
	return delay, delay > 0
}

func kimiUsageResetDelay(usage *KimiUsage, message string) (time.Duration, bool) {
	if usage == nil {
		return 0, false
	}
	message = strings.ToLower(message)
	var windows []*KimiUsageWindow
	switch {
	case strings.Contains(message, "5-hour"), strings.Contains(message, "5 hour"), strings.Contains(message, "5h"):
		windows = []*KimiUsageWindow{usage.Limit5H}
	case strings.Contains(message, "7-day"), strings.Contains(message, "7 day"), strings.Contains(message, "week"):
		windows = []*KimiUsageWindow{usage.Limit7D}
	case strings.Contains(message, "month") && strings.Contains(message, "code"):
		windows = []*KimiUsageWindow{usage.MonthCode}
	case strings.Contains(message, "month"):
		windows = []*KimiUsageWindow{usage.MonthTotal}
	default:
		windows = []*KimiUsageWindow{usage.Limit5H, usage.Limit7D, usage.MonthTotal, usage.MonthCode}
	}
	var shortest time.Duration
	for _, window := range windows {
		if window == nil || window.ResetAt == nil {
			continue
		}
		delay, ok := kimiResetDelay(*window.ResetAt)
		if ok && (shortest <= 0 || delay < shortest) {
			shortest = delay
		}
	}
	return shortest, shortest > 0
}
