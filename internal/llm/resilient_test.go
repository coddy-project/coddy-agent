package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/devinfake"
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

// jsonCutError is the decoder's own error for a payload that stops between
// tokens, the cause a truncation inside an event carries.
func jsonCutError() error {
	var v any
	return json.Unmarshal([]byte(`{"a":`), &v)
}

func TestIsRetryableLLMError(t *testing.T) {
	if !isRetryableLLMError(err429Neuraldeep()) {
		t.Fatal("429 should be retryable")
	}
	for name, err := range map[string]error{
		"400":      errors.New("openai stream: 400 Bad Request"),
		"cancel":   context.Canceled,
		"deadline": context.DeadlineExceeded,
		// A frame that is not JSON is final whatever its text holds: the
		// status scan and the transport needles never see it.
		"undecodable frame with a status in its text": fmt.Errorf("openai stream: %w",
			&streamUndecodableError{snippet: "<html><title>502 Bad Gateway</title></html>", cause: errors.New("invalid character '<' looking for beginning of value")}),
		"undecodable frame with a transport phrase in its text": fmt.Errorf("openai stream: %w",
			&streamUndecodableError{snippet: "garbage unexpected EOF here", cause: errors.New("invalid character 'g' looking for beginning of value")}),
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
// errors, a TLS handshake or a dial that never completed) classify as
// retryable, while arbitrary failures, an unknown host and the caller's own
// cancellation or deadline stay final.
func TestTransientTransportErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unexpected EOF", fmt.Errorf("openai stream: %w", io.ErrUnexpectedEOF), true},
		{"connection reset", fmt.Errorf("openai stream: %w",
			&net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}), true},
		{"connection aborted", fmt.Errorf("openai stream: %w",
			&net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNABORTED)}), true},
		// Windows reports a dead connection with Winsock's own codes, which
		// neither match syscall.ECONNRESET nor say "connection reset" (issue
		// #389): WSAECONNRESET when the remote host closed it, WSAECONNABORTED
		// when the local stack gave up on it.
		{"Windows connection reset", fmt.Errorf("openai stream: %w", windowsSocketError(10054)), true},
		{"Windows connection aborted", fmt.Errorf("openai stream: %w", windowsSocketError(10053)), true},
		{"Windows connection reset flattened to its text", errors.New(
			"openai stream: read tcp 127.0.0.1:53107->127.0.0.1:62557: wsarecv: An existing connection was forcibly closed by the remote host."), true},
		// The decoder's end-of-input diagnostic is classified at the provider
		// boundary (streamDecodeTruncation), never here: a needle for it would
		// also match a request whose own JSON is wrong.
		{"decoder end of input, flattened to its text", errors.New("codex stream: unexpected end of JSON input"), false},
		{"decoder end of input, typed", fmt.Errorf("codex stream: %w", jsonCutError()), false},
		{"Windows connection aborted flattened to its text", errors.New(
			"openai stream: write tcp 127.0.0.1:53107->127.0.0.1:62557: wsasend: An established connection was aborted by the software in your host machine."), true},
		{"another Winsock failure", fmt.Errorf("openai stream: %w", windowsSocketError(10013)), false},
		// Once text reached the caller the wrapper must not replay the call;
		// the agent loop recovers it instead (IsTransientProviderError).
		{"Windows connection reset after text", fmt.Errorf("openai stream: %w",
			&streamTransportError{cause: windowsSocketError(10054), emitted: true}), false},
		// What net/http actually prints for an RST_STREAM from the peer: no
		// "http2:" prefix (that spelling belongs to GOAWAY and the lost-ping
		// close), and wrapped in the url.Error of the request it killed.
		{"http2 stream reset by the peer", fmt.Errorf("openai complete: %w",
			&url.Error{Op: "Post", URL: "https://api.example.test/v1/chat/completions", Err: errors.New("stream error: stream ID 1; INTERNAL_ERROR; received from peer")}), true},
		{"http2 stream refused", errors.New(`openai stream: POST "https://api.example.test": stream error: stream ID 3; REFUSED_STREAM`), true},
		{"http2 lost ping", fmt.Errorf("openai stream: %w",
			&url.Error{Op: "Post", URL: "https://api.example.test/v1/chat/completions", Err: errors.New("http2: client connection lost")}), true},
		{"stalled stream before any delta", fmt.Errorf("openai stream: %w",
			&streamTransportError{cause: &streamStalledError{idle: time.Second}}), true},
		{"plain failure", errors.New("openai stream: boom"), false},
		// The request never left the client: the handshake or the dial failed.
		{"TLS handshake timeout", fmt.Errorf("openai stream: %w",
			&url.Error{Op: "Post", URL: "https://api.example.test/v1/chat/completions", Err: errors.New("net/http: TLS handshake timeout")}), true},
		{"dial timeout", fmt.Errorf("openai stream: %w",
			&url.Error{Op: "Post", URL: "https://api.example.test/v1/chat/completions", Err: realDialTimeout(t)}), true},
		{"dial host unreachable", fmt.Errorf("openai stream: %w",
			&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}), true},
		{"dial temporary DNS failure", fmt.Errorf("openai stream: %w",
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "server misbehaving", Name: "api.example.test", IsTemporary: true}}), true},
		{"dial unknown host", fmt.Errorf("openai stream: %w",
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "api.example.test", IsNotFound: true}}), false},
		{"dial canceled by the caller", fmt.Errorf("openai stream: %w",
			&net.OpError{Op: "dial", Net: "tcp", Err: context.Canceled}), false},
		{"caller deadline", fmt.Errorf("openai stream: %w", context.DeadlineExceeded), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableLLMError(tc.err); got != tc.want {
				t.Fatalf("retryable = %v, want %v for %v", got, tc.want, tc.err)
			}
		})
	}
}

