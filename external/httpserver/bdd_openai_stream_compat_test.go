//go:build http

package httpserver

// Godog harness for features/openai_stream_compat.feature: a full turn over
// POST /v1/chat/completions with the REAL agent runner and the REAL openai
// provider pointed at a stub server that streams a reasoning model's answer.
// The assertions read the SSE body the way a third-party OpenAI client does -
// VS Code Copilot's SSEProcessor, the openai SDKs - so the spec fails on
// exactly what those clients choke on.

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
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	openAICompatAnswer    = "Hi there."
	openAICompatReasoning = "Thinking it over."
)

// reasoningOpenAIBackend is an OpenAI-compatible server streaming the dialect of
// a reasoning model (vLLM, SGLang, llama.cpp): a role chunk, reasoning_content
// deltas, content deltas, the finish_reason chunk and a trailing usage chunk.
type reasoningOpenAIBackend struct{}

func (reasoningOpenAIBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	if !gjson.GetBytes(raw, "stream").Bool() {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-compat", "object": "chat.completion", "model": "qwen3-1.7b",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{
					"role": "assistant", "content": openAICompatAnswer,
					"reasoning_content": openAICompatReasoning,
				},
			}},
			"usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 5, "total_tokens": 17},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta map[string]any, finish any) string {
		line, _ := json.Marshal(map[string]any{
			"id": "chatcmpl-compat", "object": "chat.completion.chunk", "created": 1786903885,
			"model":   "qwen3-1.7b",
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		return "data: " + string(line) + "\n\n"
	}
	frames := []string{
		chunk(map[string]any{"role": "assistant", "content": ""}, nil),
		chunk(map[string]any{"reasoning_content": openAICompatReasoning}, nil),
	}
	for _, piece := range []string{"Hi", " there", "."} {
		frames = append(frames, chunk(map[string]any{"content": piece}, nil))
	}
	frames = append(frames,
		chunk(map[string]any{}, "stop"),
		`data: {"id":"chatcmpl-compat","object":"chat.completion.chunk","created":1786903885,"model":"qwen3-1.7b","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`+"\n\n",
		"data: [DONE]\n\n",
	)
	for _, f := range frames {
		_, _ = io.WriteString(w, f)
	}
}

// sseFrame is one server-sent event as a third-party parser sees it.
type sseFrame struct {
	event string
	data  string
}

// parseSSEFrames splits an SSE body into frames, keeping the event name (empty
// for the default "message" event) and the joined data payload.
func parseSSEFrames(body string) []sseFrame {
	var frames []sseFrame
	for _, block := range strings.Split(body, "\n\n") {
		var f sseFrame
		var data []string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, ":"):
				continue
			case strings.HasPrefix(line, "event:"):
				f.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if f.event == "" && len(data) == 0 {
			continue
		}
		f.data = strings.Join(data, "\n")
		frames = append(frames, f)
	}
	return frames
}

type openAIStreamCompatState struct {
	root      string
	cwd       string
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	sseBody   string
	frames    []sseFrame
}

func (s *openAIStreamCompatState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-openai-compat-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sseBody = ""
	s.frames = nil
	return nil
}

func (s *openAIStreamCompatState) close() {
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

func (s *openAIStreamCompatState) startServer() error {
	home := filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "workspace")
	for _, dir := range []string{home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.backendTS = httptest.NewServer(reasoningOpenAIBackend{})
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "local/qwen3-1.7b"}},
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

func (s *openAIStreamCompatState) stream(path, body string) error {
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+path, bytes.NewReader([]byte(body)))
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
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST %s status %d: %s", path, res.StatusCode, raw)
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

func (s *openAIStreamCompatState) openAIClientStreams(model string) error {
	return s.stream("/v1/chat/completions",
		fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"stream":true}`, model))
}

func (s *openAIStreamCompatState) openAIClientStreamsAskingForUsage(model string) error {
	return s.stream("/v1/chat/completions",
		fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"stream":true,"stream_options":{"include_usage":true}}`, model))
}

func (s *openAIStreamCompatState) coddyClientStreams(model string) error {
	return s.stream("/v1/responses",
		fmt.Sprintf(`{"model":%q,"input":"hello","stream":true}`, model))
}

// chunks returns the frames an OpenAI client would treat as chat.completion.chunk
// objects: the default-event data frames before [DONE].
func (s *openAIStreamCompatState) chunks() []string {
	var out []string
	for _, f := range s.frames {
		if f.event != "" || f.data == "[DONE]" {
			continue
		}
		out = append(out, f.data)
	}
	return out
}

func (s *openAIStreamCompatState) clientAssemblesTheAnswer(want string) error {
	var answer strings.Builder
	for _, c := range s.chunks() {
		if !gjson.Get(c, "choices").IsArray() {
			return fmt.Errorf("a data frame without choices reached the client: %s", c)
		}
		answer.WriteString(gjson.Get(c, "choices.0.delta.content").String())
	}
	if answer.String() != want {
		return fmt.Errorf("client assembled %q, want %q; stream:\n%s", answer.String(), want, s.sseBody)
	}
	return nil
}

func (s *openAIStreamCompatState) streamOpensWithAssistantRoleChunk() error {
	chunks := s.chunks()
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks on the stream: %s", s.sseBody)
	}
	if role := gjson.Get(chunks[0], "choices.0.delta.role").String(); role != "assistant" {
		return fmt.Errorf("first chunk delta.role = %q, want assistant: %s", role, chunks[0])
	}
	return nil
}

