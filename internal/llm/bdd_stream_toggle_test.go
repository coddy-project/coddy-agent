package llm

// Godog harness for features/model_stream_toggle.feature: a provider built with
// DisableStream (models[].stream: false) against a stub OpenAI-compatible server
// that answers a plain chat completion and rejects anything asking for SSE. The
// stub records every request body, so what actually went on the wire - and not
// only what came back - is what the scenarios assert.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"
)

// streamToggleBodies are the blocking chat completion responses served per scenario.
var streamToggleBodies = map[string]string{
	"that refuses streaming requests": `{"id":"chatcmpl-b1","object":"chat.completion","model":"qwen3-1.7b",` +
		`"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"Blocking answer."}}],` +
		`"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}}`,

	"that answers with reasoning": `{"id":"chatcmpl-b2","object":"chat.completion","model":"qwen3-1.7b",` +
		`"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","reasoning_content":"Deliberating.",` +
		`"content":"Answer after thinking."}}],"usage":{"prompt_tokens":12,"completion_tokens":6,"total_tokens":18}}`,

	"that answers with a tool call": `{"id":"chatcmpl-b3","object":"chat.completion","model":"qwen3-1.7b",` +
		`"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"",` +
		`"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]}}],` +
		`"usage":{"prompt_tokens":20,"completion_tokens":9,"total_tokens":29}}`,
}

// streamToggleSSE is the streamed answer served when the model keeps the default transport.
const streamToggleSSE = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Streamed \"}}],\"object\":\"chat.completion.chunk\"}\n\n" +
	"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer.\"}}],\"object\":\"chat.completion.chunk\"}\n\n" +
	"data: {\"choices\":[{\"index\":0,\"finish_reason\":\"stop\",\"delta\":{}}],\"object\":\"chat.completion.chunk\"}\n\n" +
	"data: [DONE]\n\n"

// streamToggleStub answers chat completions and records the raw request bodies.
type streamToggleStub struct {
	body string

	mu       sync.Mutex
	requests []string
}

func (b *streamToggleStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.requests = append(b.requests, string(raw))
	b.mu.Unlock()

	if gjson.GetBytes(raw, "stream").Bool() {
		if b.body == streamToggleSSE {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, streamToggleSSE)
			return
		}
		// Stands in for a server or proxy that cannot serve SSE: a model configured
		// with stream: false must never reach this branch.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"streaming is not supported by this server","code":400}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, b.body)
}

func (b *streamToggleStub) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.requests...)
}

type streamToggleState struct {
	stub     *streamToggleStub
	server   *httptest.Server
	provider Provider
	chunks   []StreamChunk
	resp     *Response
	callErr  error
}

func (s *streamToggleState) reset() {
	s.cleanup()
	s.provider = nil
	s.chunks = nil
	s.resp = nil
	s.callErr = nil
}

func (s *streamToggleState) cleanup() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
	s.stub = nil
}

func (s *streamToggleState) aBlockingProviderPointedAtStub(scenario string) error {
	body, ok := streamToggleBodies[scenario]
	if !ok {
		return fmt.Errorf("unknown stub scenario %q", scenario)
	}
	return s.startProvider(body, true)
}

func (s *streamToggleState) aStreamingProviderPointedAtStub() error {
	return s.startProvider(streamToggleSSE, false)
}

