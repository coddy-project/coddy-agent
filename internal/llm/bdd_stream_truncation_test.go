package llm

// Godog harness for features/llm_stream_truncation.feature: exercises the
// real OpenAI and Codex providers against a stub server that closes the SSE
// stream mid-generation. A stream that ends with neither a [DONE] marker nor a
// finish_reason (for Codex: neither response.completed nor
// response.incomplete) must fail with a truncation error while preserving the
// text already delivered; a terminal event without the marker stays a success.
// A frame or event cut inside its JSON (issue #384) is the same truncation,
// and the request count proves the wrapper did not replay a stream whose
// deltas had reached the caller.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
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

	// The body ends inside the JSON of the last frame, with no blank line
	// after it: the lenient reader dispatches that frame like a browser
	// would, and its decode fails at the end of the input. The cut lands
	// inside a string value on purpose, the commonest place for a cut mid
	// delta; which end-of-input diagnostic the decoder reports depends on
	// that position (streamDecodeTruncation names the spellings).
	"cuts the last frame inside its JSON": "data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null}}],\"id\":\"chatcmpl-j1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"Hello\"}}],\"id\":\"chatcmpl-j1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\" fr\"}}],\"id\":\"chatcmpl-j1\",\"model\":\"test-model\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"om",
}

// codexStreamTruncationScripts are the Codex Responses counterparts, named the
// same way the steps name them.
var codexStreamTruncationScripts = map[string]string{
	"cuts the stream after text deltas": "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" fr\"}\n\n",

	"completes the response": "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello from server\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":3}}}\n\n",

	"stops the response at its output cap": "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello fr\"}\n\n" +
		"event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n",

	// The shape the live backend sends while the model thinks without a
	// word: a comment frame of its own after a few seconds of silence, which
	// the SDK decoder dispatched as an event with no data and failed to
	// decode as "unexpected end of JSON input".
	"sends keep-alive comments while the model is silent": "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"sequence_number\":1,\"response\":{\"status\":\"in_progress\"}}\n\n" +
		": keep-alive\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n" +
		": keep-alive\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" from server\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":3}}}\n\n",

	// A framed event (the blank line is there) whose JSON stops short: the
	// SDK decoder dispatches it and its decode fails at the end of the input,
	// the error of issue #384. The cut lands inside a string value on
	// purpose; the SDK decoder appends a newline to the data line, so this
	// position reads "invalid character '\n' in string literal".
	"cuts an event inside its JSON after text deltas": "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" fr\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"del\n\n",
}

type streamTruncationState struct {
	server   *httptest.Server
	provider Provider
	resp     *Response
	callErr  error
	// requests counts what the stub served: one per attempt of the wrapper.
	requests atomic.Int32
	// prevCodexBaseURL restores the process-wide Codex redirect after a codex
	// scenario; hadCodexBaseURL says whether it was set at all, so a variable
	// that was absent is removed rather than left empty.
	prevCodexBaseURL string
	hadCodexBaseURL  bool
	setCodexBaseURL  bool
	authDir          string
}

func (s *streamTruncationState) reset() {
	s.cleanup()
	s.provider = nil
	s.resp = nil
	s.callErr = nil
	s.requests.Store(0)
}

func (s *streamTruncationState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
	if s.setCodexBaseURL {
		if s.hadCodexBaseURL {
			_ = os.Setenv(EnvCodexBaseURL, s.prevCodexBaseURL)
		} else {
			_ = os.Unsetenv(EnvCodexBaseURL)
		}
		s.setCodexBaseURL = false
	}
	if s.authDir != "" {
		_ = os.RemoveAll(s.authDir)
		s.authDir = ""
	}
}

func (s *streamTruncationState) aCodexProviderPointedAtStub(scenario string) error {
	script, ok := codexStreamTruncationScripts[scenario]
	if !ok {
		return fmt.Errorf("unknown codex stub scenario %q", scenario)
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, script)
	}))
	dir, err := os.MkdirTemp("", "coddy-codex-truncation-*")
	if err != nil {
		return err
	}
	s.authDir = dir
	authPath := filepath.Join(dir, "codex-auth.json")
	auth := fmt.Sprintf(`{"auth_mode":"chatgpt","tokens":{"access_token":%q,"refresh_token":"rt","account_id":"acct"}}`,
		makeJWT(time.Now().Add(time.Hour)))
	if err := os.WriteFile(authPath, []byte(auth), 0o600); err != nil {
		return err
	}
	s.prevCodexBaseURL, s.hadCodexBaseURL = os.LookupEnv(EnvCodexBaseURL)
	s.setCodexBaseURL = true
	if err := os.Setenv(EnvCodexBaseURL, s.server.URL); err != nil {
		return err
	}
	provider, err := NewProvider(ProviderInput{
		Type:          "codex",
		Model:         "gpt-5.5",
		AuthPath:      authPath,
		RetryMax:      1,
		RetryBase:     time.Millisecond,
		RetryMaxDelay: time.Millisecond,
	})
	if err != nil {
		return fmt.Errorf("create codex provider: %w", err)
	}
	s.provider = provider
	return nil
}

func (s *streamTruncationState) aProviderPointedAtTruncatingStub(scenario string) error {
	script, ok := streamTruncationScripts[scenario]
	if !ok {
		return fmt.Errorf("unknown stub scenario %q", scenario)
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.requests.Add(1)
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

func (s *streamTruncationState) theStubServerReceivedRequests(want int) error {
	if got := int(s.requests.Load()); got != want {
		return fmt.Errorf("the stub server received %d requests, want %d", got, want)
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

	sc.Step(`^an "openai" provider pointed at a stub server that (cuts the stream after text deltas|ends the stream with a finish_reason but no \[DONE\] marker|cuts the last frame inside its JSON)$`, s.aProviderPointedAtTruncatingStub)
	sc.Step(`^a "codex" provider pointed at a stub server that (cuts the stream after text deltas|completes the response|stops the response at its output cap|cuts an event inside its JSON after text deltas|sends keep-alive comments while the model is silent)$`, s.aCodexProviderPointedAtStub)
	sc.Step(`^a streaming completion is requested$`, s.aTruncationStreamingCompletionIsRequested)
	sc.Step(`^the call fails with a truncation error$`, s.theCallFailsWithATruncationError)
	sc.Step(`^the partial response preserves text "([^"]*)"$`, s.thePartialResponsePreservesText)
	sc.Step(`^the call succeeds with the complete text "([^"]*)"$`, s.theCallSucceedsWithCompleteText)
	sc.Step(`^the reported stop reason is "([^"]*)"$`, s.theReportedStopReasonIs)
	sc.Step(`^the stub server received (\d+) requests?$`, s.theStubServerReceivedRequests)
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
