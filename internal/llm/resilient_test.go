package llm

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

type stubProvider struct {
	streamFn   func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error)
	completeFn func(context.Context, []Message, []ToolDefinition) (*Response, error)
}

func (s *stubProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	if s.completeFn != nil {
		return s.completeFn(ctx, messages, tools)
	}
	return nil, errors.New("complete not implemented")
}

func (s *stubProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	if s.streamFn != nil {
		return s.streamFn(ctx, messages, tools, onChunk)
	}
	return nil, errors.New("stream not implemented")
}

func err429Neuraldeep() error {
	return fmt.Errorf(`openai stream: POST "https://api.neuraldeep.ru/v1/chat/completions": 429 Too Many Requests {"message":"Rate limit exceeded for api_key: x. Limit type: requests. Current limit: 5, Remaining: 0. Limit resets at: 2026-05-23 10:20:14 UTC","type":"None","param":"None","code":"429"}`)
}

func TestHTTPStatusFromError_openai429(t *testing.T) {
	if got := httpStatusFromError(err429Neuraldeep()); got != 429 {
		t.Fatalf("status=%d want 429", got)
	}
}

func TestIsRetryableLLMError(t *testing.T) {
	if !isRetryableLLMError(err429Neuraldeep()) {
		t.Fatal("429 should be retryable")
	}
	for name, err := range map[string]error{
		"400":      errors.New("openai stream: 400 Bad Request"),
		"cancel":   context.Canceled,
		"deadline": context.DeadlineExceeded,
	} {
		t.Run(name, func(t *testing.T) {
			if isRetryableLLMError(err) {
				t.Fatalf("%s should not be retryable", name)
			}
		})
	}
}

func TestParseLimitResetDelay(t *testing.T) {
	resetAt := time.Now().UTC().Add(2 * time.Second).Truncate(time.Second)
	msg := fmt.Sprintf(`Limit resets at: %s UTC`, resetAt.Format("2006-01-02 15:04:05"))
	d, ok := parseLimitResetDelay(errors.New(msg))
	if !ok {
		t.Fatal("expected parse ok")
	}
	if d < time.Second || d > 5*time.Second {
		t.Fatalf("delay=%v want ~2s", d)
	}
}

func TestResilientProviderRetries429UntilSuccess(t *testing.T) {
	var calls atomic.Int32
	inner := &stubProvider{
		streamFn: func(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
			n := calls.Add(1)
			if n < 3 {
				return nil, err429Neuraldeep()
			}
			return &Response{Content: "done", StopReason: "end_turn"}, nil
		},
	}
	p := wrapResilient(inner, ResilientOptions{
		RetryMax:      3,
		RetryBase:     5 * time.Millisecond,
		RetryMaxDelay: time.Second,
	})
	resp, err := p.Stream(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp == nil || resp.Content != "done" {
		t.Fatalf("resp=%+v", resp)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d want 3", calls.Load())
	}
}

func TestResilientProviderDoesNotRetry400(t *testing.T) {
	var calls atomic.Int32
	inner := &stubProvider{
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			calls.Add(1)
			return nil, fmt.Errorf("openai stream: 400 Bad Request")
		},
	}
	p := wrapResilient(inner, ResilientOptions{RetryMax: 3, RetryBase: time.Millisecond})
	_, err := p.Stream(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d want 1", calls.Load())
	}
}

