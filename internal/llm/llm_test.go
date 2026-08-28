package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

func TestWrappedStreamCancelIsCanceled(t *testing.T) {
	inner := context.Canceled
	wrapped := fmt.Errorf("openai stream: %w", inner)
	if !errors.Is(wrapped, context.Canceled) {
		t.Fatal("agent must detect cancel when provider wraps stream.Err with fmt.Errorf")
	}
}

// TestOpenAIMultimodalMessageContentParts verifies that a user Message with
// ImageParts is serialised as an array of content parts (text + image_url)
// rather than a plain string.
func TestOpenAIMultimodalMessageContentParts(t *testing.T) {
	p := newOpenAIProvider("gpt-4o", "key", "", nil, 1024, 0.0, "")
	msgs := []Message{
		{Role: RoleUser, Content: "describe this", ImageParts: []ImagePart{
			{DataURL: "data:image/png;base64,abc123", Name: "test.png"},
		}},
	}
	params := p.buildParams(msgs, nil, true)
	raw, err := json.Marshal(params.Messages)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"image_url"`) {
		t.Errorf("expected image_url content part, got: %s", s)
	}
	if !strings.Contains(s, `data:image/png;base64,abc123`) {
		t.Errorf("expected base64 data URL, got: %s", s)
	}
	if !strings.Contains(s, `"describe this"`) {
		t.Errorf("expected text content, got: %s", s)
	}
}

// TestNewProviderAnthropicHonorsBaseURL verifies that an Anthropic provider built
// through NewProvider routes requests to the configured api_base (BaseURL) instead of
// the hard-coded https://api.anthropic.com default. Regression test: BaseURL used to be
// dropped on the Anthropic branch, so OpenAI-compatible api_base overrides were ignored.
func TestNewProviderAnthropicHonorsBaseURL(t *testing.T) {
	var mu sync.Mutex
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hit = true
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-test",`+
			`"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()

	prov, err := NewProvider(ProviderInput{
		Type:    "anthropic",
		Model:   "claude-test",
		APIKey:  "test-key",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := prov.Complete(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !hit {
		t.Fatal("Anthropic provider ignored api_base: request did not reach the configured BaseURL server")
	}
	if resp.Content != "ok" {
		t.Errorf("unexpected content %q, want %q", resp.Content, "ok")
	}
}

func TestProviderBaseURLNeuralDeepIsFixed(t *testing.T) {
	if got := providerBaseURL("neuraldeep", "https://example.invalid/v1"); got != neuralDeepBaseURL {
		t.Fatalf("providerBaseURL(neuraldeep) = %q, want %q", got, neuralDeepBaseURL)
	}
}

func TestNewProviderNeuralDeepIsSupported(t *testing.T) {
	if _, err := NewProvider(ProviderInput{
		Type:    "neuraldeep",
		Model:   "default",
		APIKey:  "nd-test-key",
		BaseURL: "https://example.invalid/v1",
	}); err != nil {
		t.Fatalf("NewProvider(neuraldeep): %v", err)
	}
}

// streamStubProvider builds an unwrapped openai provider against a server
// that replays the given SSE body for every request.
func streamStubProvider(t *testing.T, sse string) (*openAIProvider, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	return newOpenAIProvider("qwen3-1.7b", "", srv.URL, nil, 0, 0, ""), srv.Close
}

// TestOpenAIStreamUndecodableFrameFails verifies that a malformed non-empty
// data frame after valid chunks aborts the stream with the offending payload
// preserved: silently skipping it would return a truncated response as
// success.
func TestOpenAIStreamUndecodableFrameFails(t *testing.T) {
	p, done := streamStubProvider(t,
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n"+
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\n\n"+ // truncated JSON
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: [DONE]\n\n")
	defer done()

	_, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err == nil {
		t.Fatal("Stream must fail on a malformed non-empty data frame")
	}
	if !strings.Contains(err.Error(), "undecodable SSE frame") || !strings.Contains(err.Error(), `{"choices":[{"index":0,"delta":{"content":`) {
		t.Errorf("error %q must name the undecodable frame and carry its payload", err)
	}
}

// TestOpenAIStreamedErrorRetryClassification verifies that server errors
// reported inside the SSE stream retry like their pre-stream HTTP
// equivalents: 5xx retryable, 4xx not.
func TestOpenAIStreamedErrorRetryClassification(t *testing.T) {
	const contentChunk = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n"
	cases := []struct {
		name         string
		body         string
		wantRequests int32
		wantDeltas   int32
	}{
		{"streamed 400 is not retried",
			"error: {\"code\":400,\"message\":\"the request exceeds the available context size\",\"type\":\"invalid_request_error\"}\n\n", 1, 0},
		{"streamed 500 is retried once",
			"error: {\"code\":500,\"message\":\"slot unavailable\",\"type\":\"server_error\"}\n\n", 2, 0},
		{"streamed 500 after emitted deltas is not retried",
			contentChunk + "error: {\"code\":500,\"message\":\"slot unavailable\",\"type\":\"server_error\"}\n\n", 1, 1},
		{"streamed 500 after deltas with status-like message text is not retried",
			contentChunk + "error: {\"code\":500,\"message\":\"upstream said 500 Internal Server Error\",\"type\":\"server_error\"}\n\n", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests, deltas atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			prov, err := NewProvider(ProviderInput{
				Type:          "openai",
				Model:         "qwen3-1.7b",
				BaseURL:       srv.URL,
				RetryMax:      1,
				RetryBase:     time.Millisecond,
				RetryMaxDelay: time.Millisecond,
			})
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			_, err = prov.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
				if c.TextDelta != "" {
					deltas.Add(1)
				}
			})
			if err == nil {
				t.Fatal("Stream must fail")
			}
			if got := requests.Load(); got != tc.wantRequests {
				t.Errorf("upstream requests = %d, want %d", got, tc.wantRequests)
			}
			if got := deltas.Load(); got != tc.wantDeltas {
				t.Errorf("text deltas delivered = %d, want %d (retries must not replay deltas)", got, tc.wantDeltas)
			}
		})
	}
}

// TestSSEScannerFrameAssembly pins the lenient scanner's frame handling:
// SSE-spec behaviors (CRLF, multi-line data join, comments, leading BOM) and
// the llama.cpp dialect ("error:" field, unterminated final frame).
func TestSSEScannerFrameAssembly(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []sseFrame
	}{
		{"crlf line endings",
			"data: {\"a\":1}\r\n\r\ndata: [DONE]\r\n\r\n",
			[]sseFrame{{data: []byte("{\"a\":1}\n")}, {data: []byte("[DONE]\n")}}},
		{"multiple data lines join with newline",
			"data: line1\ndata: line2\n\n",
			[]sseFrame{{data: []byte("line1\nline2\n")}}},
		{"comment-only frames and blank runs are skipped",
			": ping\n\n\n\n: pong\n\ndata: x\n\n",
			[]sseFrame{{data: []byte("x\n")}}},
		{"unterminated final frame is dispatched",
			"data: {\"a\":1}\n\ndata: tail",
			[]sseFrame{{data: []byte("{\"a\":1}\n")}, {data: []byte("tail\n")}}},
		{"error field is preserved separately",
			"error: {\"code\":400}\n\n",
			[]sseFrame{{errData: []byte("{\"code\":400}\n")}}},
		{"leading BOM is stripped",
			"\xef\xbb\xbfdata: x\n\n",
			[]sseFrame{{data: []byte("x\n")}}},
		{"field without colon or value is harmless",
			"data\n\ndata: x\n\n",
			[]sseFrame{{data: []byte("\n")}, {data: []byte("x\n")}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := newSSEScanner(strings.NewReader(tc.input))
			var got []sseFrame
			for sc.Next() {
				f := sc.Frame()
				got = append(got, sseFrame{
					data:    append([]byte(nil), f.data...),
					errData: append([]byte(nil), f.errData...),
				})
			}
			if err := sc.Err(); err != nil {
				t.Fatalf("scanner error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("frames = %d, want %d (%q)", len(got), len(tc.want), got)
			}
			for i := range got {
				if string(got[i].data) != string(tc.want[i].data) || string(got[i].errData) != string(tc.want[i].errData) {
					t.Errorf("frame %d = {data:%q err:%q}, want {data:%q err:%q}",
						i, got[i].data, got[i].errData, tc.want[i].data, tc.want[i].errData)
				}
			}
		})
	}
}

// TestStreamErrorSnippetRuneBoundary verifies the diagnostic snippet never
// splits a multibyte rune at the truncation point.
func TestStreamErrorSnippetRuneBoundary(t *testing.T) {
	// One ASCII byte shifts the 2-byte runes so the truncation index lands on
	// a continuation byte and the boundary back-off is actually exercised.
	payload := "a" + strings.Repeat("я", streamErrorSnippetLimit)
	if utf8.RuneStart(payload[streamErrorSnippetLimit]) {
		t.Fatal("test setup: truncation index must fall inside a rune")
	}
	got := streamErrorSnippet([]byte(payload))
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("snippet must be truncated with ellipsis")
	}
	if !utf8.ValidString(strings.TrimSuffix(got, "...")) {
		t.Errorf("snippet split a multibyte rune")
	}
}

// TestOpenAIStreamAllFramesUndecodable verifies that a stream yielding no
// decodable chunk fails with the offending frame preserved in the error.
func TestOpenAIStreamAllFramesUndecodable(t *testing.T) {
	p, done := streamStubProvider(t, "data: {\"choices\":[{\"index\n\n"+"data: [DONE]\n\n")
	defer done()

	_, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err == nil {
		t.Fatal("Stream must fail when no frame decodes")
	}
	if !strings.Contains(err.Error(), `{"choices":[{"index`) {
		t.Errorf("error %q must include the undecodable frame payload", err)
	}
}

// TestOpenAIStreamStandardErrorObject verifies that a data frame carrying an
// {"error": ...} object (llama.cpp b9038+, gateways) surfaces the message.
func TestOpenAIStreamStandardErrorObject(t *testing.T) {
	p, done := streamStubProvider(t,
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"}}]}\n\n"+
			"data: {\"error\":{\"code\":500,\"message\":\"slot unavailable\",\"type\":\"server_error\"}}\n\n")
	defer done()

	_, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err == nil || !strings.Contains(err.Error(), "slot unavailable") {
		t.Fatalf("error = %v, want message containing %q", err, "slot unavailable")
	}
}

// TestOpenAIStreamCancelKeepsPartial pins the cancellation contract: a stream
// cancelled after emitting content returns the partial response together with
// a context.Canceled-wrapped error.
func TestOpenAIStreamCancelKeepsPartial(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if fl != nil {
			fl.Flush()
		}
		<-release
	}))
	defer srv.Close()
	defer close(release)

	p := newOpenAIProvider("qwen3-1.7b", "", srv.URL, nil, 0, 0, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel from inside onChunk so the first delta is observed deterministically
	// before the context is torn down.
	resp, err := p.Stream(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
		if c.TextDelta != "" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if resp == nil || resp.Content != "partial" {
		t.Fatalf("resp = %+v, want partial content preserved", resp)
	}
}

// TestOpenAIStreamTruncatedBeforeFirstDelta verifies that a stream cut
// before any delta fails with a truncation error and no response: there is
// nothing worth preserving and the call is safe to retry.
func TestOpenAIStreamTruncatedBeforeFirstDelta(t *testing.T) {
	p, done := streamStubProvider(t,
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null}}],\"id\":\"chatcmpl-t1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n")
	defer done()

	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) {
		t.Fatalf("err = %v, want stream truncation", err)
	}
	if resp != nil {
		t.Fatalf("resp = %+v, want nil (nothing was delivered)", resp)
	}
	if !isRetryableLLMError(err) {
		t.Fatal("truncation before any delta must classify as retryable")
	}
}

// TestOpenAIStreamTruncatedKeepsReasoningOnlyPartial ensures a cut stream
// preserves reasoning that was already sent to the caller, even if no answer
// text followed it. The ReAct loop needs a non-nil response to persist that
// visible reasoning next to the truncation error.
func TestOpenAIStreamTruncatedKeepsReasoningOnlyPartial(t *testing.T) {
	p, done := streamStubProvider(t,
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"reasoning_content\":\"Thinking through it\"}}],\"id\":\"chatcmpl-tr\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n")
	defer done()

	var streamed strings.Builder
	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
		streamed.WriteString(c.ReasoningDelta)
	})
	if !IsStreamTruncated(err) {
		t.Fatalf("err = %v, want stream truncation", err)
	}
	if isRetryableLLMError(err) {
		t.Fatal("truncation after emitted reasoning must not be retryable")
	}
	if got := streamed.String(); got != "Thinking through it" {
		t.Fatalf("streamed reasoning = %q, want %q", got, "Thinking through it")
	}
	if resp == nil || resp.Content != "" || resp.Reasoning != "Thinking through it" {
		t.Fatalf("resp = %+v, want reasoning-only partial response", resp)
	}
}

