//go:build http

package httpserver

// Godog harness for features/openai_responses.feature: POST /v1/responses for a
// configured model, against the REAL openai provider pointed at the recording
// stub backend the passthrough suite uses, so the Responses request shape is
// asserted on the wire both ways - what reached the provider, and what the
// OpenAI client reads back.

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

// responsesRecordingBackend is the suite's stand-in OpenAI API: it records
// every request, answers "Hi." for a plain prompt, calls get_weather when the
// tool is offered and answers the tool result, and - unlike the passthrough
// stub - reports usage on streamed answers too, because a Responses completed
// event is asserted to carry it.
type responsesRecordingBackend struct {
	mu       sync.Mutex
	requests []string
}

func (b *responsesRecordingBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.requests = append(b.requests, string(raw))
	b.mu.Unlock()

	hasToolResult := false
	for _, m := range gjson.GetBytes(raw, "messages").Array() {
		if m.Get("role").String() == "tool" {
			hasToolResult = true
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
		text = "It is 18°C in Paris."
	case offersWeather:
		toolCall = map[string]any{
			"index": 0, "id": "call_weather_1", "type": "function",
			"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Paris"}`},
		}
	default:
		text = "Hi."
	}
	usage := map[string]int{"prompt_tokens": 12, "completion_tokens": 5, "total_tokens": 17}

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
			"usage":   usage,
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
	usageLine, _ := json.Marshal(map[string]any{
		"id": "chatcmpl-rec", "object": "chat.completion.chunk", "created": 1786903885, "model": "qwen3-1.7b",
		"choices": []any{}, "usage": usage,
	})
	frames = append(frames, "data: "+string(usageLine)+"\n\n", "data: [DONE]\n\n")
	for _, f := range frames {
		_, _ = io.WriteString(w, f)
	}
}

func (b *responsesRecordingBackend) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.requests) == 0 {
		return ""
	}
	return b.requests[len(b.requests)-1]
}

type openAIResponsesState struct {
	root      string
	cwd       string
	backend   *responsesRecordingBackend
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	sseBody   string
	frames    []sseFrame
	jsonBody  string
	status    int
}

func (s *openAIResponsesState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-openai-responses-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sseBody, s.jsonBody = "", ""
	s.frames = nil
	s.status = 0
	return nil
}

func (s *openAIResponsesState) close() {
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

func (s *openAIResponsesState) startServer() error {
	home := filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "workspace")
	for _, dir := range []string{home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.backend = &responsesRecordingBackend{}
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

func (s *openAIResponsesState) post(body string) error {
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	s.status = res.StatusCode
	if res.StatusCode != http.StatusOK {
		s.jsonBody = string(raw)
		return nil
	}
	if ct := res.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		s.sseBody = string(raw)
		s.frames = parseSSEFrames(s.sseBody)
		return nil
	}
	s.jsonBody = string(raw)
	return nil
}

func (s *openAIResponsesState) postsStringInput(model, input string) error {
	return s.post(fmt.Sprintf(`{"model":%q,"input":%q,"stream":false}`, model, input))
}

func (s *openAIResponsesState) streamsStringInput(model, input string) error {
	return s.post(fmt.Sprintf(`{"model":%q,"input":%q,"stream":true}`, model, input))
}

const responsesWeatherTool = `{"type":"function","name":"get_weather","description":"Current weather in a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]},"strict":false}`

func (s *openAIResponsesState) streamsWithTool(model, tool string) error {
	return s.post(fmt.Sprintf(`{"model":%q,"input":"weather in Paris?","tools":[%s],"stream":true}`,
		model, strings.Replace(responsesWeatherTool, "get_weather", tool, 1)))
}

func (s *openAIResponsesState) streamsToolResult() error {
	return s.post(fmt.Sprintf(`{"model":%q,"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"weather in Paris?"}]},
		{"type":"function_call","call_id":"call_weather_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"},
		{"type":"function_call_output","call_id":"call_weather_1","output":"It is 18°C in Paris."}
	],"stream":true}`, "local/qwen3-1.7b"))
}

func (s *openAIResponsesState) postsItemArray(model string) error {
	return s.post(fmt.Sprintf(`{"model":%q,"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"weather in Paris?"}]},
		{"type":"function_call","call_id":"call_weather_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"},
		{"type":"function_call_output","call_id":"call_weather_1","output":"sunny"}
	],"stream":false}`, model))
}

func (s *openAIResponsesState) postsPreviousResponse(model string) error {
	return s.post(fmt.Sprintf(`{"model":%q,"input":"hello","previous_response_id":"resp_abc","stream":false}`, model))
}

func (s *openAIResponsesState) streamsProfile(model string) error {
	return s.post(fmt.Sprintf(`{"model":%q,"input":"hello","stream":true}`, model))
}

func (s *openAIResponsesState) answerIsResponseObject(status string) error {
	if s.status != http.StatusOK {
		return fmt.Errorf("status %d, want 200: %s", s.status, s.jsonBody)
	}
	if got := gjson.Get(s.jsonBody, "object").String(); got != "response" {
		return fmt.Errorf("object = %q, want response: %s", got, s.jsonBody)
	}
	if id := gjson.Get(s.jsonBody, "id").String(); !strings.HasPrefix(id, "resp_") {
		return fmt.Errorf("id = %q, want resp_*: %s", id, s.jsonBody)
	}
	if got := gjson.Get(s.jsonBody, "status").String(); got != status {
		return fmt.Errorf("status = %q, want %q: %s", got, status, s.jsonBody)
	}
	return nil
}

func (s *openAIResponsesState) outputIsMessage(text string) error {
	items := gjson.Get(s.jsonBody, "output").Array()
	if len(items) != 1 {
		return fmt.Errorf("output has %d items, want 1: %s", len(items), s.jsonBody)
	}
	item := items[0]
	if item.Get("type").String() != "message" || item.Get("role").String() != "assistant" {
		return fmt.Errorf("output[0] is not an assistant message: %s", item.Raw)
	}
	parts := item.Get("content").Array()
	if len(parts) != 1 || parts[0].Get("type").String() != "output_text" || parts[0].Get("text").String() != text {
		return fmt.Errorf("output[0].content = %s, want one output_text %q", item.Get("content").Raw, text)
	}
	return nil
}

func (s *openAIResponsesState) responseCarriesUsage() error {
	u := gjson.Get(s.jsonBody, "usage")
	if !u.Exists() {
		return fmt.Errorf("no usage on the response: %s", s.jsonBody)
	}
	in, out, total := u.Get("input_tokens").Int(), u.Get("output_tokens").Int(), u.Get("total_tokens").Int()
	if total != in+out || total == 0 {
		return fmt.Errorf("usage counters = %d + %d = %d: %s", in, out, total, u.Raw)
	}
	return nil
}

func (s *openAIResponsesState) streamOpensWith(first, second string) error {
	if len(s.frames) < 2 || s.frames[0].event != first || s.frames[1].event != second {
		got := []string{}
		for i, f := range s.frames {
			if i > 1 {
				break
			}
			got = append(got, f.event)
		}
		return fmt.Errorf("stream opens with %v, want %s then %s:\n%s", got, first, second, s.sseBody)
	}
	for i, f := range s.frames {
		if gjson.Get(f.data, "sequence_number").Int() != int64(i) {
			return fmt.Errorf("frame %d sequence_number = %s, want %d: %s", i, gjson.Get(f.data, "sequence_number").Raw, i, f.data)
		}
	}
	return nil
}

func (s *openAIResponsesState) deltasAssemble(want string) error {
	var answer strings.Builder
	for _, f := range s.frames {
		if f.event == "response.output_text.delta" {
			answer.WriteString(gjson.Get(f.data, "delta").String())
		}
	}
	if answer.String() != want {
		return fmt.Errorf("deltas assemble %q, want %q; stream:\n%s", answer.String(), want, s.sseBody)
	}
	return nil
}

func (s *openAIResponsesState) streamEndsWithCompleted() error {
	if len(s.frames) == 0 || s.frames[len(s.frames)-1].event != "response.completed" {
		last := ""
		if len(s.frames) > 0 {
			last = s.frames[len(s.frames)-1].event
		}
		return fmt.Errorf("stream ends with %q, want response.completed:\n%s", last, s.sseBody)
	}
	resp := gjson.Get(s.frames[len(s.frames)-1].data, "response")
	if !resp.Exists() {
		return fmt.Errorf("response.completed has no response: %s", s.frames[len(s.frames)-1].data)
	}
	if got := resp.Get("status").String(); got != "completed" {
		return fmt.Errorf("completed response status = %q", got)
	}
	u := resp.Get("usage")
	if !u.Exists() || u.Get("total_tokens").Int() == 0 {
		return fmt.Errorf("completed response lacks usage: %s", resp.Raw)
	}
	for _, name := range []string{"response.output_item.added", "response.content_part.added", "response.output_text.done", "response.output_item.done"} {
		found := false
		for _, f := range s.frames {
			if f.event == name {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("stream is missing %s events:\n%s", name, s.sseBody)
		}
	}
	return nil
}

func (s *openAIResponsesState) streamHasNoForeignFrames() error {
	for _, f := range s.frames {
		if f.data == "[DONE]" {
			return fmt.Errorf("stream carries [DONE]:\n%s", s.sseBody)
		}
		if f.event == "coddy_meta" || strings.Contains(f.data, "coddy_meta") {
			return fmt.Errorf("stream carries coddy_meta:\n%s", s.sseBody)
		}
		if strings.Contains(f.data, "chat.completion.chunk") {
			return fmt.Errorf("stream carries a chat.completion.chunk:\n%s", f.data)
		}
		if !strings.HasPrefix(f.event, "response.") {
			return fmt.Errorf("stream carries a foreign event %q:\n%s", f.event, s.sseBody)
		}
	}
	return nil
}

func (s *openAIResponsesState) upstreamOfferedFlatTool(name string) error {
	last := s.backend.last()
	for _, t := range gjson.Get(last, "tools").Array() {
		if t.Get("function.name").String() == name {
			return nil
		}
	}
	return fmt.Errorf("the provider was not offered %q as a function tool; tools = %s", name, gjson.Get(last, "tools").Raw)
}

func (s *openAIResponsesState) streamCallsFunction(name string) error {
	var args strings.Builder
	for _, f := range s.frames {
		if f.event == "response.function_call_arguments.delta" {
			args.WriteString(gjson.Get(f.data, "delta").String())
		}
		if f.event == "response.output_item.done" && gjson.Get(f.data, "item.type").String() == "function_call" {
			if gjson.Get(f.data, "item.name").String() != name {
				continue
			}
			if gjson.Get(f.data, "item.call_id").String() == "" {
				return fmt.Errorf("function_call item has no call_id: %s", f.data)
			}
			const want = `{"city":"Paris"}`
			if args.String() != want || gjson.Get(f.data, "item.arguments").String() != want {
				return fmt.Errorf("function argument deltas %q, completed %q; want %q", args.String(), gjson.Get(f.data, "item.arguments").String(), want)
			}
			return nil
		}
	}
	return fmt.Errorf("no function_call output item for %q (args deltas: %q):\n%s", name, args.String(), s.sseBody)
}

func (s *openAIResponsesState) upstreamCarriedCallAndResult() error {
	last := s.backend.last()
	var call, result bool
	for _, m := range gjson.Get(last, "messages").Array() {
		if m.Get("role").String() == "assistant" {
			for _, tc := range m.Get("tool_calls").Array() {
				if tc.Get("id").String() == "call_weather_1" && tc.Get("function.name").String() == "get_weather" {
					call = true
				}
			}
		}
		if m.Get("role").String() == "tool" && m.Get("tool_call_id").String() == "call_weather_1" {
			result = true
		}
	}
	if !call || !result {
		return fmt.Errorf("call=%v result=%v, want both replayed: %s", call, result, gjson.Get(last, "messages").Raw)
	}
	return nil
}

func (s *openAIResponsesState) clientAssembles(want string) error {
	var answer strings.Builder
	for _, f := range s.frames {
		if f.event == "response.output_text.delta" {
			answer.WriteString(gjson.Get(f.data, "delta").String())
		}
	}
	if answer.String() != want {
		return fmt.Errorf("client assembled %q, want %q; stream:\n%s", answer.String(), want, s.sseBody)
	}
	return nil
}

func (s *openAIResponsesState) answerIs400For(field string) error {
	if s.status != http.StatusBadRequest {
		return fmt.Errorf("status %d, want 400: %s", s.status, s.jsonBody)
	}
	if !strings.Contains(s.jsonBody, field) {
		return fmt.Errorf("answer does not name %q: %s", field, s.jsonBody)
	}
	return nil
}

func (s *openAIResponsesState) streamCarriesCoddyDialect() error {
	var delta, done bool
	for _, f := range s.frames {
		if strings.Contains(f.data, "chat.completion.chunk") {
			delta = true
		}
		if f.data == "[DONE]" {
			done = true
		}
	}
	if !delta || !done {
		return fmt.Errorf("profile stream lost the coddy dialect (chunk=%v done=%v):\n%s", delta, done, s.sseBody)
	}
	return nil
}

func initializeOpenAIResponsesScenario(sc *godog.ScenarioContext) {
	s := &openAIResponsesState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy server whose direct model is a recording stub with tool calling$`, s.startServer)
	sc.Step(`^an OpenAI client posts "([^"]+)" to the Responses API with input "([^"]+)"$`, s.postsStringInput)
	sc.Step(`^an OpenAI client streams "([^"]+)" over the Responses API with input "([^"]+)"$`, s.streamsStringInput)
	sc.Step(`^an OpenAI client streams "([^"]+)" over the Responses API with a "([^"]+)" tool$`, s.streamsWithTool)
	sc.Step(`^the client streams the tool result for that call over the Responses API$`, s.streamsToolResult)
	sc.Step(`^an OpenAI client posts "([^"]+)" to the Responses API with a message, a function_call and a function_call_output$`, s.postsItemArray)
	sc.Step(`^an OpenAI client posts "([^"]+)" to the Responses API asking to continue a previous response$`, s.postsPreviousResponse)
	sc.Step(`^a coddy client streams "([^"]+)" over POST /v1/responses$`, s.streamsProfile)
	sc.Step(`^the answer is a response object with status "([^"]+)"$`, s.answerIsResponseObject)
	sc.Step(`^its output is a message item whose output_text is "([^"]+)"$`, s.outputIsMessage)
	sc.Step(`^the response carries usage counters$`, s.responseCarriesUsage)
	sc.Step(`^the stream opens with ([^ ]+) and ([^ ]+)$`, s.streamOpensWith)
	sc.Step(`^the output_text deltas assemble "([^"]+)"$`, s.deltasAssemble)
	sc.Step(`^the stream ends with response.completed carrying the response and its usage$`, s.streamEndsWithCompleted)
	sc.Step(`^the stream has no chat.completion.chunk, \[DONE\] or coddy_meta frame$`, s.streamHasNoForeignFrames)
	sc.Step(`^the upstream request offered the flat function tool "([^"]+)"$`, s.upstreamOfferedFlatTool)
	sc.Step(`^the stream answers with a function_call output item for "([^"]+)"$`, s.streamCallsFunction)
	sc.Step(`^the upstream request carried the prior call and its result$`, s.upstreamCarriedCallAndResult)
	sc.Step(`^the upstream request carried the assistant tool call and the tool result$`, s.upstreamCarriedCallAndResult)
	sc.Step(`^the client assembles the answer "([^"]+)"$`, s.clientAssembles)
	sc.Step(`^the answer is a 400 naming the unsupported field$`, func() error { return s.answerIs400For("previous_response_id") })
	sc.Step(`^the stream carries chat.completion.chunk deltas and \[DONE\]$`, s.streamCarriesCoddyDialect)
}

func TestOpenAIResponses(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "openai-responses",
		ScenarioInitializer: initializeOpenAIResponsesScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/openai_responses.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("openai responses feature suite failed")
	}
}