func TestResilientProviderEnforcesMinInterval(t *testing.T) {
	start := time.Now()
	p := wrapResilient(&stubProvider{
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			return &Response{StopReason: "end_turn"}, nil
		},
	}, ResilientOptions{MinInterval: 100 * time.Millisecond})
	if _, err := p.Stream(context.Background(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stream(context.Background(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Fatalf("expected pacing wait, elapsed=%v", elapsed)
	}
}

func TestRetryDelayForErrorUsesResetTime(t *testing.T) {
	resetAt := time.Now().UTC().Add(3 * time.Second).Truncate(time.Second)
	err := fmt.Errorf("Limit resets at: %s UTC", resetAt.Format("2006-01-02 15:04:05"))
	d := retryDelayForError(err, 0, time.Second, time.Minute)
	if d < 2*time.Second || d > 5*time.Second {
		t.Fatalf("delay=%v", d)
	}
}

func TestRetryDelayForErrorExponentialBackoff(t *testing.T) {
	err := errors.New("503 Service Unavailable")
	d0 := retryDelayForError(err, 0, 100*time.Millisecond, time.Second)
	d1 := retryDelayForError(err, 1, 100*time.Millisecond, time.Second)
	if d1 <= d0 {
		t.Fatalf("backoff should increase: d0=%v d1=%v", d0, d1)
	}
}

func TestProviderInputDefaultsApplyResilientWrap(t *testing.T) {
	p, err := NewProvider(ProviderInput{Type: "openai", Model: "gpt-4o", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*resilientProvider); !ok {
		t.Fatalf("expected resilientProvider, got %T", p)
	}
}

func TestErrorStringContains429(t *testing.T) {
	if !strings.Contains(err429Neuraldeep().Error(), "429") {
		t.Fatal("fixture should contain 429")
	}
}

// retryHTTPError builds a wrapped SDK error carrying real response headers,
// the shape retryDelayForError sees after a 429/5xx from either provider.
// Both SDK Error() methods dereference Request and Response, so the fixture
// populates both.
func retryHTTPError(t *testing.T, provider string, status int, hdr map[string]string) error {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://api.example.test/v1/chat/completions", nil)
	h := make(http.Header)
	for k, v := range hdr {
		h.Set(k, v)
	}
	resp := &http.Response{StatusCode: status, Header: h}
	switch provider {
	case "openai":
		return fmt.Errorf("openai complete: %w", &openai.Error{StatusCode: status, Request: req, Response: resp})
	case "anthropic":
		return fmt.Errorf("anthropic complete: %w", &anthropic.Error{StatusCode: status, Request: req, Response: resp})
	}
	t.Fatalf("unknown provider %q", provider)
	return nil
}

func TestRetryDelayHonorsRetryAfterSeconds(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			err := retryHTTPError(t, provider, 429, map[string]string{"Retry-After": "31"})
			if d := retryDelayForError(err, 0, time.Second, time.Minute); d != 31*time.Second {
				t.Fatalf("delay = %v, want 31s", d)
			}
		})
	}
}

func TestRetryDelayHonorsRetryAfterMs(t *testing.T) {
	err := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After-Ms": "1500"})
	if d := retryDelayForError(err, 0, time.Second, time.Minute); d != 1500*time.Millisecond {
		t.Fatalf("delay = %v, want 1.5s", d)
	}
}

func TestRetryDelayMsHeaderBeatsSecondsHeader(t *testing.T) {
	err := retryHTTPError(t, "openai", 429, map[string]string{
		"Retry-After-Ms": "1500",
		"Retry-After":    "10",
	})
	if d := retryDelayForError(err, 0, time.Second, time.Minute); d != 1500*time.Millisecond {
		t.Fatalf("delay = %v, want 1.5s (Retry-After-Ms must win)", d)
	}
}

func TestRetryDelayHonorsRetryAfterHTTPDate(t *testing.T) {
	date := time.Now().UTC().Add(3 * time.Second).Format(http.TimeFormat)
	err := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After": date})
	d := retryDelayForError(err, 0, time.Second, time.Minute)
	// http.TimeFormat has second resolution, so allow one second of slack on
	// both sides plus the anti-skew pad. The lower bound also proves the
	// 1s exponential base did not win.
	if d < 2*time.Second || d > 5*time.Second {
		t.Fatalf("delay = %v, want ~3s", d)
	}
}

func TestRetryDelayHeaderBeatsBodyPhrase(t *testing.T) {
	// The body names a reset far in the future; the header must still win.
	inner := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After": "2"})
	err := fmt.Errorf("Limit resets at: 2199-01-01 00:00:00 UTC: %w", inner)
	if d := retryDelayForError(err, 0, time.Second, time.Minute); d != 2*time.Second {
		t.Fatalf("delay = %v, want 2s (header must beat body text)", d)
	}
}