// TestOpenAIStreamTruncatedNotRetriedAfterDeltas pins the resilient-wrapper
// contract for truncations: once deltas reached the caller the request is
// not replayed (the same text would stream twice) and the partial response
// survives the wrapper.
func TestOpenAIStreamTruncatedNotRetriedAfterDeltas(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"Hel\"}}],\"id\":\"chatcmpl-t2\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n")
	}))
	defer srv.Close()

	prov, err := NewProvider(ProviderInput{
		Type:          "openai",
		Model:         "test-model",
		BaseURL:       srv.URL,
		RetryMax:      2,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	resp, err := prov.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) {
		t.Fatalf("err = %v, want stream truncation", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1 (no replay after emitted deltas)", got)
	}
	if resp == nil || resp.Content != "Hel" {
		t.Errorf("resp = %+v, want partial content %q preserved", resp, "Hel")
	}
}

// TestOpenAIStreamTruncatedRetriedWhenNothingEmitted verifies the opposite
// case: a truncation before any delta is a transient transport failure and
// gets the configured retry.
func TestOpenAIStreamTruncatedRetriedWhenNothingEmitted(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null}}],\"id\":\"chatcmpl-t3\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n")
	}))
	defer srv.Close()

	prov, err := NewProvider(ProviderInput{
		Type:          "openai",
		Model:         "test-model",
		BaseURL:       srv.URL,
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	_, err = prov.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) {
		t.Fatalf("err = %v, want stream truncation", err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("upstream requests = %d, want 2 (silent truncation deserves the retry)", got)
	}
}

// TestOpenAIStreamTruncatedDropsUnfinishedToolCalls pins the deliberate
// choice for tool calls cut mid-argument: their JSON may be invalid, so the
// truncation error carries no partial response instead of a broken call.
func TestOpenAIStreamTruncatedDropsUnfinishedToolCalls(t *testing.T) {
	p, done := streamStubProvider(t,
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]}}],\"id\":\"chatcmpl-t4\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n"+
			"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\\\"Par\"}}]}}],\"id\":\"chatcmpl-t4\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n")
	defer done()

	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) {
		t.Fatalf("err = %v, want stream truncation", err)
	}
	if resp != nil {
		t.Fatalf("resp = %+v, want nil (unfinished tool calls are dropped)", resp)
	}
}

