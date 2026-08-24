package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
)

const (
	defaultLLMRetryMax      = 3
	defaultLLMRetryBase     = time.Second
	defaultLLMRetryMaxDelay = 60 * time.Second
)

// ResilientOptions configures retry and pacing for LLM calls.
type ResilientOptions struct {
	RetryMax int
	// RetryDisabled means retries were explicitly turned off (llm_retry_max: 0):
	// a zero RetryMax alone still falls back to the default, so the zero value
	// of this struct keeps its historical behavior.
	RetryDisabled bool
	RetryBase     time.Duration
	RetryMaxDelay time.Duration
	MinInterval   time.Duration
}

func (o ResilientOptions) withDefaults() ResilientOptions {
	out := o
	if out.RetryDisabled {
		out.RetryMax = 0
	} else if out.RetryMax <= 0 {
		out.RetryMax = defaultLLMRetryMax
	}
	if out.RetryBase <= 0 {
		out.RetryBase = defaultLLMRetryBase
	}
	if out.RetryMaxDelay <= 0 {
		out.RetryMaxDelay = defaultLLMRetryMaxDelay
	}
	return out
}

// ResilientOptionsFromAgent maps config.Agent LLM pacing fields to provider
// options. retryMax is the config-resolved value (Agent.EffectiveLLMRetryMax):
// an explicit 0 disables retries entirely.
func ResilientOptionsFromAgent(retryMax, retryBaseMS, minIntervalMS int) ResilientOptions {
	opts := ResilientOptions{RetryMax: retryMax, RetryDisabled: retryMax == 0}
	if retryBaseMS > 0 {
		opts.RetryBase = time.Duration(retryBaseMS) * time.Millisecond
	}
	if minIntervalMS > 0 {
		opts.MinInterval = time.Duration(minIntervalMS) * time.Millisecond
	}
	return opts.withDefaults()
}

type resilientProvider struct {
	inner Provider
	opts  ResilientOptions
	mu    sync.Mutex
	last  time.Time
}

func wrapResilient(inner Provider, opts ResilientOptions) Provider {
	if inner == nil {
		return nil
	}
	return &resilientProvider{inner: inner, opts: opts.withDefaults()}
}

func (p *resilientProvider) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	return p.callWithRetry(ctx, func(ctx context.Context) (*Response, error) {
		return p.inner.Complete(ctx, messages, tools)
	})
}

func (p *resilientProvider) Stream(ctx context.Context, messages []Message, tools []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
	return p.callWithRetry(ctx, func(ctx context.Context) (*Response, error) {
		return p.inner.Stream(ctx, messages, tools, onChunk)
	})
}

