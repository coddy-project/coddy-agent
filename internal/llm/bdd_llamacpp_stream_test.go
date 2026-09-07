package llm

// Godog harness for features/llamacpp_openai_stream.feature: exercises the real
// OpenAI provider against a stub server replaying SSE captured from llama.cpp
// builds (b5966 mid-stream "error:" frames, keep-alive comments, empty data
// frames, b9038-style tool call and usage chunks).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// llamaCPPStreamScripts are verbatim SSE payloads in the llama.cpp dialect.
// Frames were captured from real llama-server builds and trimmed to the
// fields the provider consumes.
var llamaCPPStreamScripts = map[string]string{
	"streaming a completion with framing quirks": ": keep-alive\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null}}],\"created\":1786903885,\"id\":\"chatcmpl-x1\",\"model\":\"qwen3-1.7b\",\"system_fingerprint\":\"b5966-14c28dfc\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: \n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"reasoning_content\":\"Thinking.\"}}],\"created\":1786903885,\"id\":\"chatcmpl-x1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"Hello from \"}}],\"created\":1786903885,\"id\":\"chatcmpl-x1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"content\":\"llama.cpp\"}}],\"created\":1786903885,\"id\":\"chatcmpl-x1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}],\"created\":1786903885,\"id\":\"chatcmpl-x1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[],\"created\":1786903885,\"id\":\"chatcmpl-x1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\",\"usage\":{\"completion_tokens\":5,\"prompt_tokens\":12,\"total_tokens\":17,\"prompt_tokens_details\":{\"cached_tokens\":0}},\"timings\":{\"prompt_n\":12,\"predicted_n\":5}}\n\n" +
		"data: [DONE]\n\n",

	"streaming a tool call": "data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null}}],\"created\":1786903271,\"id\":\"chatcmpl-t1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]}}],\"created\":1786903271,\"id\":\"chatcmpl-t1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\\\"Par\"}}]}}],\"created\":1786903271,\"id\":\"chatcmpl-t1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"is\\\"}\"}}]}}],\"created\":1786903271,\"id\":\"chatcmpl-t1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"tool_calls\",\"index\":0,\"delta\":{}}],\"created\":1786903271,\"id\":\"chatcmpl-t1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"data: {\"choices\":[],\"created\":1786903271,\"id\":\"chatcmpl-t1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\",\"usage\":{\"completion_tokens\":87,\"prompt_tokens\":157,\"total_tokens\":244}}\n\n" +
		"data: [DONE]\n\n",

	"fails mid-stream with an \"error:\" frame": "data: {\"choices\":[{\"finish_reason\":null,\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null}}],\"created\":1786903885,\"id\":\"chatcmpl-e1\",\"model\":\"qwen3-1.7b\",\"object\":\"chat.completion.chunk\"}\n\n" +
		"error: {\"code\":400,\"message\":\"the request exceeds the available context size. try increasing the context size or enable context shift\",\"type\":\"invalid_request_error\"}\n\n" +
		"data: [DONE]\n\n",
}

type llamaCPPStreamState struct {
	server   *httptest.Server
	provider Provider
	chunks   []StreamChunk
	resp     *Response
	callErr  error
}

func (s *llamaCPPStreamState) reset() {
	s.cleanup()
	s.provider = nil
	s.chunks = nil
	s.resp = nil
	s.callErr = nil
}

func (s *llamaCPPStreamState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
}

func (s *llamaCPPStreamState) aProviderPointedAtStub(scenario string) error {
	script, ok := llamaCPPStreamScripts[scenario]
	if !ok {
		return fmt.Errorf("unknown stub scenario %q", scenario)
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, script)
	}))
	provider, err := NewProvider(ProviderInput{
		Type:          "openai",
		Model:         "qwen3-1.7b",
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

func (s *llamaCPPStreamState) aStreamingCompletionIsRequested() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s.resp, s.callErr = s.provider.Stream(ctx,
		[]Message{{Role: RoleUser, Content: "hello"}},
		nil,
		func(c StreamChunk) { s.chunks = append(s.chunks, c) })
	return nil
}

func (s *llamaCPPStreamState) theCallSucceedsWithText(want string) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	if s.resp == nil || s.resp.Content != want {
		return fmt.Errorf("content = %q, want %q", contentOf(s.resp), want)
	}
	return nil
}