// anthropicStreamStub serves a verbatim Anthropic SSE payload and returns an
// unwrapped provider pointed at it.
func anthropicStreamStub(t *testing.T, sse string) (*anthropicProvider, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	return newAnthropicProvider("test-model", "k", srv.URL, nil, 0, 0, ""), srv.Close
}

const anthropicStreamPrefix = "event: message_start\n" +
	"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"test-model\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\n" +
	"event: content_block_start\n" +
	"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Paris\"}}\n\n"

// TestAnthropicStreamTruncatedKeepsPartial mirrors the openai-path contract
// on the anthropic path: a stream that ends without a terminal message_delta
// fails with a truncation error while the delivered text is preserved.
func TestAnthropicStreamTruncatedKeepsPartial(t *testing.T) {
	p, done := anthropicStreamStub(t, anthropicStreamPrefix)
	defer done()

	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if !IsStreamTruncated(err) {
		t.Fatalf("err = %v, want stream truncation", err)
	}
	if isRetryableLLMError(err) {
		t.Error("truncation after emitted deltas must not be retryable")
	}
	if resp == nil || resp.Content != "Paris" {
		t.Fatalf("resp = %+v, want partial content %q preserved", resp, "Paris")
	}
}

// TestAnthropicStreamWithTerminalEventSucceeds guards the healthy anthropic
// path around the truncation check: a stream closed after message_delta and
// message_stop is a normal end_turn response.
func TestAnthropicStreamWithTerminalEventSucceeds(t *testing.T) {
	p, done := anthropicStreamStub(t, anthropicStreamPrefix+
		"event: content_block_stop\n"+
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
		"event: message_delta\n"+
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}\n\n"+
		"event: message_stop\n"+
		"data: {\"type\":\"message_stop\"}\n\n")
	defer done()

	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp == nil || resp.Content != "Paris" || resp.StopReason != "end_turn" {
		t.Fatalf("resp = %+v, want complete end_turn with %q", resp, "Paris")
	}
}

