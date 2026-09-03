package llm

// Godog harness for features/llm_transport_retry.feature: exercises the real
// OpenAI provider against a stub upstream whose first response dies at the
// transport level before any SSE output (a declared Content-Length with an
// empty body yields io.ErrUnexpectedEOF on the client), and whose second
// response streams a normal completion. The resilient wrapper must classify
// the status-less transport failure as retryable and repeat the request.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

type transportRetryState struct {
	server   *httptest.Server
	provider Provider
	requests atomic.Int32
	resp     *Response
	callErr  error
}

func (s *transportRetryState) reset() {
	s.cleanup()
	s.provider = nil
	s.requests.Store(0)
	s.resp = nil
	s.callErr = nil
}

func (s *transportRetryState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
}

func (s *transportRetryState) aProviderWhoseUpstreamCutsOnce() error {
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if s.requests.Add(1) == 1 {
			// Promise a body and send none: the client reads an unexpected
			// EOF with no HTTP status attached to the failure.
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"Hello after retry\"}}],\"id\":\"chatcmpl-r1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n"+
				"data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}],\"id\":\"chatcmpl-r1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n"+
				"data: [DONE]\n\n")
	}))
	provider, err := NewProvider(ProviderInput{
		Type:          "openai",
		Model:         "test-model",
		BaseURL:       s.server.URL,
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("create openai provider: %w", err)
	}
	s.provider = provider
	return nil
}

func (s *transportRetryState) aTransportRetryStreamingCompletionIsRequested() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s.resp, s.callErr = s.provider.Stream(ctx,
		[]Message{{Role: RoleUser, Content: "hello"}},
		nil,
		func(StreamChunk) {})
	return nil
}

func (s *transportRetryState) theCallSucceedsWithTextInRequests(want string, requests int) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	if s.resp == nil || s.resp.Content != want {
		return fmt.Errorf("content = %q, want %q", contentOf(s.resp), want)
	}
	if got := int(s.requests.Load()); got != requests {
		return fmt.Errorf("upstream requests = %d, want %d", got, requests)
	}
	return nil
}

func initializeTransportRetryScenario(sc *godog.ScenarioContext) {
	s := &transportRetryState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^an "openai" provider whose upstream cuts the connection once before any output and then streams a completion$`, s.aProviderWhoseUpstreamCutsOnce)
	sc.Step(`^a streaming completion is requested$`, s.aTransportRetryStreamingCompletionIsRequested)
	sc.Step(`^the call succeeds with text "([^"]*)" in (\d+) upstream requests$`, s.theCallSucceedsWithTextInRequests)
}

func TestLLMTransportRetryFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llm-transport-retry",
		ScenarioInitializer: initializeTransportRetryScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llm_transport_retry.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("LLM transport retry feature suite failed")
	}
}
