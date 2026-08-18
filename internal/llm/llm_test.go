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
