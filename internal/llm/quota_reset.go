package llm

import (
	"fmt"
	"time"
)

// QuotaResetError is the resilient wrapper's answer to a rate limit whose
// server-requested pause is longer than the capped retries could ever
// cover: retrying would burn the budget and end in the same 429 minutes
// later, so the call fails at once and names the moment the limit lifts.
// The agent decides whether to wait for it (agent.wait_for_limit_reset);
// every other caller sees a plain failure. Cause is the provider's error.
type QuotaResetError struct {
	ResetAt time.Time
	Delay   time.Duration
	Cause   error
}

func (e *QuotaResetError) Error() string {
	return fmt.Sprintf("usage limit reached, resets at %s (in %s): %v",
		e.ResetAt.UTC().Format("15:04:05 UTC"), e.Delay.Round(time.Second), e.Cause)
}

func (e *QuotaResetError) Unwrap() error { return e.Cause }

// LimitLedger is the caller's account of the time one unit of work (the
// agent's user turn) has spent on usage limits across its calls. The
// resilient wrapper charges every sleep it takes after a 429 to it, calls
// that succeed afterwards included, and reads the total when it judges a
// new pause against RetryBudget, so the budget is a total for the unit of
// work rather than for one call.
type LimitLedger interface {
	Spent() time.Duration
	Charge(d time.Duration)
}