// windowsSocketError is a read on an established TCP stream failing the way
// Go's net package reports it on Windows: the Winsock code as a bare
// syscall.Errno inside the wsarecv syscall error. Only the windows build of
// syscall names these codes (WSAECONNRESET is 10054), so the value is what
// the test can build everywhere.
func windowsSocketError(code int) error {
	return &net.OpError{
		Op:     "read",
		Net:    "tcp",
		Source: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53107},
		Addr:   &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 62557},
		Err:    os.NewSyscallError("wsarecv", syscall.Errno(code)),
	}
}

// realDialTimeout returns the error net.Dialer produces when its timeout
// fires: an "i/o timeout" that also matches context.DeadlineExceeded. A
// deadline already in the past fails before any packet is sent.
func realDialTimeout(t *testing.T) error {
	t.Helper()
	_, err := (&net.Dialer{Timeout: time.Nanosecond}).Dial("tcp", "192.0.2.1:443")
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial error = %#v, want a dial timeout", err)
	}
	return err
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
	stalled := fmt.Errorf("openai stream: %w", &streamTransportError{cause: &streamStalledError{idle: time.Second}, emitted: true})
	if isRetryableLLMError(stalled) {
		t.Fatal("a stream that stalled after emitted deltas must not be retryable")
	}
	if !IsStreamStalled(stalled) {
		t.Fatal("the stall must stay recognisable through the transport wrapper and the provider prefix")
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

// The call's own bound (the caller's timer) counts this call's time only:
// a turn that spent its ledger elsewhere still retries a short pause under
// it, while a pause the timer would cut fails fast.
func TestResilientProviderCallBudgetIsPerCall(t *testing.T) {
	var calls atomic.Int32
	cause := retryHTTPError(t, "openai", 429, map[string]string{"Retry-After-Ms": "100"})
	inner := &stubProvider{
		streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			if calls.Add(1) == 1 {
				return nil, cause
			}
			return &Response{Content: "done", StopReason: "end_turn"}, nil
		},
	}
	ledger := &testLimitLedger{spent: time.Hour}
	p := wrapResilient(inner, ResilientOptions{
		RetryMax:      3,
		RetryBase:     5 * time.Millisecond,
		RetryMaxDelay: 100 * time.Millisecond,
		CallBudget:    time.Second,
		Ledger:        ledger,
	})
	if _, err := p.Stream(context.Background(), nil, nil, nil); err != nil || calls.Load() != 2 {
		t.Fatalf("a short pause under the call's bound is retried whatever the ledger holds: calls=%d err=%v", calls.Load(), err)
	}
	calls.Store(0)
	tight := wrapResilient(inner, ResilientOptions{RetryMax: 3, RetryBase: 5 * time.Millisecond, RetryMaxDelay: 100 * time.Millisecond, CallBudget: 120 * time.Millisecond})
	_, err := tight.Stream(context.Background(), nil, nil, nil)
	var reset *QuotaResetError
	if !errors.As(err, &reset) || calls.Load() != 1 {
		t.Fatalf("a pause the caller's timer would cut fails fast: calls=%d err=%v", calls.Load(), err)
	}
	// An unnamed 429 under the call's bound alone: its backoff runs while
	// the bound allows and the call otherwise ends with the provider's own
	// error, never a reset and never a sleep into the caller's timer.
	unnamed := retryHTTPError(t, "openai", 429, nil)
	inner.streamFn = func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
		if calls.Add(1) == 1 {
			return nil, unnamed
		}
		return &Response{Content: "done", StopReason: "end_turn"}, nil
	}
	calls.Store(0)
	roomy := wrapResilient(inner, ResilientOptions{RetryMax: 3, RetryBase: 100 * time.Millisecond, RetryMaxDelay: 100 * time.Millisecond, CallBudget: time.Second})
	if _, err := roomy.Stream(context.Background(), nil, nil, nil); err != nil || calls.Load() != 2 {
		t.Fatalf("a backoff under the call's bound is taken: calls=%d err=%v", calls.Load(), err)
	}
	calls.Store(0)
	cut := wrapResilient(inner, ResilientOptions{RetryMax: 3, RetryBase: 100 * time.Millisecond, RetryMaxDelay: 100 * time.Millisecond, CallBudget: 120 * time.Millisecond})
	_, err = cut.Stream(context.Background(), nil, nil, nil)
	if errors.As(err, &reset) || !errors.Is(err, unnamed) || calls.Load() != 1 {
		t.Fatalf("a backoff past the call's bound ends with the provider's error at once: calls=%d err=%v", calls.Load(), err)
	}
}

