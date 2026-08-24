package llm

// Godog harness for features/llm_stream_truncation.feature: exercises the
// real OpenAI provider against a stub server that closes the SSE stream
// mid-generation. A stream that ends with neither a [DONE] marker nor a
// finish_reason must fail with a truncation error while preserving the text
// already delivered; a finish_reason without the marker stays a success.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// streamTruncationScripts are SSE payloads the stub server replays verbatim
// before closing the connection.
var streamTruncationScripts = map[string]string{
	"cuts the stream after text deltas": "data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null}}],\"id\":\"chatcmpl-c1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"Hello\"}}],\"id\":\"chatcmpl-c1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\" fr\"}}],\"id\":\"chatcmpl-c1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n",

	"ends the stream with a finish_reason but no [DONE] marker": "data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null}}],\"id\":\"chatcmpl-f1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"Hello from server\"}}],\"id\":\"chatcmpl-f1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}],\"id\":\"chatcmpl-f1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n",
}

type streamTruncationState struct {
	server   *httptest.Server
	provider Provider
	resp     *Response
	callErr  error
}

func (s *streamTruncationState) reset() {
	s.cleanup()
	s.provider = nil
	s.resp = nil
	s.callErr = nil
}

func (s *streamTruncationState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
}

func (s *streamTruncationState) aProviderPointedAtTruncatingStub(scenario string) error {
	script, ok := streamTruncationScripts[scenario]
	if !ok {
		return fmt.Errorf("unknown stub scenario %q", scenario)
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, script)
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

func (s *streamTruncationState) aTruncationStreamingCompletionIsRequested() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s.resp, s.callErr = s.provider.Stream(ctx,
		[]Message{{Role: RoleUser, Content: "hello"}},
		nil,
		func(StreamChunk) {})
	return nil
}

func (s *streamTruncationState) theCallFailsWithATruncationError() error {
	if s.callErr == nil {
		return fmt.Errorf("provider call unexpectedly succeeded with %+v", s.resp)
	}
	if !IsStreamTruncated(s.callErr) {
		return fmt.Errorf("error %q is not classified as a stream truncation", s.callErr)
	}
	return nil
}

func (s *streamTruncationState) thePartialResponsePreservesText(want string) error {
	if s.resp == nil {
		return fmt.Errorf("no partial response returned (error: %v)", s.callErr)
	}
	if s.resp.Content != want {
		return fmt.Errorf("partial content = %q, want %q", s.resp.Content, want)
	}
	return nil
}

func (s *streamTruncationState) theCallSucceedsWithCompleteText(want string) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	if s.resp == nil || s.resp.Content != want {
		return fmt.Errorf("content = %q, want %q", contentOf(s.resp), want)
	}
	return nil
}

func (s *streamTruncationState) theReportedStopReasonIs(want string) error {
	if s.resp == nil {
		return fmt.Errorf("no response (error: %v)", s.callErr)
	}
	if s.resp.StopReason != want {
		return fmt.Errorf("stop reason = %q, want %q", s.resp.StopReason, want)
	}
	return nil
}

func initializeStreamTruncationScenario(sc *godog.ScenarioContext) {
	s := &streamTruncationState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^an "openai" provider pointed at a stub server that (cuts the stream after text deltas|ends the stream with a finish_reason but no \[DONE\] marker)$`, s.aProviderPointedAtTruncatingStub)
	sc.Step(`^a streaming completion is requested$`, s.aTruncationStreamingCompletionIsRequested)
	sc.Step(`^the call fails with a truncation error$`, s.theCallFailsWithATruncationError)
	sc.Step(`^the partial response preserves text "([^"]*)"$`, s.thePartialResponsePreservesText)
	sc.Step(`^the call succeeds with the complete text "([^"]*)"$`, s.theCallSucceedsWithCompleteText)
	sc.Step(`^the reported stop reason is "([^"]*)"$`, s.theReportedStopReasonIs)
}

func TestLLMStreamTruncationFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llm-stream-truncation",
		ScenarioInitializer: initializeStreamTruncationScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llm_stream_truncation.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("LLM stream truncation feature suite failed")
	}
}
