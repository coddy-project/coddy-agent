package llm

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// The outcomes CallOutcome sorts an attempt into, in the order CallOutcomes
// lists them.
const (
	outcomeOK              = "ok"
	outcomeCanceled        = "canceled"
	outcomeTimeout         = "timeout"
	outcomeRateLimited     = "rate_limited"
	outcomeAuth            = "auth"
	outcomeContextOverflow = "context_overflow"
	outcomeClientError     = "client_error"
	outcomeServerError     = "server_error"
	outcomeError           = "error"
)

// CallOutcomes lists every outcome CallOutcome can return, in a fixed order,
// as a slice of its own.
func CallOutcomes() []string {
	return []string{
		outcomeOK, outcomeCanceled, outcomeTimeout, outcomeRateLimited, outcomeAuth,
		outcomeContextOverflow, outcomeClientError, outcomeServerError, outcomeError,
	}
}

// CallEvent is one invocation attempt of a provider: the call into the
// provider that the resilient wrapper makes, so a call that is retried
// produces one event per try. Most attempts send one HTTP request; some fail
// before sending, while a credential or the model catalog is being resolved.
// It is a plain value, strings and numbers only, so an observer cannot keep a
// request, a message or a response alive, and nothing it does reaches what is
// sent.
type CallEvent struct {
	// Provider and Model are ProviderInput.Name and .Model: the configured
	// row and the API model id, with no provider prefix.
	Provider, Model string
	// Outcome is CallOutcome of the attempt's error.
	Outcome string
	// Retry is true for an attempt the wrapper made after a failed one of
	// the same call.
	Retry bool
	// Duration is the whole attempt, the full stream included.
	Duration time.Duration
	// FirstContent is how long the attempt took to deliver its first chunk
	// with content (text, reasoning or a tool call), zero for an attempt that
	// streamed none to a caller: a non-streaming call, a stream without a
	// callback, or one that ended before any content.
	FirstContent time.Duration
	// The tokens of the attempt's Response, whenever one exists (a stream cut
	// by the caller keeps what it delivered), else zero.
	InputTokens, OutputTokens, CachedInputTokens int
}

type callObserver struct{ fn func(CallEvent) }

var (
	callObserversMu sync.Mutex
	// callObservers is replaced whole on every change, nil while there is no
	// observer, so a call without one costs a single atomic load.
	callObservers atomic.Pointer[[]*callObserver]
)

// AddCallObserver registers fn for every invocation attempt of every provider
// in the process and returns the function that removes it again, which is
// safe to call twice.
//
// fn runs on the goroutine that made the attempt, right after it returned and
// before the wrapper decides on a retry, and MUST NOT block: hand the event on
// and return, like a session.Manager.AddTurnObserver observer.
//
// Not attempts, and so never observed: model listing, usage reads, sign-in, and
// the NeuralDeep decisions request. A call that ends before it reaches the
// provider (the minimum interval wait, a context already done) is not one
// either.
func AddCallObserver(fn func(CallEvent)) (remove func()) {
	if fn == nil {
		return func() {}
	}
	o := &callObserver{fn: fn}
	swapCallObservers(func(cur []*callObserver) []*callObserver { return append(slices.Clip(cur), o) })
	var once sync.Once
	return func() {
		once.Do(func() {
			swapCallObservers(func(cur []*callObserver) []*callObserver {
				return slices.DeleteFunc(slices.Clone(cur), func(x *callObserver) bool { return x == o })
			})
		})
	}
}

// swapCallObservers replaces the observer list with change applied to a copy.
func swapCallObservers(change func(cur []*callObserver) []*callObserver) {
	callObserversMu.Lock()
	defer callObserversMu.Unlock()
	var cur []*callObserver
	if p := callObservers.Load(); p != nil {
		cur = *p
	}
	if next := change(cur); len(next) > 0 {
		callObservers.Store(&next)
	} else {
		callObservers.Store(nil)
	}
}

// attemptClock times one invocation attempt: when it began, whether it is a
// retry, and when Stream saw its first chunk with content.
type attemptClock struct {
	began time.Time
	retry bool
	// firstContent is the time from began to the first chunk with content,
	// in nanoseconds; zero until one arrives.
	firstContent atomic.Int64
}

// watch returns onChunk wrapped to note the first chunk with content. A nil
// callback stays nil, and so does every callback while nobody observes: the
// adapters read a nil callback as a call that streams nothing to a caller,
// which decides whether a failed stream may be retried.
func (c *attemptClock) watch(onChunk func(StreamChunk)) func(StreamChunk) {
	if onChunk == nil || callObservers.Load() == nil {
		return onChunk
	}
	return func(chunk StreamChunk) {
		if c.firstContent.Load() == 0 && chunkHasContent(chunk) {
			c.firstContent.CompareAndSwap(0, max(1, int64(time.Since(c.began))))
		}
		onChunk(chunk)
	}
}

// chunkHasContent reports a chunk that carries something the model wrote, as
// opposed to usage figures or a stop reason alone.
func chunkHasContent(c StreamChunk) bool {
	return c.TextDelta != "" || c.ReasoningDelta != "" || c.ToolCall != nil || c.ToolCallDelta != nil || c.ToolCallNamed != nil
}

// observeAttempt tells the observers about the attempt clock timed.
func (p *resilientProvider) observeAttempt(clock *attemptClock, resp *Response, err error) {
	observers := callObservers.Load()
	if observers == nil {
		return
	}
	ev := CallEvent{
		Provider: p.opts.Name, Model: p.opts.Model,
		Outcome:      CallOutcome(err),
		Retry:        clock.retry,
		Duration:     time.Since(clock.began),
		FirstContent: time.Duration(clock.firstContent.Load()),
	}
	if resp != nil {
		ev.InputTokens, ev.OutputTokens, ev.CachedInputTokens = resp.InputTokens, resp.OutputTokens, resp.CachedInputTokens
	}
	for _, o := range *observers {
		o.fn(ev)
	}
}

// CallOutcome sorts the error of one attempt into one of CallOutcomes. The
// status comes from the typed errors the adapters return and, failing that,
// from the guess the retry classification makes; a failure inside an HTTP 200
// answer counts by the status its adapter error carries, and one that carries
// none is "error".
func CallOutcome(err error) string {
	switch {
	case err == nil:
		return outcomeOK
	case errors.Is(err, context.Canceled):
		return outcomeCanceled
	// Before the status: an overflow is reported as a 400, a 413, and in words.
	case IsContextOverflow(err):
		return outcomeContextOverflow
	case errors.Is(err, context.DeadlineExceeded), IsStreamStalled(err):
		return outcomeTimeout
	}
	status := UpstreamStatus(err)
	if status == 0 {
		status = httpStatusFromError(err)
	}
	switch {
	case status == 429:
		return outcomeRateLimited
	case status == 401, status == 403:
		return outcomeAuth
	case status >= 400 && status < 500:
		return outcomeClientError
	case status >= 500, IsStreamTruncated(err), isDialFailure(err), isTransientTransportError(err):
		return outcomeServerError
	}
	return outcomeError
}