// TestUpstreamStatus: the status a provider answered with is read from the
// typed errors only, so a digit inside a message never passes for one.
func TestUpstreamStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"openai 400", retryHTTPError(t, "openai", 400, nil), 400},
		{"anthropic 529", retryHTTPError(t, "anthropic", 529, nil), 529},
		{"devin 400", fmt.Errorf("devin chat: %w", &devinAPIError{status: 400, code: "invalid_argument"}), 400},
		{"stream error after text", fmt.Errorf("openai stream: %w", &streamServerError{code: 500, emitted: true}), 500},
		{"quota reset", &QuotaResetError{Cause: retryHTTPError(t, "openai", 429, nil)}, 429},
		{"plain text", errors.New("dial failed after 500 ms"), 0},
		{"nil", nil, 0},
	}
	for _, c := range cases {
		if got := UpstreamStatus(c.err); got != c.want {
			t.Errorf("%s: UpstreamStatus = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestUpstreamRetryAfter(t *testing.T) {
	d, ok := UpstreamRetryAfter(retryHTTPError(t, "openai", 429, map[string]string{"Retry-After": "7"}))
	if !ok || d != 7*time.Second {
		t.Fatalf("UpstreamRetryAfter = %v, %v; want 7s", d, ok)
	}
	reset := &QuotaResetError{Delay: 90 * time.Second, Cause: errors.New("limit")}
	if d, ok := UpstreamRetryAfter(reset); !ok || d != 90*time.Second {
		t.Fatalf("UpstreamRetryAfter(quota reset) = %v, %v; want 90s", d, ok)
	}
	if _, ok := UpstreamRetryAfter(retryHTTPError(t, "openai", 400, nil)); ok {
		t.Fatal("a 400 without a hint named a pause")
	}
}

// TestIsTransientProviderError: what the agent may run again after a pause -
// a lane failure, whether or not text had already streamed - and what it
// must not: a request the provider refused, a cancel, a limit with a reset.
func TestIsTransientProviderError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"server error after text", fmt.Errorf("openai stream: %w", &streamServerError{code: 500, msg: "litellm.MidStreamFallbackError", emitted: true}), true},
		{"server error before text", fmt.Errorf("openai stream: %w", &streamServerError{code: 502}), true},
		{"truncated after text", fmt.Errorf("openai stream: %w", &streamTruncatedError{emitted: true}), true},
		{"truncated inside a JSON event after text", fmt.Errorf("codex stream: %w", &streamTruncatedError{emitted: true, cause: jsonCutError()}), true},
		{"truncated inside a JSON event before text", fmt.Errorf("codex stream: %w", &streamTruncatedError{cause: jsonCutError()}), true},
		// Records that no global JSON retry was added: the same decoder error
		// outside the truncation type is not transient.
		{"JSON syntax error in a transport wrapper", fmt.Errorf("codex stream: %w", &streamTransportError{cause: jsonCutError(), emitted: true}), false},
		{"undecodable frame with a status in its text", fmt.Errorf("openai stream: %w", &streamUndecodableError{snippet: "<html><title>502 Bad Gateway</title></html>"}), false},
		{"connection reset after text", fmt.Errorf("openai stream: %w", &streamTransportError{cause: syscall.ECONNRESET, emitted: true}), true},
		{"Windows connection reset after text", fmt.Errorf("openai stream: %w", &streamTransportError{cause: windowsSocketError(10054), emitted: true}), true},
		{"Windows connection aborted before text", fmt.Errorf("openai stream: %w", &streamTransportError{cause: windowsSocketError(10053)}), true},
		{"stalled", &streamStalledError{idle: time.Minute}, true},
		{"upstream 503", retryHTTPError(t, "openai", 503, nil), true},
		{"upstream 429", retryHTTPError(t, "openai", 429, nil), false},
		{"limit in the stream", fmt.Errorf("openai stream: %w", &streamServerError{code: 429, emitted: true}), false},
		{"bad request", retryHTTPError(t, "openai", 400, nil), false},
		{"refusal after text", fmt.Errorf("openai stream: %w", &streamServerError{code: 400, emitted: true}), false},
		{"codex failure after text", fmt.Errorf("codex stream: %w", &streamServerError{msg: "content policy", emitted: true}), false},
		{"transport error that is not transient", fmt.Errorf("openai stream: %w", &streamTransportError{cause: errors.New("x509: certificate signed by unknown authority"), emitted: true}), false},
		{"cancelled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"quota reset", &QuotaResetError{Cause: retryHTTPError(t, "openai", 429, nil)}, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := IsTransientProviderError(c.err); got != c.want {
			t.Errorf("%s: IsTransientProviderError = %v, want %v", c.name, got, c.want)
		}
	}
}

