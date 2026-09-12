//go:build http

package httpserver

// Godog harness for features/openai_passthrough.feature: POST /v1/chat/completions
// against the REAL openai provider pointed at a stub server that records every
// request body, so what reached the provider - the client's tools, its tool
// result, its image - is asserted on the wire, and what came back is read the
// way an OpenAI client reads it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	passthroughImageDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	passthroughFinalAnswer  = "It is 18°C in Paris."
	passthroughImageAnswer  = "A red square."
	passthroughPlainAnswer  = "Hi."
)

// recordingOpenAIBackend is an OpenAI-compatible server that keeps every request
// body and answers by what the request carried: a tool result gets the final
// answer, an offered get_weather tool gets a call to it, an image gets a
// description, anything else a greeting.
type recordingOpenAIBackend struct {
	mu       sync.Mutex
	requests []string
}

func (b *recordingOpenAIBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.requests = append(b.requests, string(raw))
	b.mu.Unlock()

	hasToolResult := false
	hasImage := false
	for _, m := range gjson.GetBytes(raw, "messages").Array() {
		if m.Get("role").String() == "tool" {
			hasToolResult = true
		}
		for _, part := range m.Get("content").Array() {
			if part.Get("type").String() == "image_url" {
				hasImage = true
			}
		}
	}
	offersWeather := false
	for _, t := range gjson.GetBytes(raw, "tools").Array() {
		if t.Get("function.name").String() == "get_weather" {
			offersWeather = true
		}
	}

	var text string
	var toolCall map[string]any
	switch {
	case hasToolResult:
		text = passthroughFinalAnswer
	case offersWeather:
		toolCall = map[string]any{
			"index": 0, "id": "call_weather_1", "type": "function",
			"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Paris"}`},
		}
	case hasImage:
		text = passthroughImageAnswer
	default:
		text = passthroughPlainAnswer
	}

	if !gjson.GetBytes(raw, "stream").Bool() {
		message := map[string]any{"role": "assistant", "content": text}
		finish := "stop"
		if toolCall != nil {
			message["content"] = nil
			message["tool_calls"] = []any{toolCall}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-rec", "object": "chat.completion", "model": "qwen3-1.7b",
			"choices": []map[string]any{{"index": 0, "finish_reason": finish, "message": message}},
			"usage":   map[string]int{"prompt_tokens": 12, "completion_tokens": 5, "total_tokens": 17},
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta map[string]any, finish any) string {
		line, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-rec", "object": "chat.completion.chunk", "created": 1786903885, "model": "qwen3-1.7b",
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		return "data: " + string(line) + "\n\n"
	}
	frames := []string{chunk(map[string]any{"role": "assistant", "content": ""}, nil)}
	if toolCall != nil {
		frames = append(frames,
			chunk(map[string]any{"tool_calls": []any{toolCall}}, nil),
			chunk(map[string]any{}, "tool_calls"))
	} else {
		for _, piece := range strings.SplitAfter(text, " ") {
			frames = append(frames, chunk(map[string]any{"content": piece}, nil))
		}
		frames = append(frames, chunk(map[string]any{}, "stop"))
	}
	frames = append(frames, "data: [DONE]\n\n")
	for _, f := range frames {
		_, _ = io.WriteString(w, f)
	}
}

func (b *recordingOpenAIBackend) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.requests) == 0 {
		return ""
	}
	return b.requests[len(b.requests)-1]
}

type openAIPassthroughState struct {
	root      string
	cwd       string
	backend   *recordingOpenAIBackend
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	sseBody   string
	frames    []sseFrame
	jsonBody  string
}

func (s *openAIPassthroughState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-openai-passthrough-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sseBody, s.jsonBody = "", ""
	s.frames = nil
	return nil
}

func (s *openAIPassthroughState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.backendTS != nil {
		s.backendTS.Close()
		s.backendTS = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *openAIPassthroughState) startServer() error {
	home := filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "workspace")
	for _, dir := range []string{home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.backend = &recordingOpenAIBackend{}
	s.backendTS = httptest.NewServer(s.backend)
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "local/qwen3-1.7b", Multimodal: true}},
		Agent:     config.Agent{Model: "local/qwen3-1.7b"},
	}
	cfg.Tools.PermissionMode = config.PermModeBypass
	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(s.root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, s.cwd, store)
	s.srv = New(cfg, mgr, log, s.cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

const passthroughWeatherTool = `{"type":"function","function":{"name":"get_weather","description":"Current weather in a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}`

func (s *openAIPassthroughState) post(body string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/chat/completions", bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("POST /v1/chat/completions status %d: %s", res.StatusCode, raw)
	}
	return res, raw, nil
}

func (s *openAIPassthroughState) stream(body string) error {
	res, raw, err := s.post(body)
	if err != nil {
		return err
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		return fmt.Errorf("Content-Type = %q, want an SSE stream", ct)
	}
	s.sseBody = string(raw)
	s.frames = parseSSEFrames(s.sseBody)
	if len(s.frames) == 0 || s.frames[len(s.frames)-1].data != "[DONE]" {
		return fmt.Errorf("stream was not terminated with [DONE]: %s", s.sseBody)
	}
	return nil
}

func (s *openAIPassthroughState) streamsWithTool(model, tool string) error {
	return s.stream(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"weather in Paris?"}],"tools":[%s],"stream":true}`,
		model, strings.Replace(passthroughWeatherTool, "get_weather", tool, 1)))
}

func (s *openAIPassthroughState) streamsWithToolResult(model, result, callID string) error {
	return s.stream(fmt.Sprintf(`{"model":%q,"messages":[
		{"role":"user","content":"weather in Paris?"},
		{"role":"assistant","content":null,"tool_calls":[{"id":%q,"type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},
		{"role":"tool","tool_call_id":%q,"content":%q}
	],"tools":[%s],"stream":true}`, model, callID, callID, result, passthroughWeatherTool))
}

func (s *openAIPassthroughState) postsWithToolWithoutStreaming(model, tool string) error {
	res, raw, err := s.post(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"weather in Paris?"}],"tools":[%s],"stream":false}`,
		model, strings.Replace(passthroughWeatherTool, "get_weather", tool, 1)))
	if err != nil {
		return err
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return fmt.Errorf("Content-Type = %q, want JSON", ct)
	}
	s.jsonBody = string(raw)
	return nil
}

func (s *openAIPassthroughState) streamsWithTextAndImage(model string) error {
	return s.stream(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":[
		{"type":"text","text":"What is on the picture?"},
		{"type":"image_url","image_url":{"url":%q}}
	]}],"stream":true}`, model, passthroughImageDataURL))
}

func (s *openAIPassthroughState) upstreamOfferedTheTool(name string) error {
	last := s.backend.last()
	for _, t := range gjson.Get(last, "tools").Array() {
		if t.Get("function.name").String() == name {
			if !t.Get("function.parameters.properties.city").Exists() {
				return fmt.Errorf("the tool reached the provider without its schema: %s", t.Raw)
			}
			return nil
		}
	}
	return fmt.Errorf("the provider was not offered %q; tools = %s", name, gjson.Get(last, "tools").Raw)
}

func (s *openAIPassthroughState) upstreamOfferedCoddysToolsNot(name string) error {
	last := s.backend.last()
	tools := gjson.Get(last, "tools").Array()
	if len(tools) == 0 {
		return fmt.Errorf("the agent offered no tools at all: %s", last)
	}
	for _, t := range tools {
		if t.Get("function.name").String() == name {
			return fmt.Errorf("the client's tool %q leaked into the agent's tool set", name)
		}
	}
	for _, t := range tools {
		if t.Get("function.name").String() == "read" {
			return nil
		}
	}
	return fmt.Errorf("coddy's own read tool is missing from the agent's tool set: %s", gjson.Get(last, "tools").Raw)
}

func (s *openAIPassthroughState) upstreamCarriedToolResult(result, callID string) error {
	last := s.backend.last()
	for _, m := range gjson.Get(last, "messages").Array() {
		if m.Get("role").String() == "tool" && m.Get("tool_call_id").String() == callID && strings.Contains(m.Get("content").String(), result) {
			return nil
		}
	}
	for _, m := range gjson.Get(last, "messages").Array() {
		if m.Get("role").String() == "assistant" && m.Get("tool_calls.0.id").String() == callID {
			return fmt.Errorf("the assistant's call is there but no tool result under %q: %s", callID, gjson.Get(last, "messages").Raw)
		}
	}
	return fmt.Errorf("neither the call nor the result reached the provider: %s", gjson.Get(last, "messages").Raw)
}

func (s *openAIPassthroughState) upstreamCarriedTheImage() error {
	last := s.backend.last()
	for _, m := range gjson.Get(last, "messages").Array() {
		if m.Get("role").String() != "user" {
			continue
		}
		if !m.Get("content").IsArray() {
			return fmt.Errorf("the user message reached the provider as a string, not as parts: %s", m.Raw)
		}
		for _, part := range m.Get("content").Array() {
			if part.Get("type").String() == "image_url" && part.Get("image_url.url").String() == passthroughImageDataURL {
				return nil
			}
		}
	}
	return fmt.Errorf("no image_url part reached the provider: %s", gjson.Get(last, "messages").Raw)
}

// chunks returns the data frames an OpenAI client treats as chat.completion.chunk.
func (s *openAIPassthroughState) chunks() []string {
	var out []string
	for _, f := range s.frames {
		if f.event == "" && f.data != "[DONE]" {
			out = append(out, f.data)
		}
	}
	return out
}

func (s *openAIPassthroughState) toolCallsDelta(name, args string) error {
	for _, c := range s.chunks() {
		for _, tc := range gjson.Get(c, "choices.0.delta.tool_calls").Array() {
			if tc.Get("function.name").String() == name {
				if got := tc.Get("function.arguments").String(); got != args {
					return fmt.Errorf("tool call arguments = %s, want %s", got, args)
				}
				if tc.Get("id").String() == "" || tc.Get("type").String() != "function" {
					return fmt.Errorf("tool call delta lacks id or type: %s", tc.Raw)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("no tool_calls delta for %q on the stream:\n%s", name, s.sseBody)
}

func (s *openAIPassthroughState) everyChunkCarriesFinishReason() error {
	for _, c := range s.chunks() {
		if gjson.Get(c, "choices.#").Int() > 0 && !gjson.Get(c, "choices.0.finish_reason").Exists() {
			return fmt.Errorf("chunk without finish_reason: %s", c)
		}
	}
	return nil
}

func (s *openAIPassthroughState) lastChunkFinishesWith(reason string) error {
	chunks := s.chunks()
	for i := len(chunks) - 1; i >= 0; i-- {
		if gjson.Get(chunks[i], "choices.#").Int() == 0 {
			continue
		}
		if got := gjson.Get(chunks[i], "choices.0.finish_reason").String(); got != reason {
			return fmt.Errorf("last choice finish_reason = %q, want %q: %s", got, reason, chunks[i])
		}
		return nil
	}
	return fmt.Errorf("no chunk with a choice on the stream: %s", s.sseBody)
}

func (s *openAIPassthroughState) clientAssembles(want string) error {
	var answer strings.Builder
	for _, c := range s.chunks() {
		answer.WriteString(gjson.Get(c, "choices.0.delta.content").String())
	}
	if answer.String() != want {
		return fmt.Errorf("client assembled %q, want %q; stream:\n%s", answer.String(), want, s.sseBody)
	}
	return nil
}

func (s *openAIPassthroughState) jsonAnswerCalls(name, args string) error {
	for _, tc := range gjson.Get(s.jsonBody, "choices.0.message.tool_calls").Array() {
		if tc.Get("function.name").String() == name {
			if got := tc.Get("function.arguments").String(); got != args {
				return fmt.Errorf("tool call arguments = %s, want %s", got, args)
			}
			return nil
		}
	}
	return fmt.Errorf("no tool call for %q in the JSON answer: %s", name, s.jsonBody)
}

func (s *openAIPassthroughState) jsonAnswerFinishesWith(reason string) error {
	if got := gjson.Get(s.jsonBody, "choices.0.finish_reason").String(); got != reason {
		return fmt.Errorf("finish_reason = %q, want %q: %s", got, reason, s.jsonBody)
	}
	return nil
}

func initializeOpenAIPassthroughScenario(sc *godog.ScenarioContext) {
	s := &openAIPassthroughState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy server whose direct model is a recording stub with tool calling$`, s.startServer)
	sc.Step(`^an OpenAI client streams "([^"]+)" with a "([^"]+)" tool$`, s.streamsWithTool)
	sc.Step(`^an OpenAI client streams "([^"]+)" with the tool result "([^"]+)" for call "([^"]+)"$`, s.streamsWithToolResult)
	sc.Step(`^an OpenAI client posts "([^"]+)" with a "([^"]+)" tool without streaming$`, s.postsWithToolWithoutStreaming)
	sc.Step(`^an OpenAI client streams "([^"]+)" with a text part and an image part$`, s.streamsWithTextAndImage)
	sc.Step(`^the upstream request offered the tool "([^"]+)"$`, s.upstreamOfferedTheTool)
	sc.Step(`^the upstream request offered coddy's own tools and not "([^"]+)"$`, s.upstreamOfferedCoddysToolsNot)
	sc.Step(`^the upstream request carried the tool result "([^"]+)" under call "([^"]+)"$`, s.upstreamCarriedToolResult)
	sc.Step(`^the upstream request carried the image as an image_url part$`, s.upstreamCarriedTheImage)
	sc.Step(`^a tool_calls delta calls "([^"]+)" with arguments (\{.*\})$`, s.toolCallsDelta)
	sc.Step(`^every chunk carries a finish_reason field$`, s.everyChunkCarriesFinishReason)
	sc.Step(`^the last chunk before \[DONE\] finishes with "([^"]+)"$`, s.lastChunkFinishesWith)
	sc.Step(`^the client assembles the answer "([^"]+)"$`, s.clientAssembles)
	sc.Step(`^the JSON answer calls "([^"]+)" with arguments (\{.*\})$`, s.jsonAnswerCalls)
	sc.Step(`^the JSON answer finishes with "([^"]+)"$`, s.jsonAnswerFinishesWith)
}

func TestOpenAIPassthrough(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "openai-passthrough",
		ScenarioInitializer: initializeOpenAIPassthroughScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/openai_passthrough.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("openai passthrough feature suite failed")
	}
}
