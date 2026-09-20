package llm

import (
	"context"
	"sync"
)

// RetryAllowance shares a count of extra attempts between the resilient wrapper
// and its caller's recovery loop. It belongs to one model step, not a provider:
// reusing a provider in another turn must not inherit the previous step's debt.
// This count is independent of ResilientOptions.RetryBudget, which bounds time.
type RetryAllowance struct {
	mu               sync.Mutex
	remaining        int
	attempts         int
	transportRetries int
}

// RetrySnapshot counts adapter calls actually started, not scheduled retries.
type RetrySnapshot struct {
	Remaining        int
	Attempts         int
	TransportRetries int
}

func NewRetryAllowance(retries int) *RetryAllowance {
	return &RetryAllowance{remaining: max(0, retries)}
}

type retryAllowanceKey struct{}

func WithRetryAllowance(ctx context.Context, allowance *RetryAllowance) context.Context {
	return context.WithValue(ctx, retryAllowanceKey{}, allowance)
}

func retryAllowanceFrom(ctx context.Context) *RetryAllowance {
	allowance, _ := ctx.Value(retryAllowanceKey{}).(*RetryAllowance)
	return allowance
}

// TakeRetry reserves an extra attempt. A nil allowance leaves standalone
// providers governed only by their own ResilientOptions, as before.
func (a *RetryAllowance) TakeRetry() bool {
	if a == nil {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.remaining == 0 {
		return false
	}
	a.remaining--
	return true
}

func (a *RetryAllowance) Snapshot() RetrySnapshot {
	if a == nil {
		return RetrySnapshot{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return RetrySnapshot{Remaining: a.remaining, Attempts: a.attempts, TransportRetries: a.transportRetries}
}

func (a *RetryAllowance) recordAttempt(retry bool) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attempts++
	if retry {
		a.transportRetries++
	}
}