func TestRetryDelayCapsHeaderAtMaxDelay(t *testing.T) {
	err := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After": "120"})
	if d := retryDelayForError(err, 0, time.Second, time.Minute); d != time.Minute {
		t.Fatalf("delay = %v, want cap at 1m", d)
	}
}

func TestRetryDelayGarbageHeaderFallsBackToExponential(t *testing.T) {
	for _, bad := range []string{"soon", "0", "-5", ""} {
		t.Run(fmt.Sprintf("value=%q", bad), func(t *testing.T) {
			err := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After": bad})
			if d := retryDelayForError(err, 1, time.Second, time.Minute); d != 2*time.Second {
				t.Fatalf("delay = %v, want exponential 2s", d)
			}
		})
	}
}

func TestRetryDelayParsesRetryInBodyPhrase(t *testing.T) {
	err := fmt.Errorf(`openai stream: POST "https://api.neuraldeep.ru/v1/chat/completions": 429 Too Many Requests {"error":{"message":"qwen3.8-27b rate limit 6/min reached - retry in 31s","type":"rate_limit_error","code":"429"}}`)
	if d := retryDelayForError(err, 0, time.Second, time.Minute); d != 31*time.Second {
		t.Fatalf("delay = %v, want 31s from body phrase", d)
	}
}

// TestTransientTransportErrorClassification verifies that transport failures
// carrying no HTTP status (unexpected EOF, connection reset, http2 stream
// errors) classify as retryable, while arbitrary failures stay final.
func TestTransientTransportErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unexpected EOF", fmt.Errorf("openai stream: %w", io.ErrUnexpectedEOF), true},
		{"connection reset", fmt.Errorf("openai stream: %w",
			&net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}), true},
		{"http2 stream error text", errors.New(`openai stream: POST "https://api.example.test": http2: stream error: stream ID 5; INTERNAL_ERROR; received from peer`), true},
		{"plain failure", errors.New("openai stream: boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableLLMError(tc.err); got != tc.want {
				t.Fatalf("retryable = %v, want %v for %v", got, tc.want, tc.err)
			}
		})
	}
}

// TestStreamTransportErrorEmittedBlocksRetry pins the emitted contract for
// mid-stream transport failures: retry only while nothing reached the
// caller, and never for deterministic (non-transient) causes.
func TestStreamTransportErrorEmittedBlocksRetry(t *testing.T) {
	fresh := fmt.Errorf("openai stream: %w", &streamTransportError{cause: io.ErrUnexpectedEOF})
	if !isRetryableLLMError(fresh) {
		t.Fatal("transport cut before any delta must be retryable")
	}
	emitted := fmt.Errorf("openai stream: %w", &streamTransportError{cause: io.ErrUnexpectedEOF, emitted: true})
	if isRetryableLLMError(emitted) {
		t.Fatal("transport cut after emitted deltas must not be retryable")
	}
	odd := fmt.Errorf("openai stream: %w", &streamTransportError{cause: bufio.ErrTooLong})
	if isRetryableLLMError(odd) {
		t.Fatal("a deterministic cause must stay non-retryable even before output")
	}
}

// TestResilientProviderAppliesMinIntervalBetweenRetries verifies that
// llm_min_interval_ms paces retry attempts too, not only fresh calls.
func TestResilientProviderAppliesMinIntervalBetweenRetries(t *testing.T) {
	const minInterval = 60 * time.Millisecond
	var calls []time.Time
	inner := &stubProvider{
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			calls = append(calls, time.Now())
			if len(calls) < 3 {
				return nil, errors.New("503 Service Unavailable")
			}
			return &Response{Content: "done", StopReason: "end_turn"}, nil
		},
	}
	p := wrapResilient(inner, ResilientOptions{
		RetryMax:      3,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: 2 * time.Millisecond,
		MinInterval:   minInterval,
	})
	if _, err := p.Stream(context.Background(), nil, nil, nil); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	for i := 1; i < len(calls); i++ {
		// Allow a small scheduling slack below the configured interval.
		if gap := calls[i].Sub(calls[i-1]); gap < minInterval-10*time.Millisecond {
			t.Fatalf("gap %d = %v, want >= %v", i, gap, minInterval)
		}
	}
}