// TestProviderTimeoutBoundsRequest verifies that providers[].timeout_ms
// reaches the HTTP client: a hung upstream fails within the configured
// bound instead of waiting forever, and the timeout is not retried (the
// caller's budget is spent).
func TestProviderTimeoutBoundsRequest(t *testing.T) {
	release := make(chan struct{})
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		<-release
	}))
	// LIFO: release the parked handler first so srv.Close can finish.
	defer srv.Close()
	defer close(release)

	prov, err := NewProvider(ProviderInput{
		Type:          "openai",
		Model:         "test-model",
		BaseURL:       srv.URL,
		Timeout:       100 * time.Millisecond,
		RetryMax:      2,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	start := time.Now()
	_, err = prov.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("call took %v, want the 100ms client timeout to cut it", elapsed)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("upstream requests = %d, want 1 (client timeouts are not retried)", got)
	}
}

// TestOpenAITextOnlyMessageIsString verifies that a user Message without
// ImageParts still results in a plain string content field.
func TestOpenAITextOnlyMessageIsString(t *testing.T) {
	p := newOpenAIProvider("gpt-4o", "key", "", nil, 1024, 0.0, "")
	msgs := []Message{
		{Role: RoleUser, Content: "hello"},
	}
	params := p.buildParams(msgs, nil, true)
	raw, err := json.Marshal(params.Messages)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(raw)
	if strings.Contains(s, `"image_url"`) {
		t.Errorf("unexpected image_url in text-only message: %s", s)
	}
	if !strings.Contains(s, `"hello"`) {
		t.Errorf("expected text content, got: %s", s)
	}
}

