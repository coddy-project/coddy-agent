package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