func (s *streamToggleState) startProvider(body string, disableStream bool) error {
	s.stub = &streamToggleStub{body: body}
	s.server = httptest.NewServer(s.stub)
	provider, err := NewProvider(ProviderInput{
		Type:          "openai",
		Model:         "qwen3-1.7b",
		BaseURL:       s.server.URL,
		DisableStream: disableStream,
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

func (s *streamToggleState) aStreamingCompletionIsRequested() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s.resp, s.callErr = s.provider.Stream(ctx,
		[]Message{{Role: RoleUser, Content: "hello"}},
		nil,
		func(c StreamChunk) { s.chunks = append(s.chunks, c) })
	return nil
}

func (s *streamToggleState) theCallSucceedsWithTextToggle(want string) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	if s.resp == nil || s.resp.Content != want {
		return fmt.Errorf("content = %q, want %q", contentOf(s.resp), want)
	}
	return nil
}

func (s *streamToggleState) theStubReceivedRequestsWithStreamUnset(count int) error {
	requests := s.stub.snapshot()
	if len(requests) != count {
		return fmt.Errorf("stub received %d requests, want %d", len(requests), count)
	}
	for i, raw := range requests {
		if gjson.Get(raw, "stream").Exists() {
			return fmt.Errorf("request %d sent stream=%s, want the field absent", i+1, gjson.Get(raw, "stream").Raw)
		}
		// stream_options is rejected outright by OpenAI on a blocking request.
		if gjson.Get(raw, "stream_options").Exists() {
			return fmt.Errorf("request %d sent stream_options on a blocking request", i+1)
		}
	}
	return nil
}

func (s *streamToggleState) theStubReceivedRequestsWithStreamTrue(count int) error {
	requests := s.stub.snapshot()
	if len(requests) != count {
		return fmt.Errorf("stub received %d requests, want %d", len(requests), count)
	}
	for i, raw := range requests {
		if !gjson.Get(raw, "stream").Bool() {
			return fmt.Errorf("request %d did not ask for a stream: %s", i+1, raw)
		}
	}
	return nil
}

func (s *streamToggleState) theAnswerArrivedAsASingleTextChunk() error {
	var texts []string
	for _, c := range s.chunks {
		if c.TextDelta != "" {
			texts = append(texts, c.TextDelta)
		}
	}
	if len(texts) != 1 {
		return fmt.Errorf("text chunks = %d (%q), want exactly one", len(texts), texts)
	}
	if s.resp != nil && texts[0] != s.resp.Content {
		return fmt.Errorf("the single chunk %q does not carry the whole answer %q", texts[0], s.resp.Content)
	}
	return nil
}

func (s *streamToggleState) theReasoningIsDeliveredBeforeTheAnswer(want string) error {
	reasoningAt, textAt := -1, -1
	var reasoning strings.Builder
	for i, c := range s.chunks {
		if c.ReasoningDelta != "" {
			reasoning.WriteString(c.ReasoningDelta)
			if reasoningAt < 0 {
				reasoningAt = i
			}
		}
		if c.TextDelta != "" && textAt < 0 {
			textAt = i
		}
	}
	if reasoning.String() != want {
		return fmt.Errorf("reasoning = %q, want %q", reasoning.String(), want)
	}
	if reasoningAt < 0 || textAt < 0 || reasoningAt > textAt {
		return fmt.Errorf("reasoning chunk at %d, text chunk at %d: reasoning must come first", reasoningAt, textAt)
	}
	if s.resp == nil || s.resp.Reasoning != want {
		return fmt.Errorf("response reasoning = %q, want %q", reasoningOf(s.resp), want)
	}
	return nil
}

func (s *streamToggleState) theCallSucceedsWithAToolCallToggle(name, args string) error {
	if s.callErr != nil {
		return fmt.Errorf("provider call failed: %v", s.callErr)
	}
	if s.resp == nil || len(s.resp.ToolCalls) != 1 {
		return fmt.Errorf("tool calls = %+v, want exactly one", toolCallsOf(s.resp))
	}
	tc := s.resp.ToolCalls[0]
	if tc.Name != name || !jsonEquivalent(tc.InputJSON, args) {
		return fmt.Errorf("tool call = %q(%s), want %q(%s)", tc.Name, tc.InputJSON, name, args)
	}
	// The complete call must also have reached the caller as a chunk, so the ACP
	// tool-call update fires exactly as it does mid-stream.
	for _, c := range s.chunks {
		if c.ToolCall != nil && c.ToolCall.Name == name {
			return nil
		}
	}
	return fmt.Errorf("no tool call chunk was delivered for %q", name)
}

func (s *streamToggleState) theStopReasonIsToggle(want string) error {
	if s.resp == nil {
		return fmt.Errorf("no response (error: %v)", s.callErr)
	}
	if s.resp.StopReason != want {
		return fmt.Errorf("stop reason = %q, want %q", s.resp.StopReason, want)
	}
	return nil
}

func reasoningOf(r *Response) string {
	if r == nil {
		return "<nil>"
	}
	return r.Reasoning
}

// jsonEquivalent compares two JSON documents by value, so the feature file can
// spell arguments readably without matching the provider's byte-for-byte output.
func jsonEquivalent(a, b string) bool {
	var av, bv interface{}
	if json.Unmarshal([]byte(a), &av) != nil || json.Unmarshal([]byte(b), &bv) != nil {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return fmt.Sprintf("%v", av) == fmt.Sprintf("%v", bv)
}

func initializeStreamToggleScenario(sc *godog.ScenarioContext) {
	s := &streamToggleState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.cleanup()
		return ctx, nil
	})

	sc.Step(`^an "openai" provider with streaming disabled pointed at a stub server (that refuses streaming requests|that answers with reasoning|that answers with a tool call)$`, s.aBlockingProviderPointedAtStub)
	sc.Step(`^an "openai" provider with the stream key omitted pointed at a stub server that streams a completion$`, s.aStreamingProviderPointedAtStub)
	sc.Step(`^a streaming completion is requested$`, s.aStreamingCompletionIsRequested)
	sc.Step(`^the call succeeds with text "([^"]*)"$`, s.theCallSucceedsWithTextToggle)
	sc.Step(`^the stub server received (\d+) requests? with "stream" unset$`, s.theStubReceivedRequestsWithStreamUnset)
	sc.Step(`^the stub server received (\d+) requests? with "stream" set to true$`, s.theStubReceivedRequestsWithStreamTrue)
	sc.Step(`^the answer arrived as a single text chunk$`, s.theAnswerArrivedAsASingleTextChunk)
	sc.Step(`^the reasoning delta "([^"]*)" is delivered before the answer text$`, s.theReasoningIsDeliveredBeforeTheAnswer)
	sc.Step(`^the call succeeds with a "([^"]*)" tool call with arguments (.+)$`, s.theCallSucceedsWithAToolCallToggle)
	sc.Step(`^the stop reason is "([^"]*)"$`, s.theStopReasonIsToggle)
}

func TestModelStreamToggleFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "model-stream-toggle",
		ScenarioInitializer: initializeStreamToggleScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/model_stream_toggle.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("model stream toggle feature suite failed")
	}
}