// recordCalls attaches a call observer for the rest of the test and returns
// what it has seen of the provider row called name; the observer is process
// wide, so other tests' rows are not its business.
func recordCalls(t *testing.T, name string) func() []CallEvent {
	t.Helper()
	var mu sync.Mutex
	var got []CallEvent
	remove := AddCallObserver(func(ev CallEvent) {
		if ev.Provider != name {
			return
		}
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	t.Cleanup(remove)
	return func() []CallEvent {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

// The tokens every stand-in backend of TestCallObserverOneEventPerAttempt
// reports for its finished answer, as the Response of the call holds them.
const (
	standInputTokens  = 11
	standOutputTokens = 4
	standCachedTokens = 3
)

// callStand is a stand-in backend for one provider type: its first failures
// requests are answered with a 503 (an unavailable trailer for Devin), every
// later one with a finished answer that reports the stand* tokens.
type callStand struct {
	input    ProviderInput
	requests func() int
}

func newCallStand(t *testing.T, kind string, failures int32) callStand {
	t.Helper()
	var left atomic.Int32
	left.Store(failures)
	failNext := func() bool { return left.Add(-1) >= 0 }
	in := ProviderInput{
		Name: "call-" + kind, Type: kind, Model: "test-model", APIKey: "test-key",
		RetryMax: 2, RetryBase: time.Millisecond, RetryMaxDelay: time.Millisecond,
	}
	if kind == "devin" {
		stand := devinfake.New(devinfake.Options{
			Models: []devinfake.Model{
				{UID: "fam-low", Family: "fam", FamilyLabel: "Fam", ContextWindow: 100000, MaxOutput: 8000},
				{UID: "fam-high", Family: "fam", FamilyLabel: "Fam", Default: true, ContextWindow: 100000, MaxOutput: 8000},
			},
			Reply: func(devinfake.ChatRequest, int) devinfake.Turn {
				if failNext() {
					return devinfake.Turn{ErrorCode: "unavailable", ErrorMessage: "overloaded"}
				}
				return devinfake.Turn{Text: "hi", StopReason: 2,
					Input: standInputTokens - standCachedTokens, Output: standOutputTokens, CacheRead: standCachedTokens}
			},
		})
		srv := httptest.NewServer(stand)
		t.Cleanup(srv.Close)
		devinTestEnv(t, srv.URL)
		in.Model, in.ReasoningEffort, in.APIKey = "fam", "low", "devin-session-token$stand-token"
		return callStand{input: in, requests: func() int { return len(stand.Chats()) }}
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests.Add(1)
		if failNext() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"overloaded","type":"server_error"}}`)
			return
		}
		writeStandAnswer(t, w, kind, gjson.GetBytes(body, "stream").Bool())
	}))
	t.Cleanup(srv.Close)
	switch kind {
	case "neuraldeep":
		t.Setenv(EnvNeuralDeepBaseURL, srv.URL)
	case "codex":
		t.Setenv(EnvCodexBaseURL, srv.URL)
		in.APIKey = ""
		in.AuthPath = writeCodexAuth(t, t.TempDir(), codexAuthFile{
			AuthMode: codexAuthModeChatGPT,
			Tokens:   codexTokens{AccessToken: makeJWT(time.Now().Add(time.Hour)), RefreshToken: "rt", AccountID: "acct-1"},
		})
		in.NoCLILogin = true
	default:
		in.BaseURL = srv.URL
	}
	return callStand{input: in, requests: func() int { return int(requests.Load()) }}
}

// writeStandAnswer writes the finished answer of a provider type in the shape
// the request asked for: a stream, or one blocking body.
func writeStandAnswer(t *testing.T, w http.ResponseWriter, kind string, stream bool) {
	t.Helper()
	switch kind {
	case "codex":
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w, "response.output_text.delta", map[string]any{"delta": "hi"})
		sse(w, "response.completed", map[string]any{"response": map[string]any{
			"status": "completed",
			"usage": map[string]any{
				"input_tokens": standInputTokens, "output_tokens": standOutputTokens,
				"input_tokens_details": map[string]any{"cached_tokens": standCachedTokens},
			},
		}})
	case "anthropic":
		// The prompt is 8 uncached tokens plus 3 read from the cache.
		start := `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"test-model","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":8,"cache_read_input_tokens":3,"output_tokens":0}}}`
		if !stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"test-model","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":8,"cache_read_input_tokens":3,"output_tokens":4}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		events := append([]string{start}, anthropicTextBlock(0, "hi")...)
		events = append(events,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":4}}`,
			`{"type":"message_stop"}`)
		_, _ = io.WriteString(w, anthropicSSE(t, events...))
	default:
		usage := `"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}}`
		if !stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],`+usage+`}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n"+
			"data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\",\"delta\":{}}]}\n\n"+
			"data: {\"choices\":[],"+usage+"}\n\n"+
			"data: [DONE]\n\n")
	}
}

