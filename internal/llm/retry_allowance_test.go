package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryAllowanceSharedAcrossCalls(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name                             string
			remaining, wrapperMax, wantCalls int
		}{
			{"disabled", 0, 3, 1},
			{"caller is tighter", 1, 3, 2},
			{"wrapper is tighter", 4, 1, 2},
		} {
			t.Run(tc.name+map[bool]string{true: "/stream", false: "/complete"}[stream], func(t *testing.T) {
				calls := 0
				fail := func() (*Response, error) { calls++; return nil, errors.New("500 Internal Server Error") }
				p := wrapResilient(&stubProvider{
					streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
						return fail()
					},
					completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) { return fail() },
				}, ResilientOptions{RetryMax: tc.wrapperMax, RetryBase: time.Millisecond})
				allowance := NewRetryAllowance(tc.remaining + 1)
				if !allowance.TakeRetry() {
					t.Fatal("reserve caller recovery")
				}
				ctx := WithRetryAllowance(context.Background(), allowance)
				call := func(ctx context.Context) error {
					if stream {
						_, err := p.Stream(ctx, nil, nil, nil)
						return err
					}
					_, err := p.Complete(ctx, nil, nil)
					return err
				}
				if err := call(ctx); err == nil {
					t.Fatal("expected error")
				}
				want := RetrySnapshot{Remaining: tc.remaining - (tc.wantCalls - 1), Attempts: tc.wantCalls, TransportRetries: tc.wantCalls - 1}
				if got := allowance.Snapshot(); got != want || calls != tc.wantCalls {
					t.Fatalf("snapshot=%+v calls=%d; want %+v", got, calls, want)
				}
				// A fresh step on the same provider gets a fresh allowance.
				fresh := NewRetryAllowance(1)
				if err := call(WithRetryAllowance(context.Background(), fresh)); err == nil {
					t.Fatal("expected error")
				}
				if got := fresh.Snapshot(); got.Attempts != 2 || got.Remaining != 0 {
					t.Fatalf("fresh step: %+v", got)
				}
			})
		}
	}
}

func TestRetryAllowanceCancelledCallDoesNotCountAnAttempt(t *testing.T) {
	allowance := NewRetryAllowance(3)
	ctx, cancel := context.WithCancel(WithRetryAllowance(context.Background(), allowance))
	cancel()
	p := wrapResilient(&stubProvider{}, ResilientOptions{RetryMax: 3})
	if _, err := p.Complete(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if got := allowance.Snapshot(); got != (RetrySnapshot{Remaining: 3}) {
		t.Fatalf("snapshot=%+v", got)
	}
}

func TestRetryAllowanceCancellationDuringBackoffCountsOnlyStartedCalls(t *testing.T) {
	allowance := NewRetryAllowance(1)
	ctx, cancel := context.WithTimeout(WithRetryAllowance(context.Background(), allowance), 100*time.Millisecond)
	defer cancel()
	p := wrapResilient(&stubProvider{completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) {
		return nil, errors.New("500 Internal Server Error")
	}}, ResilientOptions{RetryMax: 3, RetryBase: time.Second})
	if _, err := p.Complete(ctx, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	if got := allowance.Snapshot(); got.Attempts != 1 || got.TransportRetries != 0 {
		t.Fatalf("scheduled retry counted as sent: %+v", got)
	}
}

func TestRetryAllowanceExhaustedQuotaFailsTyped(t *testing.T) {
	allowance := NewRetryAllowance(0)
	p := wrapResilient(&stubProvider{completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) {
		return nil, errors.New("429 Too Many Requests; retry in 1s")
	}}, ResilientOptions{RetryMax: 3})
	_, err := p.Complete(WithRetryAllowance(context.Background(), allowance), nil, nil)
	var reset *QuotaResetError
	if !errors.As(err, &reset) {
		t.Fatalf("want typed quota error, got %v", err)
	}
	if got := allowance.Snapshot(); got.Attempts != 1 {
		t.Fatalf("snapshot=%+v", got)
	}
}
