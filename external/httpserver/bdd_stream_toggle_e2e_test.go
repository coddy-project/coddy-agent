//go:build http

package httpserver

// Godog harness for features/model_stream_toggle_http.feature: a full turn over
// POST /v1/responses whose agent model is configured with stream: false and whose
// LLM is the REAL openai provider (llm.NewProvider, no injected fake) pointed at a
// stub OpenAI-compatible server that rejects any request asking for SSE. The stub
// records every request body, so the transport actually used on the wire is
// observable, while the client keeps asking for - and receiving - an SSE stream.

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

// streamToggleE2EFileToken is the payload of the workspace file the scripted model
// asks coddy to read with its own tool.
const streamToggleE2EFileToken = "STREAM-TOGGLE-E2E-OK"

// blockingOpenAIBackend stands in for an OpenAI-compatible server that cannot serve
// SSE - a llama.cpp or vLLM deployment behind a proxy that breaks event streams. It
// answers two blocking turns: the first calls coddy's "read" tool, the second answers
// with the tool result.
type blockingOpenAIBackend struct {
	readPath string

	mu       sync.Mutex
	requests []string
}

func (b *blockingOpenAIBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.requests = append(b.requests, string(raw))
	turn := len(b.requests)
	b.mu.Unlock()

	if gjson.GetBytes(raw, "stream").Bool() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"this server does not support streaming","code":400}}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if turn == 1 {
		args, _ := json.Marshal(map[string]string{"path": b.readPath})
		payload := map[string]any{
			"id": "chatcmpl-e2e-1", "object": "chat.completion", "model": "qwen3-1.7b",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "tool_calls",
				"message": map[string]any{
					"role":              "assistant",
					"content":           "",
					"reasoning_content": "The answer is in that file.",
					"tool_calls": []map[string]any{{
						"id": "call_read_1", "type": "function",
						"function": map[string]string{"name": "read", "arguments": string(args)},
					}},
				},
			}},
			"usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 12, "total_tokens": 42},
		}
		_ = json.NewEncoder(w).Encode(payload)
		return
	}

	answer := "The file says " + streamToggleE2EFileToken + "."
	payload := map[string]any{
		"id": "chatcmpl-e2e-2", "object": "chat.completion", "model": "qwen3-1.7b",
		"choices": []map[string]any{{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": answer},
		}},
		"usage": map[string]int{"prompt_tokens": 60, "completion_tokens": 9, "total_tokens": 69},
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func (b *blockingOpenAIBackend) snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.requests...)
}

type streamToggleE2EState struct {
	root      string
	cwd       string
	backend   *blockingOpenAIBackend
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	sid       string
	sseBody   string
}

func (s *streamToggleE2EState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-stream-toggle-*")
	if err != nil {
		return err
	}
	s.root = root
	s.sid = ""
	s.sseBody = ""
	return nil
}

func (s *streamToggleE2EState) close() {
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

// startServer boots the gateway with the REAL agent runner and the REAL provider
// factory, so the transport comes from configuration exactly as in production.
func (s *streamToggleE2EState) startServer() error {
	home := filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "workspace")
	for _, dir := range []string{home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	readPath := filepath.Join(s.cwd, "stream-toggle-e2e.txt")
	if err := os.WriteFile(readPath, []byte(streamToggleE2EFileToken+"\n"), 0o644); err != nil {
		return err
	}

	s.backend = &blockingOpenAIBackend{readPath: readPath}
	s.backendTS = httptest.NewServer(s.backend)

	streaming := false
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "local/qwen3-1.7b", Stream: &streaming}},
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

func (s *streamToggleE2EState) sendStreamingPrompt() error {
	s.sid = "sess_stream_toggle_1"
	body := `{"model":"agent","input":"read the workspace file and tell me what it says","stream":true}`
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d: %s", res.StatusCode, raw)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		return fmt.Errorf("Content-Type = %q, want an SSE stream", ct)
	}
	s.sseBody = string(raw)
	return nil
}

func (s *streamToggleE2EState) backendSawOnlyBlockingRequests() error {
	requests := s.backend.snapshot()
	if len(requests) < 2 {
		return fmt.Errorf("backend received %d requests, want the two turns of the ReAct loop", len(requests))
	}
	for i, raw := range requests {
		if gjson.Get(raw, "stream").Exists() {
			return fmt.Errorf("request %d asked for a stream: %s", i+1, gjson.Get(raw, "stream").Raw)
		}
		if gjson.Get(raw, "stream_options").Exists() {
			return fmt.Errorf("request %d sent stream_options on a blocking request", i+1)
		}
	}
	return nil
}

func (s *streamToggleE2EState) clientReceivedTheAnswerOverSSE() error {
	if !strings.Contains(s.sseBody, "data: [DONE]") {
		return fmt.Errorf("SSE body was not terminated: %s", s.sseBody)
	}
	var answer strings.Builder
	for _, block := range strings.Split(s.sseBody, "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			payload, ok := strings.CutPrefix(line, "data: ")
			if !ok || payload == "[DONE]" {
				continue
			}
			answer.WriteString(gjson.Get(payload, "choices.0.delta.content").String())
		}
	}
	if !strings.Contains(answer.String(), streamToggleE2EFileToken) {
		return fmt.Errorf("streamed answer %q lacks the tool result", answer.String())
	}
	return nil
}

func (s *streamToggleE2EState) toolRanAndReachedTheAnswer() error {
	if !strings.Contains(s.sseBody, `"read"`) {
		return fmt.Errorf("no read tool call was announced on the stream: %s", s.sseBody)
	}
	requests := s.backend.snapshot()
	last := requests[len(requests)-1]
	if !strings.Contains(last, streamToggleE2EFileToken) {
		return fmt.Errorf("the tool result never reached the model: %s", last)
	}
	return nil
}

func (s *streamToggleE2EState) transcriptEndsWithThatAssistantMessage() error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + s.sid + "/messages")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("messages status %d: %s", res.StatusCode, raw)
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	for i := len(body.Messages) - 1; i >= 0; i-- {
		m := body.Messages[i]
		if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
			if !strings.Contains(m.Content, streamToggleE2EFileToken) {
				return fmt.Errorf("final assistant message %q lacks the tool result", m.Content)
			}
			return nil
		}
	}
	return fmt.Errorf("no assistant message in transcript: %s", raw)
}

func initializeStreamToggleE2EScenario(sc *godog.ScenarioContext) {
	s := &streamToggleE2EState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy gateway whose agent model is backed by a stub server that refuses streaming requests$`, s.startServer)
	sc.Step(`^a client sends a streaming prompt over POST /v1/responses$`, s.sendStreamingPrompt)
	sc.Step(`^the stub server received only non-streaming chat completion requests$`, s.backendSawOnlyBlockingRequests)
	sc.Step(`^the client received the answer over SSE$`, s.clientReceivedTheAnswerOverSSE)
	sc.Step(`^the workspace tool call ran and its result reached the final assistant message$`, s.toolRanAndReachedTheAnswer)
	sc.Step(`^the session transcript ends with that assistant message$`, s.transcriptEndsWithThatAssistantMessage)
}

func TestModelStreamToggleHTTPE2E(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "model-stream-toggle-http",
		ScenarioInitializer: initializeStreamToggleE2EScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/model_stream_toggle_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("model stream toggle HTTP feature suite failed")
	}
}
