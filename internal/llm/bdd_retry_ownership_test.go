package llm

// Godog harness for features/llm_retry_ownership.feature: exercises the real
// OpenAI and Anthropic providers against a request-counting HTTP server.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

type retryOwnershipState struct {
	server   *httptest.Server
	provider Provider
	mode     string
	requests atomic.Int32
	callErr  error
}

func (s *retryOwnershipState) reset() {
	s.cleanup()
	s.provider = nil
	s.mode = ""
	s.requests.Store(0)
	s.callErr = nil
}

func (s *retryOwnershipState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
}

func (s *retryOwnershipState) aProviderWithOneConfiguredRetry(providerType, mode string) error {
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"temporary failure","type":"server_error"}}`))
	}))

	provider, err := NewProvider(ProviderInput{
		Type:          providerType,
		Model:         "test-model",
		APIKey:        "test-key",
		BaseURL:       s.server.URL,
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("create %s provider: %w", providerType, err)
	}
	s.provider = provider
	s.mode = mode
	return nil
}

func (s *retryOwnershipState) theUpstreamRespondsWithARetryableServerError() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	messages := []Message{{Role: RoleUser, Content: "hello"}}
	switch s.mode {
	case "non-streaming":
		_, s.callErr = s.provider.Complete(ctx, messages, nil)
	case "streaming":
		_, s.callErr = s.provider.Stream(ctx, messages, nil, func(StreamChunk) {})
	default:
		return fmt.Errorf("unsupported request mode %q", s.mode)
	}
	return nil
}

func (s *retryOwnershipState) theProviderCallFails() error {
	if s.callErr == nil {
		return fmt.Errorf("provider call unexpectedly succeeded")
	}
	return nil
}

func (s *retryOwnershipState) exactlyUpstreamRequestsAreSent(want int) error {
	if got := int(s.requests.Load()); got != want {
		return fmt.Errorf("upstream requests = %d, want %d", got, want)
	}
	return nil
}

func initializeRetryOwnershipScenario(sc *godog.ScenarioContext) {
	s := &retryOwnershipState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^a "([^"]+)" provider using "([^"]+)" requests with one configured retry$`, s.aProviderWithOneConfiguredRetry)
	sc.Step(`^the upstream responds with a retryable server error$`, s.theUpstreamRespondsWithARetryableServerError)
	sc.Step(`^the provider call fails$`, s.theProviderCallFails)
	sc.Step(`^exactly (\d+) upstream requests are sent$`, s.exactlyUpstreamRequestsAreSent)
}

func TestLLMRetryOwnershipFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llm-retry-ownership",
		ScenarioInitializer: initializeRetryOwnershipScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llm_retry_ownership.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("LLM retry ownership feature suite failed")
	}
}
