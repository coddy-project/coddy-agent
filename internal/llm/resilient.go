package llm

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	RetryMax      int
	RetryBase     time.Duration
	RetryMaxDelay time.Duration
	MinInterval   time.Duration
}

func (o ResilientOptions) withDefaults() ResilientOptions {
	out := o
	if out.RetryMax <= 0 {
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

// ResilientOptionsFromAgent maps config.Agent LLM pacing fields to provider options.
func ResilientOptionsFromAgent(retryMax, retryBaseMS, minIntervalMS int) ResilientOptions {
	opts := ResilientOptions{RetryMax: retryMax}
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
	if err := p.waitMinInterval(ctx); err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt <= p.opts.RetryMax; attempt++ {
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

func retryDelayForError(err error, attempt int, base, maxDelay time.Duration) time.Duration {
	if d, ok := parseLimitResetDelay(err); ok {
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
	switch httpStatusFromError(err) {
	case 429, 408, 500, 502, 503, 504:
		return true
	default:
		return false
	}
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
	if errors.As(err, &sse) && sse.code > 0 && !sse.emitted {
		// A streamed error after chunks already reached the caller must not
		// classify as retryable: replaying the request would emit the same
		// deltas a second time.
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
func WithAgentResilience(in ProviderInput, retryMax, retryBaseMS, minIntervalMS int) ProviderInput {
	ro := ResilientOptionsFromAgent(retryMax, retryBaseMS, minIntervalMS)
	in.RetryMax = ro.RetryMax
	in.RetryBase = ro.RetryBase
	in.RetryMaxDelay = ro.RetryMaxDelay
	in.MinInterval = ro.MinInterval
	return in
}

func applyResilientWrap(p Provider, in ProviderInput) Provider {
	return wrapResilient(p, ResilientOptions{
		RetryMax:      in.RetryMax,
		RetryBase:     in.RetryBase,
		RetryMaxDelay: in.RetryMaxDelay,
		MinInterval:   in.MinInterval,
	}.withDefaults())
}
