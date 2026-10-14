//go:build http

package httpserver

// Godog harness for features/aggregator_reasoning.feature: a full turn over
// POST /v1/responses with the real agent runner and the real openai provider
// pointed at a stub of an OpenRouter-style aggregator - a model listing that
// names each model's supported_parameters, and a chat endpoint that streams
// reasoning in delta.reasoning and reports usage reasoning_tokens.

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
	aggregatorReasoning       = "Weighing the request."
	aggregatorAnswer          = "Here it is."
	aggregatorReasoningTokens = 42
	aggregatorSessionID       = "sess_aggregator_reasoning"
)

// aggregatorBackend answers GET /models and POST /chat/completions the way
// OpenRouter and RouterAI do, and keeps the chat requests it was sent.
type aggregatorBackend struct {
	model  string
	params []string
	mu     sync.Mutex
	chats  [][]byte
}

func (b *aggregatorBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"id": b.model, "name": "Qwen: Qwen3.8 Flash", "context_length": 1000000,
			"supported_parameters": b.params,
		}}})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
		raw, _ := io.ReadAll(r.Body)
		b.mu.Lock()
		b.chats = append(b.chats, raw)
		b.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta map[string]any, finish any) string {
			line, _ := json.Marshal(map[string]any{
				"id": "gen-1", "object": "chat.completion.chunk", "created": 1786903885, "model": b.model,
				"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
			})
			return "data: " + string(line) + "\n\n"
		}
		frames := []string{
			chunk(map[string]any{"role": "assistant", "content": "", "reasoning": "Weighing "}, nil),
			chunk(map[string]any{"reasoning": "the request."}, nil),
			chunk(map[string]any{"content": aggregatorAnswer}, nil),
			chunk(map[string]any{}, "stop"),
			fmt.Sprintf(`data: {"id":"gen-1","object":"chat.completion.chunk","created":1786903885,"model":%q,"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":50,"total_tokens":170,"completion_tokens_details":{"reasoning_tokens":%d}}}`+"\n\n", b.model, aggregatorReasoningTokens),
			"data: [DONE]\n\n",
		}
		for _, f := range frames {
			_, _ = io.WriteString(w, f)
		}
	default:
		http.NotFound(w, r)
	}
}

type aggregatorReasoningState struct {
	root      string
	backend   *aggregatorBackend
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	modelRef  string
	models    []byte
	frames    []sseFrame
	body      string
}

func (s *aggregatorReasoningState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-aggregator-*")
	if err != nil {
		return err
	}
	s.root = root
	s.models, s.frames, s.body = nil, nil, ""
	return nil
}

func (s *aggregatorReasoningState) close() {
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

func (s *aggregatorReasoningState) anAggregatorServing(model string) error {
	s.backend = &aggregatorBackend{
		model:  model,
		params: []string{"include_reasoning", "max_tokens", "reasoning", "response_format", "temperature", "tool_choice", "tools"},
	}
	s.backendTS = httptest.NewServer(s.backend)
	return nil
}

func (s *aggregatorReasoningState) aCoddyServerOnTheAggregator(modelRef string) error {
	home := filepath.Join(s.root, "home")
	cwd := filepath.Join(s.root, "workspace")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	provider, _, err := config.SplitModelRef(modelRef)
	if err != nil {
		return err
	}
	s.modelRef = modelRef
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: provider, Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: modelRef}},
		Agent:     config.Agent{Model: modelRef},
	}
	cfg.Tools.PermissionMode = config.PermModeBypass
	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(s.root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, cwd, store)
	s.srv = New(cfg, mgr, log, cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *aggregatorReasoningState) aClientListsTheModels() error {
	res, err := http.Get(s.ts.URL + "/v1/models")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	s.models, _ = io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /v1/models status %d: %s", res.StatusCode, s.models)
	}
	return nil
}

func (s *aggregatorReasoningState) modelOffersTheLevels(model, levels string) error {
	for _, row := range gjson.GetBytes(s.models, "data").Array() {
		if row.Get("id").String() != model {
			continue
		}
		var got []string
		for _, lv := range row.Get("reasoning_levels").Array() {
			got = append(got, lv.String())
		}
		if strings.Join(got, ",") != levels {
			return fmt.Errorf("%s offers %v, want %s", model, got, levels)
		}
		return nil
	}
	return fmt.Errorf("%s is not listed: %s", model, s.models)
}

