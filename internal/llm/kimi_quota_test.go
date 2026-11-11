package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const kimiQuotaRefusal = `{"error":{"message":"You've reached your 5-hour usage limit. Your quota will reset when the current 5-hour window ends. To continue now, purchase extra usage or upgrade your plan: https://www.kimi.com/settings/subscription?tab=quota","type":"exceeded_current_quota_error"}}`

type kimiQuotaStand struct {
	inferenceStatus int
	inferenceBody   string
	inferenceHeader http.Header
	usageStatus     int
	usageBody       string
	inferenceCalls  int
	usageCalls      int
}

func (s *kimiQuotaStand) RoundTrip(req *http.Request) (*http.Response, error) {
	status := s.inferenceStatus
	body := s.inferenceBody
	header := s.inferenceHeader.Clone()
	if req.URL.Path == "/coding/v1/usages" {
		s.usageCalls++
		status = s.usageStatus
		body = s.usageBody
		header = make(http.Header)
	} else {
		s.inferenceCalls++
	}
	if status == 0 {
		status = http.StatusOK
	}
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func (s *kimiQuotaStand) client() *http.Client {
	return &http.Client{Transport: s}
}

func newKimiQuotaTestProvider(t *testing.T, protocol string, client *http.Client) Provider {
	t.Helper()
	transport, err := newKimiInferenceTransport("subscription", "global", protocol, func(context.Context) (string, error) {
		return "token", nil
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	input := ProviderInput{Model: "kimi"}
	switch protocol {
	case "kimi":
		return newKimiChatProvider(transport, input)
	case "anthropic":
		return newKimiAnthropicProvider(transport, input)
	case "openai_responses":
		return newKimiResponsesProvider(transport, input)
	default:
		t.Fatalf("unknown protocol %q", protocol)
		return nil
	}
}

func TestKimiQuotaErrorResolvesUsageReset(t *testing.T) {
	for _, protocol := range []string{"kimi", "anthropic", "openai_responses"} {
		t.Run(protocol, func(t *testing.T) {
			resetAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
			stand := &kimiQuotaStand{
				inferenceStatus: http.StatusTooManyRequests,
				inferenceBody:   kimiQuotaRefusal,
				usageStatus:     http.StatusOK,
				usageBody:       fmt.Sprintf(`{"usages":{"limit_5h":{"used_ratio":1,"reset_time":%q}}}`, resetAt.Format(time.RFC3339)),
			}
			provider := newKimiQuotaTestProvider(t, protocol, stand.client())
			wrapped := wrapResilient(provider, ResilientOptions{RetryDisabled: true})
			_, err := wrapped.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
			var reset *QuotaResetError
			if !errors.As(err, &reset) {
				t.Fatalf("error = %v (%T), want QuotaResetError", err, err)
			}
			if stand.inferenceCalls != 1 || stand.usageCalls != 1 {
				t.Fatalf("inference calls = %d, usage calls = %d, want one each", stand.inferenceCalls, stand.usageCalls)
			}
			if delta := reset.ResetAt.Sub(resetAt); delta < -time.Second || delta > time.Second {
				t.Fatalf("reset at = %s, want about %s", reset.ResetAt, resetAt)
			}
		})
	}
}

func TestKimiQuotaErrorWithBodyDelayDoesNotFetchUsage(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want time.Duration
	}{
		"retry after seconds": {
			body: `{"error":{"message":"quota reached","type":"exceeded_current_quota_error","retry_after":180}}`,
			want: 3 * time.Minute,
		},
		"retry after milliseconds": {
			body: `{"error":{"message":"quota reached","type":"exceeded_current_quota_error","retry_after_ms":45000}}`,
			want: 45 * time.Second,
		},
		"retry in prose": {
			body: `{"error":{"message":"quota reached, retry in 45 seconds","type":"exceeded_current_quota_error"}}`,
			want: 45 * time.Second,
		},
	} {
		t.Run(name, func(t *testing.T) {
			stand := &kimiQuotaStand{inferenceStatus: http.StatusTooManyRequests, inferenceBody: tc.body}
			provider := newKimiQuotaTestProvider(t, "kimi", stand.client())
			_, err := provider.Complete(context.Background(), nil, nil)
			if err == nil {
				t.Fatal("expected quota refusal")
			}
			if delay, ok := serverRetryDelay(err); !ok || delay != tc.want {
				t.Fatalf("delay = %v, present = %v, want %v; error: %v", delay, ok, tc.want, err)
			}
			if stand.usageCalls != 0 {
				t.Fatalf("usage calls = %d, want none", stand.usageCalls)
			}
		})
	}
}

func TestKimiQuotaErrorWithAbsoluteResetDoesNotFetchUsage(t *testing.T) {
	resetAt := time.Now().UTC().Add(90 * time.Second).Truncate(time.Second)
	stand := &kimiQuotaStand{
		inferenceStatus: http.StatusTooManyRequests,
		inferenceBody:   fmt.Sprintf(`{"error":{"message":"Your quota resets at %s","type":"exceeded_current_quota_error"}}`, resetAt.Format(time.RFC3339)),
	}
	provider := newKimiQuotaTestProvider(t, "kimi", stand.client())
	_, err := provider.Complete(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected quota refusal")
	}
	delay, ok := serverRetryDelay(err)
	if !ok || delay < 89*time.Second || delay > 92*time.Second {
		t.Fatalf("delay = %v, present = %v, want about 90s; error: %v", delay, ok, err)
	}
	if stand.usageCalls != 0 {
		t.Fatalf("usage calls = %d, want none", stand.usageCalls)
	}
}

func TestKimiRateLimitDoesNotFetchUsage(t *testing.T) {
	stand := &kimiQuotaStand{
		inferenceStatus: http.StatusTooManyRequests,
		inferenceBody:   `{"error":{"message":"Too many requests, retry later","type":"rate_limit_error"}}`,
	}
	provider := newKimiQuotaTestProvider(t, "kimi", stand.client())
	_, err := provider.Complete(context.Background(), nil, nil)
	if httpStatusFromError(err) != http.StatusTooManyRequests {
		t.Fatalf("error = %v, want 429", err)
	}
	if _, ok := serverRetryDelay(err); ok {
		t.Fatalf("unexpected retry delay for %v", err)
	}
	if stand.usageCalls != 0 {
		t.Fatalf("usage calls = %d, want none", stand.usageCalls)
	}
}

func TestKimiQuotaUsageFailureLeavesOriginalError(t *testing.T) {
	for name, usage := range map[string]struct {
		status int
		body   string
	}{
		"request fails": {status: http.StatusServiceUnavailable, body: `unavailable`},
		"no reset":      {status: http.StatusOK, body: `{"usages":{"limit_5h":{"used_ratio":1}}}`},
	} {
		t.Run(name, func(t *testing.T) {
			stand := &kimiQuotaStand{
				inferenceStatus: http.StatusTooManyRequests,
				inferenceBody:   kimiQuotaRefusal,
				usageStatus:     usage.status,
				usageBody:       usage.body,
			}
			provider := newKimiQuotaTestProvider(t, "kimi", stand.client())
			_, err := provider.Complete(context.Background(), nil, nil)
			if httpStatusFromError(err) != http.StatusTooManyRequests || !strings.Contains(err.Error(), "5-hour usage limit") {
				t.Fatalf("error = %v, want original quota refusal", err)
			}
			if _, ok := serverRetryDelay(err); ok {
				t.Fatalf("invented retry delay for %v", err)
			}
			if stand.usageCalls != 1 {
				t.Fatalf("usage calls = %d, want one", stand.usageCalls)
			}
		})
	}
}
