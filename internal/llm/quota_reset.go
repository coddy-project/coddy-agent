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
	// Elapsed is what the call had already spent, retry sleeps on earlier
	// pauses included, when the wrapper gave up: a caller that budgets its
	// waiting counts it too.
	Elapsed time.Duration
	Cause   error
}

func (e *QuotaResetError) Error() string {
	return fmt.Sprintf("usage limit reached, resets at %s (in %s): %v",
		e.ResetAt.UTC().Format("15:04:05 UTC"), e.Delay.Round(time.Second), e.Cause)
}

func (e *QuotaResetError) Unwrap() error { return e.Cause }