// TestResilientOptionsExplicitZeroDisablesRetries verifies that a
// config-resolved llm_retry_max of 0 means a single attempt, while the
// zero-value ProviderInput keeps the default retry budget.
func TestResilientOptionsExplicitZeroDisablesRetries(t *testing.T) {
	var calls atomic.Int32
	inner := &stubProvider{
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			calls.Add(1)
			return nil, errors.New("503 Service Unavailable")
		},
	}
	p := wrapResilient(inner, ResilientOptionsFromAgent(0, 1, 0))
	if _, err := p.Stream(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (explicit zero disables retries)", calls.Load())
	}
	if got := (ResilientOptions{}).withDefaults().RetryMax; got != defaultLLMRetryMax {
		t.Fatalf("zero-value RetryMax = %d, want default %d", got, defaultLLMRetryMax)
	}
}

func TestStreamTruncationRetryClassification(t *testing.T) {
	notEmitted := fmt.Errorf("openai stream: %w", &streamTruncatedError{emitted: false})
	if !isRetryableLLMError(notEmitted) {
		t.Fatal("truncation before any delta must be retryable")
	}
	emitted := fmt.Errorf("openai stream: %w", &streamTruncatedError{emitted: true})
	if isRetryableLLMError(emitted) {
		t.Fatal("truncation after emitted deltas must not be retryable")
	}
	if !IsStreamTruncated(notEmitted) || !IsStreamTruncated(emitted) {
		t.Fatal("IsStreamTruncated must match both wrapped variants")
	}
	if IsStreamTruncated(errors.New("stream truncated")) {
		t.Fatal("IsStreamTruncated must not match by message text")
	}
}

// A server-requested pause the remaining retries could never cover fails
// fast as a typed quota reset error after one call; a pause inside the
// budget is retried as before. The budget is RetryMaxDelay per wait still
// available (RetryMax - attempt), capped by RetryBudget when set; retries
// disabled have no waits, so every named pause is a reset.
func TestResilientProviderFailsFastPastTheRetryBudget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		retryMax  int
		disabled  bool
		budget    time.Duration
		pauseMS   string
		wantCalls int32
		wantReset bool
		wantDelay time.Duration
	}{
		{name: "past the budget", retryMax: 3, pauseMS: "500", wantCalls: 1, wantReset: true, wantDelay: 500 * time.Millisecond},
		{name: "inside the budget", retryMax: 3, pauseMS: "150", wantCalls: 2},
		{name: "retries disabled have no budget", disabled: true, pauseMS: "150", wantCalls: 1, wantReset: true, wantDelay: 150 * time.Millisecond},
		{name: "the caller's budget caps the ladder", retryMax: 3, budget: 120 * time.Millisecond, pauseMS: "150", wantCalls: 1, wantReset: true, wantDelay: 150 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			cause := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After-Ms": tc.pauseMS})
			inner := &stubProvider{
				streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
					if calls.Add(1) == 1 {
						return nil, cause
					}
					return &Response{Content: "done", StopReason: "end_turn"}, nil
				},
			}
			p := wrapResilient(inner, ResilientOptions{
				RetryMax:       tc.retryMax,
				RetryDisabled:  tc.disabled,
				RetryBase:      5 * time.Millisecond,
				RetryMaxDelay:  100 * time.Millisecond,
				RetryBudget:    tc.budget,
				RetryBudgetSet: tc.budget > 0,
			})
			before := time.Now()
			_, err := p.Stream(context.Background(), nil, nil, nil)
			if calls.Load() != tc.wantCalls {
				t.Fatalf("calls=%d want %d (err %v)", calls.Load(), tc.wantCalls, err)
			}
			var reset *QuotaResetError
			if got := errors.As(err, &reset); got != tc.wantReset {
				t.Fatalf("QuotaResetError=%v want %v (err %v)", got, tc.wantReset, err)
			}
			if !tc.wantReset {
				if err != nil {
					t.Fatalf("Stream: %v", err)
				}
				return
			}
			if reset.Delay != tc.wantDelay {
				t.Fatalf("Delay=%v want %v", reset.Delay, tc.wantDelay)
			}
			if reset.ResetAt.Before(before.Add(tc.wantDelay - 50*time.Millisecond)) {
				t.Fatalf("ResetAt=%v is earlier than the pause implies", reset.ResetAt)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("the cause must stay reachable through errors.Is: %v", err)
			}
			if isRetryableLLMError(err) {
				t.Fatal("a quota reset error must not be retryable itself")
			}
			if time.Since(before) > 80*time.Millisecond {
				t.Fatalf("the wrapper waited %v instead of failing fast", time.Since(before))
			}
			if !strings.Contains(err.Error(), "resets") {
				t.Fatalf("error text should name the reset: %q", err.Error())
			}
		})
	}
}