// TestCallObserverOneEventPerAttempt: the observer hears one event for every
// invocation attempt of a provider, retries included, whatever the type of
// the provider and the transport it was asked to use.
func TestCallObserverOneEventPerAttempt(t *testing.T) {
	rows := []struct {
		name          string
		failures      int32
		disableStream bool
		retryDisabled bool
		want          []string
	}{
		{"an answer on the first try", 0, false, false, []string{"ok"}},
		{"a retried 5xx", 1, false, false, []string{"server_error", "ok"}},
		{"a retried 5xx on the blocking transport", 1, true, false, []string{"server_error", "ok"}},
		{"retries disabled", 3, false, true, []string{"server_error"}},
	}
	for _, kind := range []string{"openai", "anthropic", "neuraldeep", "codex", "devin"} {
		for _, row := range rows {
			t.Run(kind+"/"+row.name, func(t *testing.T) {
				stand := newCallStand(t, kind, row.failures)
				stand.input.DisableStream = row.disableStream
				stand.input.RetryDisabled = row.retryDisabled
				calls := recordCalls(t, stand.input.Name)
				p, err := NewProvider(stand.input)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
				if (err == nil) != (row.want[len(row.want)-1] == "ok") {
					t.Fatalf("Stream error = %v, want outcomes %v", err, row.want)
				}
				events := calls()
				var outcomes []string
				for _, ev := range events {
					outcomes = append(outcomes, ev.Outcome)
					if ev.Provider != stand.input.Name || ev.Model != stand.input.Model {
						t.Errorf("event names %q/%q, want %q/%q", ev.Provider, ev.Model, stand.input.Name, stand.input.Model)
					}
				}
				if !slices.Equal(outcomes, row.want) {
					t.Fatalf("outcomes = %v, want %v", outcomes, row.want)
				}
				if got := stand.requests(); got != len(row.want) {
					t.Fatalf("the backend saw %d requests for %d events", got, len(row.want))
				}
				for i, ev := range events {
					if ev.Outcome != "ok" {
						if ev.InputTokens != 0 || ev.OutputTokens != 0 || ev.CachedInputTokens != 0 {
							t.Errorf("event %d (%s) carries tokens without a response: %+v", i, ev.Outcome, ev)
						}
						continue
					}
					if ev.InputTokens != standInputTokens || ev.OutputTokens != standOutputTokens || ev.CachedInputTokens != standCachedTokens {
						t.Errorf("event %d tokens = %d/%d/%d, want %d/%d/%d", i, ev.InputTokens, ev.OutputTokens, ev.CachedInputTokens,
							standInputTokens, standOutputTokens, standCachedTokens)
					}
					if resp == nil || ev.InputTokens != resp.InputTokens || ev.OutputTokens != resp.OutputTokens || ev.CachedInputTokens != resp.CachedInputTokens {
						t.Errorf("event %d tokens differ from the response: %+v vs %+v", i, ev, resp)
					}
				}
			})
		}
	}

	opts := ResilientOptions{Name: "call-stub", Model: "m", RetryMax: 2, RetryBase: time.Millisecond}
	t.Run("a stream cut by the caller reports what it delivered", func(t *testing.T) {
		calls := recordCalls(t, opts.Name)
		partial := &Response{Content: "par", InputTokens: 7, OutputTokens: 2, CachedInputTokens: 1}
		p := wrapResilient(&stubProvider{streamFn: func(context.Context, []Message, []ToolDefinition, func(StreamChunk)) (*Response, error) {
			return partial, context.Canceled
		}}, opts)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if _, err := p.Stream(ctx, nil, nil, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		events := calls()
		if len(events) != 1 || events[0].Outcome != "canceled" || events[0].InputTokens != 7 || events[0].OutputTokens != 2 || events[0].CachedInputTokens != 1 {
			t.Fatalf("events = %+v", events)
		}
	})
	t.Run("the duration spans the whole stream", func(t *testing.T) {
		calls := recordCalls(t, opts.Name)
		p := wrapResilient(&stubProvider{streamFn: func(_ context.Context, _ []Message, _ []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
			onChunk(StreamChunk{TextDelta: "a"})
			time.Sleep(40 * time.Millisecond)
			onChunk(StreamChunk{TextDelta: "b"})
			return &Response{Content: "ab"}, nil
		}}, opts)
		if _, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {}); err != nil {
			t.Fatal(err)
		}
		if events := calls(); len(events) != 1 || events[0].Duration < 40*time.Millisecond {
			t.Fatalf("events = %+v, want one that lasted the whole stream", events)
		}
	})
	t.Run("a retried attempt says it is a retry", func(t *testing.T) {
		calls := recordCalls(t, opts.Name)
		var n atomic.Int32
		p := wrapResilient(&stubProvider{completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) {
			if n.Add(1) == 1 {
				return nil, errors.New("503 Service Unavailable")
			}
			return &Response{}, nil
		}}, opts)
		if _, err := p.Complete(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
		events := calls()
		if len(events) != 2 || events[0].Retry || !events[1].Retry {
			t.Fatalf("events = %+v, want a first attempt and then a retry", events)
		}
	})
	t.Run("the first token is the first chunk that carries content", func(t *testing.T) {
		calls := recordCalls(t, opts.Name)
		p := wrapResilient(&stubProvider{streamFn: func(_ context.Context, _ []Message, _ []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
			onChunk(StreamChunk{InputTokens: 3})
			time.Sleep(30 * time.Millisecond)
			onChunk(StreamChunk{ReasoningDelta: "hm"})
			time.Sleep(30 * time.Millisecond)
			onChunk(StreamChunk{TextDelta: "a"})
			return &Response{Content: "a"}, nil
		}}, opts)
		if _, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {}); err != nil {
			t.Fatal(err)
		}
		events := calls()
		if len(events) != 1 || events[0].FirstContent < 30*time.Millisecond || events[0].FirstContent >= events[0].Duration-20*time.Millisecond {
			t.Fatalf("events = %+v, want the first content after the usage chunk and before the text", events)
		}
	})
	t.Run("an attempt with no content has no first token", func(t *testing.T) {
		calls := recordCalls(t, opts.Name)
		p := wrapResilient(&stubProvider{
			streamFn: func(_ context.Context, _ []Message, _ []ToolDefinition, onChunk func(StreamChunk)) (*Response, error) {
				// As the adapters do: nothing is streamed to a caller that passed no callback.
				if onChunk != nil {
					onChunk(StreamChunk{StopReason: "stop", OutputTokens: 1})
				}
				return &Response{}, nil
			},
			completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) {
				return &Response{Content: "x"}, nil
			},
		}, opts)
		if _, err := p.Stream(context.Background(), nil, nil, func(StreamChunk) {}); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Stream(context.Background(), nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Complete(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
		events := calls()
		if len(events) != 3 {
			t.Fatalf("events = %+v, want three", events)
		}
		for _, ev := range events {
			if ev.FirstContent != 0 {
				t.Fatalf("event %+v has a first token", ev)
			}
		}
	})
	t.Run("a call that never reaches the provider is not an attempt", func(t *testing.T) {
		calls := recordCalls(t, opts.Name)
		var reached atomic.Int32
		inner := &stubProvider{completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) {
			reached.Add(1)
			return &Response{}, nil
		}}
		paced := opts
		paced.MinInterval = time.Hour
		p := wrapResilient(inner, paced)
		if _, err := p.Complete(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Complete(ctx, nil, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("a call made after its context ended: %v", err)
		}
		waiting, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer stop()
		if _, err := p.Complete(waiting, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a call cut off in the minimum interval wait: %v", err)
		}
		if events := calls(); len(events) != 1 || reached.Load() != 1 {
			t.Fatalf("events = %+v after %d real attempts, want one of each", events, reached.Load())
		}
	})
}

