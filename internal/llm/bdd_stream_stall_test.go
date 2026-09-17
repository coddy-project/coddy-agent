package llm

// Godog harness for features/llm_stream_stall.feature: exercises the real
// OpenAI provider, built through NewProvider with a stream idle timeout,
// against a stub upstream that starts answering and then goes quiet. The
// guard has to cut the stream after the idle time, keep the delivered text
// next to a stall error, and repeat the request only while no delta reached
// the caller.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

type streamStallState struct {
	server   *httptest.Server
	provider Provider
	requests atomic.Int32
	idle     time.Duration
	resp     *Response
	callErr  error
}

func (s *streamStallState) reset() {
	s.cleanup()
	s.provider = nil
	s.requests.Store(0)
	s.idle = 0
	s.resp = nil
	s.callErr = nil
}

func (s *streamStallState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
}

func stallDelta(text string) string {
	return "data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"" + text + "\"}}],\"id\":\"chatcmpl-s1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n"
}

// stallCompletion is the answer the healthy request streams.
func stallCompletion(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w,
		stallDelta("Hello after retry")+
			"data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}],\"id\":\"chatcmpl-s2\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n"+
			"data: [DONE]\n\n")
}

func (s *streamStallState) newProvider(idleMS int) error {
	s.idle = time.Duration(idleMS) * time.Millisecond
	provider, err := NewProvider(ProviderInput{
		Type:              "openai",
		Model:             "test-model",
		BaseURL:           s.server.URL,
		RetryMax:          1,
		RetryBase:         time.Millisecond,
		RetryMaxDelay:     time.Millisecond,
		StreamIdleTimeout: s.idle,
	})
	if err != nil {
		return fmt.Errorf("create openai provider: %w", err)
	}
	s.provider = provider
	return nil
}

// aProviderStallingAfterTextDeltas streams two deltas and then holds the
// connection open without another byte until the client gives up.
func (s *streamStallState) aProviderStallingAfterTextDeltas(idleMS int) error {
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Both deltas in one write, so a slow runner cannot open a gap the
		// guard would take for the stall.
		_, _ = io.WriteString(w, stallDelta("Hello")+stallDelta(" fr"))
		flusher.Flush()
		// The model is stuck: nothing more arrives until the client cancels.
		<-r.Context().Done()
	}))
	return s.newProvider(idleMS)
}

// aProviderStallingOnceAfterEmptyFrame answers the first request with a
// role-only chunk and then nothing; the second request streams normally.
func (s *streamStallState) aProviderStallingOnceAfterEmptyFrame(idleMS int) error {
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.requests.Add(1) == 1 {
			flusher, _ := w.(http.Flusher)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"role\":\"assistant\"}}],\"id\":\"chatcmpl-s0\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n")
			flusher.Flush()
			<-r.Context().Done()
			return
		}
		stallCompletion(w)
	}))
	return s.newProvider(idleMS)
}

func (s *streamStallState) aStreamingCompletionIsRequested() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s.resp, s.callErr = s.provider.Stream(ctx,
		[]Message{{Role: RoleUser, Content: "hello"}},
		nil,
		func(StreamChunk) {})
	return nil
}

func (s *streamStallState) theCallFailsWithAStallError() error {
	if s.callErr == nil {
		return fmt.Errorf("expected a stall error, the call succeeded with %q", contentOf(s.resp))
	}
	if !IsStreamStalled(s.callErr) {
		return fmt.Errorf("error is not a stall: %v", s.callErr)
	}
	if !strings.Contains(s.callErr.Error(), s.idle.String()) {
		return fmt.Errorf("error does not name the idle time %v: %v", s.idle, s.callErr)
	}
	return nil
}

func (s *streamStallState) thePartialResponsePreservesText(want string) error {
	if s.resp == nil {
		return fmt.Errorf("no partial response returned next to the error")
	}
	if s.resp.Content != want {
		return fmt.Errorf("partial content = %q, want %q", s.resp.Content, want)
	}
	return nil
}

func (s *streamStallState) theStubServerReceivedRequests(n int) error {
	if got := int(s.requests.Load()); got != n {
		return fmt.Errorf("upstream requests = %d, want %d", got, n)
	}
	return nil
}

func (s *streamStallState) theCallSucceedsWithTextInRequests(want string, requests int) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	if s.resp == nil || s.resp.Content != want {
		return fmt.Errorf("content = %q, want %q", contentOf(s.resp), want)
	}
	return s.theStubServerReceivedRequests(requests)
}

func initializeStreamStallScenario(sc *godog.ScenarioContext) {
	s := &streamStallState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^an "openai" provider with a stream idle timeout of (\d+) ms pointed at a stub server that stalls after text deltas$`, s.aProviderStallingAfterTextDeltas)
	sc.Step(`^an "openai" provider with a stream idle timeout of (\d+) ms whose upstream stalls once after an empty first frame and then streams a completion$`, s.aProviderStallingOnceAfterEmptyFrame)
	sc.Step(`^a streaming completion is requested$`, s.aStreamingCompletionIsRequested)
	sc.Step(`^the call fails with a stall error that names the idle time$`, s.theCallFailsWithAStallError)
	sc.Step(`^the partial response preserves text "([^"]*)"$`, s.thePartialResponsePreservesText)
	sc.Step(`^the stub server received (\d+) requests?$`, s.theStubServerReceivedRequests)
	sc.Step(`^the call succeeds with text "([^"]*)" in (\d+) upstream requests$`, s.theCallSucceedsWithTextInRequests)
}

func TestLLMStreamStallFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llm-stream-stall",
		ScenarioInitializer: initializeStreamStallScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llm_stream_stall.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("LLM stream stall feature suite failed")
	}
}
