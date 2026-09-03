package llm

// Godog harness for features/llm_retry_after.feature: exercises the real
// OpenAI and Anthropic providers against a stub upstream whose first answer
// is a 429 carrying a retry header (Retry-After seconds or Retry-After-Ms
// milliseconds) and whose second answer succeeds. The resilient wrapper must
// pace the retry by the server-provided value, not the exponential ladder,
// so the harness pins the wall-clock gap between the two upstream requests.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

const retryAfterOpenAICompletion = `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"test-model",` +
	`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
	`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

const retryAfterAnthropicCompletion = `{"id":"msg_1","type":"message","role":"assistant","model":"test-model",` +
	`"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,` +
	`"usage":{"input_tokens":1,"output_tokens":1}}`

type retryAfterState struct {
	server   *httptest.Server
	provider Provider

	mu       sync.Mutex
	requests []time.Time

	callErr error
}

func (s *retryAfterState) reset() {
	s.cleanup()
	s.provider = nil
	s.mu.Lock()
	s.requests = nil
	s.mu.Unlock()
	s.callErr = nil
}

func (s *retryAfterState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
}

func (s *retryAfterState) aProviderWhoseUpstreamRateLimits(providerType, header, value string) error {
	success := retryAfterOpenAICompletion
	if providerType == "anthropic" {
		success = retryAfterAnthropicCompletion
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, time.Now())
		first := len(s.requests) == 1
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if first {
			// The body deliberately carries neither "Limit resets at:" nor a
			// "retry in Ns" phrase, so only the header can explain the pause.
			w.Header().Set(header, value)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limit reached","type":"rate_limit_error","code":"429"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(success))
	}))

	provider, err := NewProvider(ProviderInput{
		Type:    providerType,
		Model:   "test-model",
		APIKey:  "test-key",
		BaseURL: s.server.URL,
		// A millisecond backoff base makes the exponential ladder invisible on
		// the clock: any observed pause must come from the server header.
		RetryMax:      2,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: 30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("create %s provider: %w", providerType, err)
	}
	s.provider = provider
	return nil
}

func (s *retryAfterState) aCompletionIsRequested() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, s.callErr = s.provider.Complete(ctx, []Message{{Role: RoleUser, Content: "hello"}}, nil)
	return nil
}

func (s *retryAfterState) theCallSucceedsAfterUpstreamRequests(want int) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	s.mu.Lock()
	got := len(s.requests)
	s.mu.Unlock()
	if got != want {
		return fmt.Errorf("upstream requests = %d, want %d", got, want)
	}
	return nil
}

func (s *retryAfterState) atLeastMsPassBetweenRequests(minMS int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) < 2 {
		return fmt.Errorf("need at least 2 upstream requests, got %d", len(s.requests))
	}
	gap := s.requests[1].Sub(s.requests[0])
	if gap < time.Duration(minMS)*time.Millisecond {
		return fmt.Errorf("gap between requests = %v, want at least %dms", gap, minMS)
	}
	return nil
}

func initializeRetryAfterScenario(sc *godog.ScenarioContext) {
	s := &retryAfterState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^an "([^"]+)" provider whose upstream responds 429 with header "([^"]+)" set to "([^"]+)" and then succeeds$`, s.aProviderWhoseUpstreamRateLimits)
	sc.Step(`^a completion is requested$`, s.aCompletionIsRequested)
	sc.Step(`^the call succeeds after (\d+) upstream requests$`, s.theCallSucceedsAfterUpstreamRequests)
	sc.Step(`^at least (\d+) ms pass between the two upstream requests$`, s.atLeastMsPassBetweenRequests)
}

func TestLLMRetryAfterFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llm-retry-after",
		ScenarioInitializer: initializeRetryAfterScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llm_retry_after.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("LLM retry-after feature suite failed")
	}
}