func (s *llamaCPPStreamState) theReasoningDeltaIsStreamed(want string) error {
	var reasoning strings.Builder
	for _, c := range s.chunks {
		reasoning.WriteString(c.ReasoningDelta)
	}
	if reasoning.String() != want {
		return fmt.Errorf("reasoning deltas = %q, want %q", reasoning.String(), want)
	}
	return nil
}

func (s *llamaCPPStreamState) theStopReasonIs(want string) error {
	if s.resp == nil {
		return fmt.Errorf("no response (error: %v)", s.callErr)
	}
	if s.resp.StopReason != want {
		return fmt.Errorf("stop reason = %q, want %q", s.resp.StopReason, want)
	}
	return nil
}

func (s *llamaCPPStreamState) usageReports(input, output int) error {
	if s.resp == nil {
		return fmt.Errorf("no response (error: %v)", s.callErr)
	}
	if s.resp.InputTokens != input || s.resp.OutputTokens != output {
		return fmt.Errorf("usage = %d/%d, want %d/%d", s.resp.InputTokens, s.resp.OutputTokens, input, output)
	}
	return nil
}

func (s *llamaCPPStreamState) theCallSucceedsWithAToolCall(name, args string) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	if s.resp == nil || len(s.resp.ToolCalls) != 1 {
		return fmt.Errorf("tool calls = %+v, want exactly one", toolCallsOf(s.resp))
	}
	tc := s.resp.ToolCalls[0]
	if tc.Name != name || tc.InputJSON != args {
		return fmt.Errorf("tool call = %q(%s), want %q(%s)", tc.Name, tc.InputJSON, name, args)
	}
	return nil
}

func (s *llamaCPPStreamState) theCallFailsWithErrorContaining(want string) error {
	if s.callErr == nil {
		return fmt.Errorf("provider call unexpectedly succeeded")
	}
	if !strings.Contains(s.callErr.Error(), want) {
		return fmt.Errorf("error %q does not contain %q", s.callErr.Error(), want)
	}
	return nil
}

func (s *llamaCPPStreamState) theErrorDoesNotContain(text string) error {
	if s.callErr == nil {
		return fmt.Errorf("provider call unexpectedly succeeded")
	}
	if strings.Contains(s.callErr.Error(), text) {
		return fmt.Errorf("error %q must not contain %q", s.callErr.Error(), text)
	}
	return nil
}

func contentOf(r *Response) string {
	if r == nil {
		return "<nil>"
	}
	return r.Content
}

func toolCallsOf(r *Response) []ToolCall {
	if r == nil {
		return nil
	}
	return r.ToolCalls
}

func initializeLlamaCPPStreamScenario(sc *godog.ScenarioContext) {
	s := &llamaCPPStreamState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^an "openai" provider pointed at a stub llama\.cpp server (streaming a completion with framing quirks|streaming a tool call)$`, s.aProviderPointedAtStub)
	sc.Step(`^an "openai" provider pointed at a stub llama\.cpp server that (fails mid-stream with an "error:" frame)$`, s.aProviderPointedAtStub)
	sc.Step(`^a streaming completion is requested$`, s.aStreamingCompletionIsRequested)
	sc.Step(`^the call succeeds with text "([^"]*)"$`, s.theCallSucceedsWithText)
	sc.Step(`^the reasoning delta "([^"]*)" is streamed$`, s.theReasoningDeltaIsStreamed)
	sc.Step(`^the stop reason is "([^"]*)"$`, s.theStopReasonIs)
	sc.Step(`^usage reports (\d+) input and (\d+) output tokens$`, s.usageReports)
	sc.Step(`^the call succeeds with a "([^"]*)" tool call with arguments (.+)$`, s.theCallSucceedsWithAToolCall)
	sc.Step(`^the call fails with an error containing "([^"]*)"$`, s.theCallFailsWithErrorContaining)
	sc.Step(`^the error does not contain "([^"]*)"$`, s.theErrorDoesNotContain)
}

func TestLlamaCPPOpenAIStreamFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "llamacpp-openai-stream",
		ScenarioInitializer: initializeLlamaCPPStreamScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/llamacpp_openai_stream.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("llama.cpp OpenAI stream feature suite failed")
	}
}