func (s *aggregatorReasoningState) aCoddyClientStreamsATurnAt(level string) error {
	body := fmt.Sprintf(`{"model":"agent","input":"hello","stream":true,"metadata":{"model":%q,"reasoning":%q}}`, s.modelRef, level)
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", aggregatorSessionID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("POST /v1/responses status %d: %s", res.StatusCode, raw)
	}
	s.body = string(raw)
	s.frames = parseSSEFrames(s.body)
	if len(s.frames) == 0 || s.frames[len(s.frames)-1].data != "[DONE]" {
		return fmt.Errorf("stream was not terminated with [DONE]: %s", s.body)
	}
	return nil
}

func (s *aggregatorReasoningState) theAggregatorWasAskedForEffort(effort string) error {
	s.backend.mu.Lock()
	defer s.backend.mu.Unlock()
	if len(s.backend.chats) == 0 {
		return fmt.Errorf("the aggregator was never asked")
	}
	req := s.backend.chats[0]
	if got := gjson.GetBytes(req, "reasoning.effort").String(); got != effort {
		return fmt.Errorf("reasoning.effort = %q, want %q: %s", got, effort, req)
	}
	for _, field := range []string{"reasoning_effort", "chat_template_kwargs"} {
		if gjson.GetBytes(req, field).Exists() {
			return fmt.Errorf("the request carries %s, which the listing does not name: %s", field, req)
		}
	}
	return nil
}

func (s *aggregatorReasoningState) theStreamCarriesTheReasoning(want string) error {
	var got strings.Builder
	for _, f := range s.frames {
		if f.event == "" && f.data != "[DONE]" {
			got.WriteString(gjson.Get(f.data, "choices.0.delta.reasoning_content").String())
		}
	}
	if got.String() != want {
		return fmt.Errorf("streamed reasoning = %q, want %q; stream:\n%s", got.String(), want, s.body)
	}
	return nil
}

func (s *aggregatorReasoningState) tokenUsageReportsReasoningTokens(want int) error {
	for _, f := range s.frames {
		if f.event == "token_usage" && int(gjson.Get(f.data, "reasoningTokens").Int()) == want {
			return nil
		}
	}
	return fmt.Errorf("no token_usage frame reports %d reasoning tokens; stream:\n%s", want, s.body)
}

func (s *aggregatorReasoningState) historyKeepsTheReasoning(want string, tokens int) error {
	res, err := http.Get(s.ts.URL + "/coddy/sessions/" + aggregatorSessionID + "/messages")
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET messages status %d: %s", res.StatusCode, raw)
	}
	for _, m := range gjson.GetBytes(raw, "messages").Array() {
		if m.Get("role").String() != "assistant" {
			continue
		}
		if m.Get("reasoning").String() == want && int(m.Get("reasoning_tokens").Int()) == tokens {
			return nil
		}
	}
	return fmt.Errorf("no assistant message keeps %q with %d reasoning tokens: %s", want, tokens, raw)
}

func initializeAggregatorReasoningScenario(sc *godog.ScenarioContext) {
	s := &aggregatorReasoningState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^an aggregator serving "([^"]+)" that takes the reasoning object and not reasoning_effort$`, s.anAggregatorServing)
	sc.Step(`^a coddy server whose model "([^"]+)" runs on that aggregator$`, s.aCoddyServerOnTheAggregator)
	sc.Step(`^a client lists the models$`, s.aClientListsTheModels)
	sc.Step(`^"([^"]+)" offers the reasoning levels "([^"]+)"$`, s.modelOffersTheLevels)
	sc.Step(`^a coddy client streams a turn at reasoning level "([^"]+)"$`, s.aCoddyClientStreamsATurnAt)
	sc.Step(`^the aggregator was asked for reasoning with effort "([^"]+)" and without reasoning_effort$`, s.theAggregatorWasAskedForEffort)
	sc.Step(`^the stream carries the reasoning "([^"]+)" as reasoning_content$`, s.theStreamCarriesTheReasoning)
	sc.Step(`^the stream's token_usage reports (\d+) reasoning tokens$`, s.tokenUsageReportsReasoningTokens)
	sc.Step(`^the session history keeps the reasoning "([^"]+)" with (\d+) reasoning tokens$`, s.historyKeepsTheReasoning)
}

func TestAggregatorReasoning(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "aggregator-reasoning",
		ScenarioInitializer: initializeAggregatorReasoningScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/aggregator_reasoning.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("aggregator reasoning feature suite failed")
	}
}