// recordingProvider counts which half of the Provider interface a decorator uses
// and hands back a scripted response.
type recordingProvider struct {
	resp        *Response
	err         error
	completes   int
	streams     int
	completeErr []error
}

func (p *recordingProvider) Complete(_ context.Context, _ []Message, _ []ToolDefinition) (*Response, error) {
	p.completes++
	if len(p.completeErr) > 0 {
		err := p.completeErr[0]
		p.completeErr = p.completeErr[1:]
		if err != nil {
			return nil, err
		}
	}
	return p.resp, p.err
}

func (p *recordingProvider) Stream(_ context.Context, _ []Message, _ []ToolDefinition, _ func(StreamChunk)) (*Response, error) {
	p.streams++
	return p.resp, p.err
}

// TestBlockingProviderReplaysCompleteInOrder pins the replay contract: the inner
// Stream is never touched, and the finished response reaches the caller as one
// reasoning chunk, one text chunk, and one chunk per tool call, in that order.
func TestBlockingProviderReplaysCompleteInOrder(t *testing.T) {
	inner := &recordingProvider{resp: &Response{
		Content:      "answer",
		Reasoning:    "thinking",
		StopReason:   "tool_use",
		InputTokens:  7,
		OutputTokens: 3,
		ToolCalls: []ToolCall{
			{ID: "a", Name: "read", InputJSON: `{"path":"x"}`},
			{ID: "b", Name: "glob", InputJSON: `{"pattern":"*"}`},
		},
	}}
	p := newBlockingProvider(inner)

	var chunks []StreamChunk
	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil,
		func(c StreamChunk) { chunks = append(chunks, c) })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if inner.completes != 1 || inner.streams != 0 {
		t.Fatalf("inner calls: %d Complete, %d Stream; want exactly one Complete", inner.completes, inner.streams)
	}
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d, want reasoning + text + two tool calls: %+v", len(chunks), chunks)
	}
	if chunks[0].ReasoningDelta != "thinking" || chunks[1].TextDelta != "answer" {
		t.Fatalf("replay order wrong: %+v", chunks)
	}
	if chunks[2].ToolCall == nil || chunks[2].ToolCall.ID != "a" ||
		chunks[3].ToolCall == nil || chunks[3].ToolCall.ID != "b" {
		t.Fatalf("tool call chunks wrong: %+v", chunks)
	}
	// Stop reason and usage travel in the Response, never as a trailing chunk.
	for i, c := range chunks {
		if c.StopReason != "" || c.InputTokens != 0 || c.OutputTokens != 0 {
			t.Fatalf("chunk %d carries terminal metadata: %+v", i, c)
		}
	}
	if resp.StopReason != "tool_use" || resp.InputTokens != 7 || resp.OutputTokens != 3 {
		t.Fatalf("response = %+v, want the inner response unchanged", resp)
	}
}

