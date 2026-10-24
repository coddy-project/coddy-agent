package llm

// Godog harness for features/anthropic_large_max_tokens.feature: the real
// Anthropic provider, built by NewProvider with a max_tokens above the 21333
// the SDK lets a non-streaming request promise, against a stub server that
// answers a streaming request with SSE and any other with one JSON message.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

const anthropicLargeAnswer = "Summary of the session."

// anthropicLargeStub records the body of every request and answers it in the
// shape the request asked for.
type anthropicLargeStub struct {
	mu       sync.Mutex
	requests []map[string]any
}

func (b *anthropicLargeStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	b.mu.Lock()
	b.requests = append(b.requests, body)
	b.mu.Unlock()
	if body["stream"] == true {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, anthropicTextAnswerSSE(anthropicLargeAnswer))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":%q}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":5}}`, anthropicLargeAnswer)
}

func (b *anthropicLargeStub) seen() []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]map[string]any(nil), b.requests...)
}

type anthropicLargeState struct {
	stub     *anthropicLargeStub
	server   *httptest.Server
	provider Provider
	resp     *Response
}

func (s *anthropicLargeState) reset() {
	if s.server != nil {
		s.server.Close()
	}
	s.stub, s.server, s.provider, s.resp = nil, nil, nil, nil
}

func (s *anthropicLargeState) model(maxTokens int, disableStream bool) error {
	s.stub = &anthropicLargeStub{}
	s.server = httptest.NewServer(s.stub)
	provider, err := NewProvider(ProviderInput{
		Name:          "claude",
		Type:          "anthropic",
		Model:         "claude-sonnet-4-5",
		APIKey:        "key",
		BaseURL:       s.server.URL,
		MaxTokens:     maxTokens,
		DisableStream: disableStream,
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("create anthropic provider: %w", err)
	}
	s.provider = provider
	return nil
}

func (s *anthropicLargeState) streamingModel(maxTokens int) error { return s.model(maxTokens, false) }

func (s *anthropicLargeState) blockingModel(maxTokens int) error { return s.model(maxTokens, true) }

func (s *anthropicLargeState) askComplete() error {
	resp, err := s.provider.Complete(context.Background(), []Message{{Role: RoleUser, Content: "Summarize."}}, nil)
	if err != nil {
		return fmt.Errorf("complete: %w", err)
	}
	s.resp = resp
	return nil
}

func (s *anthropicLargeState) askStream() error {
	resp, err := s.provider.Stream(context.Background(), []Message{{Role: RoleUser, Content: "Summarize."}}, nil, func(StreamChunk) {})
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}
	s.resp = resp
	return nil
}

func (s *anthropicLargeState) answerIs(text string) error {
	if s.resp == nil || s.resp.Content != text {
		return fmt.Errorf("answer = %+v, want %q", s.resp, text)
	}
	return nil
}

func (s *anthropicLargeState) receivedRequest(n int, wantStream bool, maxTokens int) error {
	seen := s.stub.seen()
	if len(seen) != n {
		return fmt.Errorf("stub received %d request(s), want %d", len(seen), n)
	}
	for i, body := range seen {
		stream, set := body["stream"]
		if wantStream && stream != true {
			return fmt.Errorf("request %d: stream = %v, want true", i, stream)
		}
		if !wantStream && set {
			return fmt.Errorf("request %d: stream = %v, want it unset", i, stream)
		}
		// JSON numbers decode as float64.
		if got, _ := body["max_tokens"].(float64); int(got) != maxTokens {
			return fmt.Errorf("request %d: max_tokens = %v, want %d", i, body["max_tokens"], maxTokens)
		}
	}
	return nil
}

func (s *anthropicLargeState) receivedStreaming(n, maxTokens int) error {
	return s.receivedRequest(n, true, maxTokens)
}

func (s *anthropicLargeState) receivedBlocking(n, maxTokens int) error {
	return s.receivedRequest(n, false, maxTokens)
}

func TestAnthropicLargeMaxTokensFeature(t *testing.T) {
	s := &anthropicLargeState{}
	suite := godog.TestSuite{
		Name: "anthropic-large-max-tokens",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s.reset()
				return ctx, nil
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				s.reset()
				return ctx, nil
			})
			sc.Step(`^an Anthropic model with max_tokens (\d+) pointed at a stub server$`, s.streamingModel)
			sc.Step(`^an Anthropic model with max_tokens (\d+) and streaming disabled pointed at a stub server$`, s.blockingModel)
			sc.Step(`^Coddy asks the model for one complete answer$`, s.askComplete)
			sc.Step(`^Coddy streams the model's answer$`, s.askStream)
			sc.Step(`^the answer is "([^"]*)"$`, s.answerIs)
			sc.Step(`^the stub server received (\d+) request with "stream" set to true and max_tokens (\d+)$`, s.receivedStreaming)
			sc.Step(`^the stub server received (\d+) request with "stream" unset and max_tokens (\d+)$`, s.receivedBlocking)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/anthropic_large_max_tokens.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("Anthropic large max_tokens feature suite failed")
	}
}