// The caller's RetryBudget bounds the whole call, so the sleeps already taken
// count, and the request after a pause keeps a quarter of a small budget as
// headroom: three pauses of 100 ms under a 350 ms budget are slept twice
// (263 ms and then 158 ms left) and then reported as a reset (53 ms left),
// with the third request never repeated.
func TestResilientProviderBudgetCountsTheTimeAlreadySlept(t *testing.T) {
	var calls atomic.Int32
	cause := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After-Ms": "100"})
	inner := &stubProvider{
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			calls.Add(1)
			return nil, cause
		},
	}
	p := wrapResilient(inner, ResilientOptions{
		RetryMax:       5,
		RetryBase:      5 * time.Millisecond,
		RetryMaxDelay:  100 * time.Millisecond,
		RetryBudget:    350 * time.Millisecond,
		RetryBudgetSet: true,
	})
	before := time.Now()
	_, err := p.Stream(context.Background(), nil, nil, nil)
	var reset *QuotaResetError
	if !errors.As(err, &reset) {
		t.Fatalf("want a QuotaResetError once the budget is spent, got %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d want 3 (two sleeps inside the budget, then the verdict)", calls.Load())
	}
	if took := time.Since(before); took < 200*time.Millisecond || took > 400*time.Millisecond {
		t.Fatalf("the wrapper slept %v, want about 200 ms", took)
	}
}

// A budget set to zero means no sleep on a limit at all: the first named
// pause is reported, the request never repeated.
func TestResilientProviderZeroBudgetNeverSleeps(t *testing.T) {
	var calls atomic.Int32
	cause := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After-Ms": "10"})
	inner := &stubProvider{
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			calls.Add(1)
			return nil, cause
		},
	}
	p := wrapResilient(inner, ResilientOptions{RetryMax: 3, RetryBase: time.Millisecond, RetryMaxDelay: 100 * time.Millisecond, RetryBudgetSet: true})
	_, err := p.Stream(context.Background(), nil, nil, nil)
	var reset *QuotaResetError
	if !errors.As(err, &reset) || calls.Load() != 1 {
		t.Fatalf("want a reset after one call, got calls=%d err=%v", calls.Load(), err)
	}
}

// testLimitLedger is the caller's account of limit time in the tests.
type testLimitLedger struct {
	mu    sync.Mutex
	spent time.Duration
}

func (l *testLimitLedger) Spent() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.spent
}

func (l *testLimitLedger) Charge(d time.Duration) {
	l.mu.Lock()
	l.spent += d
	l.mu.Unlock()
}

