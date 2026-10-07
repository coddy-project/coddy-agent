package llm

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// The wait of a coddy provider for a free stream slot of the remote.
//
// A remote Coddy lets at most a handful of shared-model calls run at once per
// credential and answers the next one busy at once, never queueing it. The
// client waits that out inside Provider.Stream (decided by model m2-busy-wait):
// every caller waits - the agent loop, compaction, the direct completion
// routes, prompt enhancement and the children - because Complete is Stream.
//
// One monotonic deadline start+budget covers the requests, the sleeps and the
// jitter of admission only: once the remote answers 200 it no longer applies,
// and the stream ends by its own final or error frame. The time such a stream
// ran before it was cut is not charged to the wait of the attempt that follows
// it: the deadline moves by it when that attempt begins. The deadline belongs
// to one Stream call of the OUTER wrapper (coddyScope) and travels in the
// context, the way a RetryAllowance does, so a transport failure after a
// successful wait does not start a second wait; otherwise a call could wait
// (llm_retry_max + 1) times busy_wait_ms.

const (
	// busySleepFloor is the least a sleep lasts, and the first step of the
	// backoff; the backoff doubles up to busySleepCeiling.
	busySleepFloor   = time.Second
	busySleepCeiling = 5 * time.Second
	// busyJitter is the share of a sleep the jitter may add, upward only: it
	// never takes a sleep below the Retry-After the remote asked for.
	busyJitter = 0.2
	// busyRetryAfterCeiling is the longest Retry-After a sleep honours. The
	// sleep is cut to what is left of the budget anyway; the ceiling keeps the
	// arithmetic of the jitter far from the limits of a Duration, whatever the
	// remote claims.
	busyRetryAfterCeiling = time.Hour
)