func (s *openAIStreamCompatState) everyChunkCarriesFinishReason() error {
	for _, c := range s.chunks() {
		if gjson.Get(c, "choices.#").Int() == 0 {
			continue // a usage-only chunk has no choice to finish
		}
		if !gjson.Get(c, "choices.0.finish_reason").Exists() {
			return fmt.Errorf("chunk without finish_reason: %s", c)
		}
	}
	return nil
}

func (s *openAIStreamCompatState) lastChunkBeforeDoneFinishesWith(reason string) error {
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

func (s *openAIStreamCompatState) exactlyOneChunkFinishesTheChoice() error {
	finished := 0
	for _, c := range s.chunks() {
		if gjson.Get(c, "choices.0.finish_reason").Type == gjson.String {
			finished++
		}
	}
	if finished != 1 {
		return fmt.Errorf("%d chunks carry a non-null finish_reason, want exactly one:\n%s", finished, s.sseBody)
	}
	return nil
}

func (s *openAIStreamCompatState) streamCarriesNoNamedEvents() error {
	for _, f := range s.frames {
		if f.event != "" {
			return fmt.Errorf("named event %q reached an OpenAI client: %s", f.event, s.sseBody)
		}
	}
	return nil
}

func (s *openAIStreamCompatState) streamCarriesNamedEvents() error {
	for _, f := range s.frames {
		if f.event != "" {
			return nil
		}
	}
	return fmt.Errorf("no named event on the coddy channel: %s", s.sseBody)
}

func (s *openAIStreamCompatState) streamEndsWithCoddyMeta() error {
	if len(s.frames) < 2 {
		return fmt.Errorf("stream too short: %s", s.sseBody)
	}
	if last := s.frames[len(s.frames)-2]; last.event != "coddy_meta" {
		return fmt.Errorf("frame before [DONE] is %q, want coddy_meta: %s", last.event, s.sseBody)
	}
	return nil
}

func (s *openAIStreamCompatState) reasoningSurvivesAsReasoningContent(want string) error {
	var reasoning strings.Builder
	for _, c := range s.chunks() {
		reasoning.WriteString(gjson.Get(c, "choices.0.delta.reasoning_content").String())
	}
	if reasoning.String() != want {
		return fmt.Errorf("reasoning_content assembled %q, want %q", reasoning.String(), want)
	}
	return nil
}

func (s *openAIStreamCompatState) lastChunkReportsUsage(input, output int) error {
	chunks := s.chunks()
	if len(chunks) == 0 {
		return fmt.Errorf("no chunks on the stream: %s", s.sseBody)
	}
	last := chunks[len(chunks)-1]
	got := gjson.Get(last, "usage")
	if !got.Exists() {
		return fmt.Errorf("last chunk carries no usage: %s", last)
	}
	if in, out := got.Get("prompt_tokens").Int(), got.Get("completion_tokens").Int(); in != int64(input) || out != int64(output) {
		return fmt.Errorf("usage = %s, want prompt %d completion %d", got.Raw, input, output)
	}
	if total := got.Get("total_tokens").Int(); total != int64(input+output) {
		return fmt.Errorf("total_tokens = %d, want %d", total, input+output)
	}
	return nil
}

func (s *openAIStreamCompatState) usageChunkCarriesEmptyChoices() error {
	chunks := s.chunks()
	last := chunks[len(chunks)-1]
	if !gjson.Get(last, "choices").IsArray() || gjson.Get(last, "choices.#").Int() != 0 {
		return fmt.Errorf("usage chunk choices = %s, want []", gjson.Get(last, "choices").Raw)
	}
	return nil
}

func initializeOpenAIStreamCompatScenario(sc *godog.ScenarioContext) {
	s := &openAIStreamCompatState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy server whose model streams reasoning before its answer$`, s.startServer)
	sc.Step(`^an OpenAI client streams "([^"]+)" over POST /v1/chat/completions$`, s.openAIClientStreams)
	sc.Step(`^an OpenAI client streams "([^"]+)" over POST /v1/chat/completions asking for usage$`, s.openAIClientStreamsAskingForUsage)
	sc.Step(`^a coddy client streams "([^"]+)" over POST /v1/responses$`, s.coddyClientStreams)
	sc.Step(`^the client assembles the answer "([^"]+)"$`, s.clientAssemblesTheAnswer)
	sc.Step(`^the stream opens with an assistant role chunk$`, s.streamOpensWithAssistantRoleChunk)
	sc.Step(`^every chunk carries a finish_reason field$`, s.everyChunkCarriesFinishReason)
	sc.Step(`^the last chunk before \[DONE\] finishes with "([^"]+)"$`, s.lastChunkBeforeDoneFinishesWith)
	sc.Step(`^exactly one chunk finishes the choice$`, s.exactlyOneChunkFinishesTheChoice)
	sc.Step(`^the stream carries no named SSE events$`, s.streamCarriesNoNamedEvents)
	sc.Step(`^the stream carries named SSE events$`, s.streamCarriesNamedEvents)
	sc.Step(`^the stream ends with coddy_meta before \[DONE\]$`, s.streamEndsWithCoddyMeta)
	sc.Step(`^the streamed reasoning "([^"]+)" survives as reasoning_content$`, s.reasoningSurvivesAsReasoningContent)
	sc.Step(`^the last chunk before \[DONE\] reports (\d+) input and (\d+) output tokens$`, s.lastChunkReportsUsage)
	sc.Step(`^that usage chunk carries an empty choices array$`, s.usageChunkCarriesEmptyChoices)
}

func TestOpenAIStreamCompat(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "openai-stream-compat",
		ScenarioInitializer: initializeOpenAIStreamCompatScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/openai_stream_compat.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("openai stream compat feature suite failed")
	}
}