// TestCallObserverRegistry: every registered observer is told, one that was
// removed is not, and removing twice or registering nothing is harmless.
func TestCallObserverRegistry(t *testing.T) {
	var first, second atomic.Int32
	removeFirst := AddCallObserver(func(CallEvent) { first.Add(1) })
	removeSecond := AddCallObserver(func(CallEvent) { second.Add(1) })
	AddCallObserver(nil)()
	defer removeSecond()
	p := wrapResilient(&stubProvider{completeFn: func(context.Context, []Message, []ToolDefinition) (*Response, error) {
		return &Response{}, nil
	}}, ResilientOptions{Name: "call-registry"})
	call := func() {
		t.Helper()
		if _, err := p.Complete(context.Background(), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	call()
	removeFirst()
	removeFirst()
	call()
	if first.Load() != 1 || second.Load() != 2 {
		t.Fatalf("first heard %d events, second %d; want 1 and 2", first.Load(), second.Load())
	}
}

func TestCallOutcome(t *testing.T) {
	overflow := fmt.Errorf("openai complete: %w", &openai.Error{StatusCode: 400, Code: "context_length_exceeded",
		Request:  httptest.NewRequest(http.MethodPost, "https://api.example.test/v1/chat/completions", nil),
		Response: &http.Response{StatusCode: 400}})
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no error", nil, "ok"},
		{"a cancel", context.Canceled, "canceled"},
		{"a cancel inside a stream", fmt.Errorf("openai stream: %w", &streamTransportError{cause: context.Canceled, emitted: true}), "canceled"},
		{"a deadline", fmt.Errorf("openai stream: %w", context.DeadlineExceeded), "timeout"},
		{"a dial that ran out of time", &net.OpError{Op: "dial", Err: context.DeadlineExceeded}, "timeout"},
		{"a stall", fmt.Errorf("openai stream: %w", &streamTransportError{cause: &streamStalledError{idle: time.Minute}}), "timeout"},
		{"openai 429", retryHTTPError(t, "openai", 429, nil), "rate_limited"},
		{"anthropic 429", retryHTTPError(t, "anthropic", 429, nil), "rate_limited"},
		{"a limit inside a stream", fmt.Errorf("openai stream: %w", &streamServerError{code: 429, emitted: true}), "rate_limited"},
		{"devin resource exhausted", fmt.Errorf("devin chat: %w", &devinAPIError{status: 429, code: "resource_exhausted"}), "rate_limited"},
		{"a status flattened into text", errors.New("codex stream: 429 Too Many Requests"), "rate_limited"},
		{"openai 401", retryHTTPError(t, "openai", 401, nil), "auth"},
		{"anthropic 403", retryHTTPError(t, "anthropic", 403, nil), "auth"},
		{"devin unauthenticated", fmt.Errorf("devin chat: %w", &devinAPIError{status: 401, code: "unauthenticated"}), "auth"},
		{"a context window that is too small, as a 400", overflow, "context_overflow"},
		{"an overflow phrase that holds a 5xx in its digits", errors.New("prompt is too long: 4500 tokens"), "context_overflow"},
		{"openai 400", retryHTTPError(t, "openai", 400, nil), "client_error"},
		{"anthropic 404", retryHTTPError(t, "anthropic", 404, nil), "client_error"},
		{"devin invalid argument", fmt.Errorf("devin chat: %w", &devinAPIError{status: 400, code: "invalid_argument"}), "client_error"},
		{"a refusal inside a stream", fmt.Errorf("openai stream: %w", &streamServerError{code: 400, emitted: true}), "client_error"},
		{"openai 503", retryHTTPError(t, "openai", 503, nil), "server_error"},
		{"anthropic 529", retryHTTPError(t, "anthropic", 529, nil), "server_error"},
		{"devin unavailable", fmt.Errorf("devin chat: %w", &devinAPIError{status: 503, code: "unavailable"}), "server_error"},
		{"a failure inside a stream after text", fmt.Errorf("openai stream: %w", &streamServerError{code: 500, emitted: true}), "server_error"},
		{"a status flattened into text, 5xx", errors.New("codex stream: 503 Service Unavailable"), "server_error"},
		{"a connection reset", fmt.Errorf("openai stream: %w", &streamTransportError{cause: syscall.ECONNRESET}), "server_error"},
		{"a connection nobody answers", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "server_error"},
		{"a truncated stream", fmt.Errorf("openai stream: %w", &streamTruncatedError{emitted: true}), "server_error"},
		{"a corrupt stream", fmt.Errorf("openai stream: %w", &streamUndecodableError{snippet: "<html>"}), "error"},
		{"a failure without a status", fmt.Errorf("codex stream: %w", &streamServerError{msg: "response.failed"}), "error"},
		{"a transport error that is not a drop", fmt.Errorf("openai stream: %w", &streamTransportError{cause: errors.New("x509: certificate signed by unknown authority")}), "error"},
		{"an error of Coddy's own", errors.New("boom"), "error"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CallOutcome(c.err); got != c.want {
				t.Fatalf("CallOutcome = %q, want %q", got, c.want)
			}
		})
		seen[c.want] = true
	}
	all := CallOutcomes()
	want := []string{"ok", "canceled", "timeout", "rate_limited", "auth", "context_overflow", "client_error", "server_error", "error"}
	if !slices.Equal(all, want) {
		t.Fatalf("CallOutcomes = %v, want %v", all, want)
	}
	all[0] = "changed"
	if CallOutcomes()[0] != "ok" {
		t.Fatal("CallOutcomes hands out its own slice")
	}
	for _, outcome := range want {
		if !seen[outcome] {
			t.Errorf("no case yields %q", outcome)
		}
	}
}

// TestCallEventHoldsNoRequestReference: an event is a plain value. A
// field that could hold the request, a message, a tool or the response would
// keep a prompt alive in an observer, and this is the check that holds for
// every provider, Devin's regenerated identifiers included.
func TestCallEventHoldsNoRequestReference(t *testing.T) {
	typ := reflect.TypeFor[CallEvent]()
	for i := range typ.NumField() {
		field := typ.Field(i)
		switch {
		case field.Type.Kind() == reflect.String, field.Type.Kind() == reflect.Int, field.Type.Kind() == reflect.Bool,
			field.Type == reflect.TypeFor[time.Duration]():
		default:
			t.Errorf("CallEvent.%s is a %s; only strings, ints, bools and a time.Duration may be fields", field.Name, field.Type)
		}
	}
}