// coddyClock is the provider's time, injectable so the wait is tested without
// sleeping.
type coddyClock struct {
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

func realCoddyClock() coddyClock {
	return coddyClock{
		now: time.Now,
		sleep: func(ctx context.Context, d time.Duration) error {
			if d <= 0 {
				return ctx.Err()
			}
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

// busyWaitState is the account of one Stream call of the outer wrapper.
type busyWaitState struct {
	mu       sync.Mutex
	started  bool
	start    time.Time
	deadline time.Time
	requests int
	busies   int
	// admittedAt is when the remote last took the call; the zero time while no
	// admitted stream is waiting to be accounted for. streamed is the time such
	// streams ran, which the wait does not count.
	admittedAt time.Time
	streamed   time.Duration
}

// begin fixes the deadline at the first request of the call. A later attempt of
// the same call keeps it, moved by the time the stream of the attempt before it
// ran: a stream that was admitted and then cut is not waiting for a slot.
func (st *busyWaitState) begin(now time.Time, budget time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.started {
		st.started = true
		st.start = now
		st.deadline = now.Add(budget)
		return
	}
	if !st.admittedAt.IsZero() {
		if ran := now.Sub(st.admittedAt); ran > 0 {
			st.deadline = st.deadline.Add(ran)
			st.streamed += ran
		}
		st.admittedAt = time.Time{}
	}
}

func (st *busyWaitState) noteRequest() {
	st.mu.Lock()
	st.requests++
	st.mu.Unlock()
}

// noteAdmitted records that the remote took the call at now: the stream that
// follows is not part of the wait.
func (st *busyWaitState) noteAdmitted(now time.Time) {
	st.mu.Lock()
	st.admittedAt = now
	st.mu.Unlock()
}

// noteBusy counts a busy answer and returns what the wait looks like now.
func (st *busyWaitState) noteBusy(now time.Time) (index, requests int, waited, remaining time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()
	index = st.busies
	st.busies++
	return index, st.requests, now.Sub(st.start) - st.streamed, st.deadline.Sub(now)
}

// progress is what the wait looks like at now, without counting anything.
func (st *busyWaitState) progress(now time.Time) (requests int, waited, remaining time.Duration) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.requests, now.Sub(st.start) - st.streamed, st.deadline.Sub(now)
}

type busyWaitKey struct{}

func withBusyWaitState(ctx context.Context, st *busyWaitState) context.Context {
	return context.WithValue(ctx, busyWaitKey{}, st)
}

func busyWaitStateFrom(ctx context.Context) *busyWaitState {
	st, _ := ctx.Value(busyWaitKey{}).(*busyWaitState)
	return st
}

// BusyWaitStatus is what a wait for a free slot reports before each sleep.
type BusyWaitStatus struct {
	// Budget is the whole wait the row allows.
	Budget time.Duration
	// Requests is how many requests the call has made so far.
	Requests int
	// Waited is how long the call has waited since its first request.
	Waited time.Duration
	// Remaining is what is left of the budget before this sleep.
	Remaining time.Duration
	// RetryIn is the sleep that follows, cut to what is left.
	RetryIn time.Duration
	// Admitted is set on the one report that ends a wait the remote ended by
	// taking the call: the countdown is over, RetryIn is zero and the stream
	// follows. A call that was not made to wait never sends it.
	Admitted bool
}

type busyWaitNotifyKey struct{}

// WithBusyWaitNotify returns a context whose calls report the countdown of
// their wait for a free stream slot of a remote Coddy to fn, before each
// sleep, and once more, with Admitted set, when the remote takes a call that
// had to wait. fn runs on the calling goroutine and must not block.
func WithBusyWaitNotify(ctx context.Context, fn func(BusyWaitStatus)) context.Context {
	return context.WithValue(ctx, busyWaitNotifyKey{}, fn)
}

func busyWaitNotifyFrom(ctx context.Context) func(BusyWaitStatus) {
	fn, _ := ctx.Value(busyWaitNotifyKey{}).(func(BusyWaitStatus))
	return fn
}

type busyWaitSleepKey struct{}

// WithBusyWaitSleep returns a context whose calls spend each sleep of their
// wait for a free stream slot of a remote Coddy with fn instead of the wall
// clock: fn is given the sleep the provider chose and returns when it is over,
// or with the context's error when the call is stopped. A host that drives time
// itself (a test, a simulation) uses it; the provider still decides how long
// each sleep lasts, tells the remote nothing different and counts the budget on
// its own clock, so only the time a sleep takes changes.
func WithBusyWaitSleep(ctx context.Context, fn func(ctx context.Context, d time.Duration) error) context.Context {
	return context.WithValue(ctx, busyWaitSleepKey{}, fn)
}

func busyWaitSleepFrom(ctx context.Context) func(ctx context.Context, d time.Duration) error {
	fn, _ := ctx.Value(busyWaitSleepKey{}).(func(ctx context.Context, d time.Duration) error)
	return fn
}

// busySleep is one sleep of the wait: the larger of the Retry-After the remote
// asked for (never less than a second) and a backoff that doubles from a
// second to a ceiling of five, with jitter in [0, 20%] added on top. jitter is
// a draw in [0, 1).
func busySleep(busyIndex int, retryAfter time.Duration, jitter float64) time.Duration {
	backoff := busySleepFloor << min(max(busyIndex, 0), 3)
	if backoff > busySleepCeiling {
		backoff = busySleepCeiling
	}
	// The jitter is a draw in [0, 1); anything else adds nothing, and a
	// Retry-After above the ceiling is honoured only up to it.
	if !(jitter > 0) {
		jitter = 0
	}
	jitter = min(jitter, 1)
	base := max(min(retryAfter, busyRetryAfterCeiling), busySleepFloor)
	if backoff > base {
		base = backoff
	}
	return base + time.Duration(float64(base)*busyJitter*jitter)
}

func randomJitter() float64 { return rand.Float64() }

// coddyScope is the outermost layer of a coddy provider. It starts the wait of
// one Stream (or Complete) call and carries it in the context through every
// attempt of the resilient wrapper below it.
type coddyScope struct {
	inner Provider
}

func newCoddyScope(inner Provider) Provider {
	if inner == nil {
		return nil
	}
	return &coddyScope{inner: inner}
}

func (s *coddyScope) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	return s.inner.Complete(withBusyWaitState(ctx, &busyWaitState{}), messages, tools)
}

func (s *coddyScope) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	return s.inner.Stream(withBusyWaitState(ctx, &busyWaitState{}), messages, tools, onChunk)
}