// TestBlockingProviderDropsAnswerAfterCancel covers a backend that ignores
// cancellation: a response that lands after the user pressed Stop must not be
// replayed, or its tool calls would run for a turn that was already stopped.
func TestBlockingProviderDropsAnswerAfterCancel(t *testing.T) {
	inner := &recordingProvider{resp: &Response{Content: "late answer", ToolCalls: []ToolCall{{ID: "a", Name: "run_command"}}}}
	p := newBlockingProvider(inner)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var chunks []StreamChunk
	resp, err := p.Stream(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil,
		func(c StreamChunk) { chunks = append(chunks, c) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if resp != nil {
		t.Fatalf("resp = %+v, want nothing published for a cancelled turn", resp)
	}
	if len(chunks) != 0 {
		t.Fatalf("chunks = %+v, want none after cancellation", chunks)
	}
}

// TestBlockingProviderRetriesEmitOnce checks the wrapping order: the resilient
// provider sits outside the decorator, so a retried call re-issues a blocking
// request that has emitted nothing rather than replaying deltas twice.
func TestBlockingProviderRetriesEmitOnce(t *testing.T) {
	inner := &recordingProvider{
		resp:        &Response{Content: "second attempt"},
		completeErr: []error{errors.New("500 Internal Server Error")},
	}
	p := wrapResilient(newBlockingProvider(inner), ResilientOptions{
		RetryMax: 2, RetryBase: time.Millisecond, RetryMaxDelay: time.Millisecond,
	})

	var texts []string
	resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil,
		func(c StreamChunk) {
			if c.TextDelta != "" {
				texts = append(texts, c.TextDelta)
			}
		})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if inner.completes != 2 {
		t.Fatalf("inner Complete calls = %d, want the failure plus one retry", inner.completes)
	}
	if len(texts) != 1 || texts[0] != "second attempt" {
		t.Fatalf("emitted text = %q, want only the successful attempt", texts)
	}
	if resp.Content != "second attempt" {
		t.Fatalf("resp = %+v", resp)
	}
}

// TestNewProviderWrapsOnlyWhenStreamDisabled makes sure the default transport is
// untouched: without DisableStream the openai provider must still open a stream.
func TestNewProviderWrapsOnlyWhenStreamDisabled(t *testing.T) {
	var sawStream []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sawStream = append(sawStream, gjson.GetBytes(raw, "stream").Bool())
		if gjson.GetBytes(raw, "stream").Bool() {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\",\"delta\":{\"content\":\"streamed\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"blocking"}}]}`)
	}))
	defer srv.Close()

	for _, tc := range []struct {
		name     string
		disable  bool
		wantText string
	}{
		{"default streams", false, "streamed"},
		{"stream false blocks", true, "blocking"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewProvider(ProviderInput{Type: "openai", Model: "m", BaseURL: srv.URL, DisableStream: tc.disable})
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			resp, err := p.Stream(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil, func(StreamChunk) {})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			if resp.Content != tc.wantText {
				t.Fatalf("content = %q, want %q", resp.Content, tc.wantText)
			}
		})
	}
	if len(sawStream) != 2 || !sawStream[0] || sawStream[1] {
		t.Fatalf("wire stream flags = %v, want [true false]", sawStream)
	}
}