func (p *resilientProvider) callWithRetry(ctx context.Context, fn func(context.Context) (*Response, error)) (*Response, error) {
	var lastErr error
	for attempt := 0; attempt <= p.opts.RetryMax; attempt++ {
		// Inside the loop so llm_min_interval_ms paces retry attempts too, not
		// only fresh calls: the pause stacks with the retry delay below, and
		// the effective gap is whichever of the two is longer.
		if err := p.waitMinInterval(ctx); err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		resp, err := fn(ctx)
		p.markCallFinished()
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if ctx.Err() != nil || !isRetryableLLMError(err) || attempt >= p.opts.RetryMax {
			return resp, err
		}
		delay := retryDelayForError(err, attempt, p.opts.RetryBase, p.opts.RetryMaxDelay)
		if delay <= 0 {
			delay = p.opts.RetryBase
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return resp, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (p *resilientProvider) waitMinInterval(ctx context.Context) error {
	if p.opts.MinInterval <= 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last.IsZero() {
		return nil
	}
	wait := p.opts.MinInterval - time.Since(p.last)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p *resilientProvider) markCallFinished() {
	if p.opts.MinInterval <= 0 {
		return
	}
	p.mu.Lock()
	p.last = time.Now()
	p.mu.Unlock()
}

var limitResetRE = regexp.MustCompile(`(?i)Limit resets at:\s*([0-9]{4}-[0-9]{2}-[0-9]{2}\s+[0-9]{2}:[0-9]{2}:[0-9]{2})\s*UTC`)

func parseLimitResetDelay(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	m := limitResetRE.FindStringSubmatch(err.Error())
	if len(m) != 2 {
		return 0, false
	}
	t, parseErr := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(m[1]), time.UTC)
	if parseErr != nil {
		return 0, false
	}
	d := time.Until(t)
	if d < 0 {
		return 0, false
	}
	return d + 200*time.Millisecond, true
}

// retryInRE matches the relative "retry in 31s" phrase gateways print in
// 429 bodies (api.neuraldeep.ru among them) when no absolute reset time is
// present.
var retryInRE = regexp.MustCompile(`(?i)\bretry in\s+([0-9]+(?:\.[0-9]+)?)\s*s(?:ec(?:onds?)?)?\b`)

func parseRetryInDelay(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	m := retryInRE.FindStringSubmatch(err.Error())
	if len(m) != 2 {
		return 0, false
	}
	secs, parseErr := strconv.ParseFloat(m[1], 64)
	if parseErr != nil || secs <= 0 {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}

// responseHeadersFromError digs the response headers out of a wrapped SDK
// error. Coddy owns retries (features/llm_retry_ownership.feature), so the
// header parsing the SDKs would have done lives here instead.
func responseHeadersFromError(err error) (http.Header, bool) {
	var oai *openai.Error
	if errors.As(err, &oai) && oai.Response != nil {
		return oai.Response.Header, true
	}
	var ant *anthropic.Error
	if errors.As(err, &ant) && ant.Response != nil {
		return ant.Response.Header, true
	}
	return nil, false
}

// parseRetryAfterHeaders reads the server-requested pause from response
// headers: Retry-After-Ms (milliseconds) first — the more precise header
// wins, matching the SDKs' own order — then Retry-After as either delay
// seconds or an HTTP-date. Unparsable or non-positive values fall through
// so the next delay source gets its chance.
func parseRetryAfterHeaders(err error) (time.Duration, bool) {
	h, ok := responseHeadersFromError(err)
	if !ok {
		return 0, false
	}
	if ms := strings.TrimSpace(h.Get("Retry-After-Ms")); ms != "" {
		if v, parseErr := strconv.ParseFloat(ms, 64); parseErr == nil && v > 0 {
			return time.Duration(v * float64(time.Millisecond)), true
		}
	}
	ra := strings.TrimSpace(h.Get("Retry-After"))
	if ra == "" {
		return 0, false
	}
	if v, parseErr := strconv.ParseFloat(ra, 64); parseErr == nil {
		if v > 0 {
			return time.Duration(v * float64(time.Second)), true
		}
		return 0, false
	}
	if t, parseErr := http.ParseTime(ra); parseErr == nil {
		if d := time.Until(t); d > 0 {
			// HTTP-dates carry second resolution; the same pad as
			// parseLimitResetDelay absorbs clock skew.
			return d + 200*time.Millisecond, true
		}
	}
	return 0, false
}

// serverRetryDelay extracts the pause the server asked for, in priority
// order: response headers, the absolute "Limit resets at: ... UTC" body
// phrase, the relative "retry in Ns" body phrase.
func serverRetryDelay(err error) (time.Duration, bool) {
	if d, ok := parseRetryAfterHeaders(err); ok {
		return d, true
	}
	if d, ok := parseLimitResetDelay(err); ok {
		return d, true
	}
	if d, ok := parseRetryInDelay(err); ok {
		return d, true
	}
	return 0, false
}

func retryDelayForError(err error, attempt int, base, maxDelay time.Duration) time.Duration {
	if d, ok := serverRetryDelay(err); ok {
		if d > maxDelay {
			return maxDelay
		}
		return d
	}
	if base <= 0 {
		base = defaultLLMRetryBase
	}
	delay := base << attempt
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

func isRetryableLLMError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var trunc *streamTruncatedError
	if errors.As(err, &trunc) {
		// A truncated stream is a transient transport failure worth the
		// configured retries, but only while nothing reached the caller:
		// replaying after emitted deltas would show the same text twice.
		return !trunc.emitted
	}
	var transport *streamTransportError
	if errors.As(err, &transport) && transport.emitted {
		// Same emitted contract for transport failures mid-stream; a fresh
		// one falls through to normal classification of its cause.
		return false
	}
	switch httpStatusFromError(err) {
	case 429, 408, 500, 502, 503, 504:
		return true
	}
	return isTransientTransportError(err)
}

// isTransientTransportError reports network-level failures that carry no
// HTTP status yet are worth repeating: the connection died, not the request.
// The substring needles cover error types the standard library keeps
// internal (http2 bundle errors) or stringified along the way.
func isTransientTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	s := err.Error()
	for _, needle := range []string{
		"http2: stream error",
		"http2: server sent GOAWAY",
		"connection reset by peer",
		"unexpected EOF",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func httpStatusFromError(err error) int {
	if err == nil {
		return 0
	}
	var oai *openai.Error
	if errors.As(err, &oai) && oai.StatusCode > 0 {
		return oai.StatusCode
	}
	var sse *streamServerError
	if errors.As(err, &sse) {
		// The typed streamed error is authoritative: no fallthrough to the
		// substring matcher below, whose needles could occur inside the
		// server-provided message text. After chunks already reached the
		// caller the error must not classify as retryable at all: replaying
		// the request would emit the same deltas a second time.
		if sse.emitted {
			return 0
		}
		return sse.code
	}
	var ant *anthropic.Error
	if errors.As(err, &ant) && ant.StatusCode > 0 {
		return ant.StatusCode
	}
	s := err.Error()
	for _, code := range []int{429, 408, 500, 502, 503, 504} {
		needle := strconv.Itoa(code)
		if strings.Contains(s, needle+" ") || strings.Contains(s, `"code":"`+needle+`"`) {
			return code
		}
	}
	return 0
}

// WithAgentResilience copies agent LLM pacing settings into ProviderInput.
// retryMax is the config-resolved value; an explicit 0 disables retries.
func WithAgentResilience(in ProviderInput, retryMax, retryBaseMS, minIntervalMS int) ProviderInput {
	ro := ResilientOptionsFromAgent(retryMax, retryBaseMS, minIntervalMS)
	in.RetryMax = ro.RetryMax
	in.RetryDisabled = ro.RetryDisabled
	in.RetryBase = ro.RetryBase
	in.RetryMaxDelay = ro.RetryMaxDelay
	in.MinInterval = ro.MinInterval
	return in
}

func applyResilientWrap(p Provider, in ProviderInput) Provider {
	return wrapResilient(p, ResilientOptions{
		RetryMax:      in.RetryMax,
		RetryDisabled: in.RetryDisabled,
		RetryBase:     in.RetryBase,
		RetryMaxDelay: in.RetryMaxDelay,
		MinInterval:   in.MinInterval,
	}.withDefaults())
}