// With a ledger attached the wrapper charges every sleep after a 429 and
// judges a new pause against what the ledger held before the call plus
// this call's own time, never booking a sleep twice: the same three 100 ms
// pauses under a 350 ms budget still give two sleeps and then the verdict,
// and the ledger ends up holding the two sleeps.
func TestResilientProviderLedgerCountsEachSleepOnce(t *testing.T) {
	var calls atomic.Int32
	cause := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After-Ms": "100"})
	inner := &stubProvider{
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			calls.Add(1)
			return nil, cause
		},
	}
	ledger := &testLimitLedger{}
	p := wrapResilient(inner, ResilientOptions{
		RetryMax:       5,
		RetryBase:      5 * time.Millisecond,
		RetryMaxDelay:  100 * time.Millisecond,
		RetryBudget:    350 * time.Millisecond,
		RetryBudgetSet: true,
		Ledger:         ledger,
	})
	_, err := p.Stream(context.Background(), nil, nil, nil)
	var reset *QuotaResetError
	if !errors.As(err, &reset) {
		t.Fatalf("want a QuotaResetError once the budget is spent, got %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d want 3 (two sleeps inside the budget, then the verdict)", calls.Load())
	}
	if spent := ledger.Spent(); spent < 200*time.Millisecond || spent > 300*time.Millisecond {
		t.Fatalf("ledger holds %v, want the two sleeps (about 200 ms)", spent)
	}
	// What the ledger held before a call counts: a second call with the
	// same budget finds nothing left and reports the first pause at once.
	calls.Store(0)
	_, err = p.Stream(context.Background(), nil, nil, nil)
	if !errors.As(err, &reset) || calls.Load() != 1 {
		t.Fatalf("a call after a spent ledger must fail at once, got calls=%d err=%v", calls.Load(), err)
	}
}

// A 429 that names no pause takes the ordinary backoff only while the
// caller's budget allows, and never becomes a reset: an empty budget ends
// the call with the provider's own error at once, a small one lets the
// backoff run until it is spent and then ends the call the same way.
func TestResilientProviderUnnamed429HonoursTheBudget(t *testing.T) {
	var calls atomic.Int32
	cause := retryHTTPError(t, "openai", 429, nil)
	inner := &stubProvider{
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			calls.Add(1)
			return nil, cause
		},
	}
	zero := wrapResilient(inner, ResilientOptions{RetryMax: 3, RetryBase: 50 * time.Millisecond, RetryMaxDelay: 100 * time.Millisecond, RetryBudgetSet: true})
	_, err := zero.Stream(context.Background(), nil, nil, nil)
	var reset *QuotaResetError
	if errors.As(err, &reset) || !errors.Is(err, cause) || calls.Load() != 1 {
		t.Fatalf("an empty budget must end an unnamed 429 with the provider's error at once, got calls=%d err=%v", calls.Load(), err)
	}
	calls.Store(0)
	small := wrapResilient(inner, ResilientOptions{
		RetryMax:       5,
		RetryBase:      100 * time.Millisecond,
		RetryMaxDelay:  100 * time.Millisecond,
		RetryBudget:    350 * time.Millisecond,
		RetryBudgetSet: true,
	})
	before := time.Now()
	_, err = small.Stream(context.Background(), nil, nil, nil)
	if errors.As(err, &reset) || !errors.Is(err, cause) || calls.Load() != 3 {
		t.Fatalf("a small budget lets two backoffs run and then ends with the provider's error, got calls=%d err=%v", calls.Load(), err)
	}
	if took := time.Since(before); took < 200*time.Millisecond || took > 400*time.Millisecond {
		t.Fatalf("the wrapper slept %v, want about 200 ms", took)
	}
	plain := wrapResilient(inner, ResilientOptions{RetryMax: 1, RetryBase: time.Millisecond, RetryMaxDelay: 100 * time.Millisecond})
	calls.Store(0)
	if _, err := plain.Stream(context.Background(), nil, nil, nil); errors.As(err, &reset) || calls.Load() != 2 {
		t.Fatalf("without a budget an unnamed 429 keeps the ordinary retry, got calls=%d err=%v", calls.Load(), err)
	}
}