// TestOpenAICompleteReadsReasoningContent covers the non-streamed reasoning
// fields vLLM, SGLang and llama.cpp use, including their precedence.
func TestOpenAICompleteReadsReasoningContent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		want    string
	}{
		{"reasoning_content", `{"role":"assistant","content":"a","reasoning_content":"deliberating"}`, "deliberating"},
		{"thinking fallback", `{"role":"assistant","content":"a","thinking":"pondering"}`, "pondering"},
		{"reasoning_content wins", `{"role":"assistant","content":"a","reasoning_content":"first","thinking":"second"}`, "first"},
		{"neither", `{"role":"assistant","content":"a"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"index":0,"finish_reason":"stop","message":`+tc.message+`}]}`)
			}))
			defer srv.Close()

			p := newOpenAIProvider("m", "", srv.URL, nil, 0, 0, "")
			resp, err := p.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}}, nil)
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if resp.Reasoning != tc.want {
				t.Fatalf("reasoning = %q, want %q", resp.Reasoning, tc.want)
			}
		})
	}
}

// TestOpenAIBlockingParamsOmitStreamOptions guards the request shape: OpenAI
// rejects stream_options outright when the request is not a stream.
func TestOpenAIBlockingParamsOmitStreamOptions(t *testing.T) {
	p := newOpenAIProvider("gpt-4o", "key", "", nil, 1024, 0.2, "")
	msgs := []Message{{Role: RoleUser, Content: "hello"}}

	blocking, err := json.Marshal(p.buildParams(msgs, nil, false))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if gjson.GetBytes(blocking, "stream_options").Exists() {
		t.Fatalf("blocking request carries stream_options: %s", blocking)
	}

	streamed, err := json.Marshal(p.buildParams(msgs, nil, true))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !gjson.GetBytes(streamed, "stream_options.include_usage").Bool() {
		t.Fatalf("streaming request lost include_usage: %s", streamed)
	}
}

// TestBlockingProviderStopsReplayOnMidReplayCancel is the loop-guard contract: the
// consumer cancels from inside onChunk when a channel degenerates, and what comes
// back must describe only what it saw. Returning the rest would persist an answer
// nobody read and tool calls nobody executed, then replay those calls to the model
// with no matching results.
func TestBlockingProviderStopsReplayOnMidReplayCancel(t *testing.T) {
	inner := &recordingProvider{resp: &Response{
		Reasoning:  "round and round and round",
		Content:    "an answer nobody should see",
		StopReason: "tool_use",
		ToolCalls:  []ToolCall{{ID: "a", Name: "run_command", InputJSON: `{"command":"rm -rf /"}`}},
	}}
	p := newBlockingProvider(inner)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var chunks []StreamChunk
	resp, err := p.Stream(ctx, []Message{{Role: RoleUser, Content: "hi"}}, nil, func(c StreamChunk) {
		chunks = append(chunks, c)
		if c.ReasoningDelta != "" {
			cancel() // what the loop guard does when the thinking channel degenerates
		}
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(chunks) != 1 || chunks[0].ReasoningDelta == "" {
		t.Fatalf("chunks = %+v, want only the reasoning chunk", chunks)
	}
	if resp == nil {
		t.Fatal("resp = nil, want the delivered part of the response")
	}
	if resp.Reasoning != "round and round and round" {
		t.Fatalf("delivered reasoning = %q, want what was emitted", resp.Reasoning)
	}
	if resp.Content != "" || len(resp.ToolCalls) != 0 {
		t.Fatalf("resp = %+v, want no undelivered content or tool calls", resp)
	}
}
