//go:build http

package httpserver

// Godog harness for features/direct_completion_options.feature: POST
// /v1/chat/completions against the REAL openai and codex providers pointed at
// recording stubs, so the generation options are asserted on the request that
// left coddy, not on what coddy echoes back.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// recordingCodexBackend stands in for the Codex Responses endpoint: it keeps
// every request body and answers with one short, completed response.
type recordingCodexBackend struct {
	mu       sync.Mutex
	requests []string
}

func (b *recordingCodexBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	b.mu.Lock()
	b.requests = append(b.requests, string(raw))
	b.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	codexSSE(w, "response.output_text.delta", map[string]any{"delta": "Hi."})
	codexSSE(w, "response.completed", map[string]any{
		"response": map[string]any{"usage": map[string]any{"input_tokens": 5, "output_tokens": 2}},
	})
}

func (b *recordingCodexBackend) last() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.requests) == 0 {
		return ""
	}
	return b.requests[len(b.requests)-1]
}

type directOptionsState struct {
	root      string
	backend   *recordingOpenAIBackend
	codex     *recordingCodexBackend
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	pass      *openAIPassthroughState
	jsonBody  string
}

func (s *directOptionsState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-direct-options-*")
	if err != nil {
		return err
	}
	s.root = root
	s.jsonBody = ""
	return nil
}

func (s *directOptionsState) close() {
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
	s.pass = nil
	s.codex = nil
}

func (s *directOptionsState) startServer(maxTokens int, temperature float64) error {
	s.backend = &recordingOpenAIBackend{}
	s.backendTS = httptest.NewServer(s.backend)
	return s.serve(
		[]config.ProviderConfig{{Name: "local", Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		config.ModelEntry{Model: "local/llama-3.1-8b", MaxTokens: maxTokens, Temperature: temperature})
}

func (s *directOptionsState) startReasoningServer(model, level string) error {
	s.backend = &recordingOpenAIBackend{}
	s.backendTS = httptest.NewServer(s.backend)
	return s.serve(
		[]config.ProviderConfig{{Name: "local", Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		config.ModelEntry{Model: model, ReasoningDefault: level})
}

// startCodexServer serves a codex model with a signed-in credential on disk and
// the Codex endpoint redirected to the recording stub.
func (s *directOptionsState) startCodexServer(model, level string) error {
	s.codex = &recordingCodexBackend{}
	s.backendTS = httptest.NewServer(s.codex)
	home := filepath.Join(s.root, "home")
	authPath := config.CodexAuthPath(home, "codex")
	if err := os.MkdirAll(filepath.Dir(authPath), 0o700); err != nil {
		return err
	}
	auth, _ := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]string{
			"access_token":  codexE2ETestJWT(map[string]any{"exp": 4_102_444_800}),
			"refresh_token": "rt",
			"account_id":    "acct-options",
		},
	})
	if err := os.WriteFile(authPath, auth, 0o600); err != nil {
		return err
	}
	if err := os.Setenv("CODDY_CODEX_BASE_URL", s.backendTS.URL); err != nil {
		return err
	}
	return s.serve([]config.ProviderConfig{{Name: "codex", Type: "codex"}},
		config.ModelEntry{Model: model, ReasoningDefault: level})
}

func (s *directOptionsState) serve(providers []config.ProviderConfig, model config.ModelEntry) error {
	home := filepath.Join(s.root, "home")
	cwd := filepath.Join(s.root, "workspace")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: providers,
		Models:    []config.ModelEntry{model},
		Agent:     config.Agent{Model: model.Model},
	}
	log := slog.Default()
	store := &session.FileStore{Root: filepath.Join(s.root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, nil, log, cwd, store)
	s.srv = New(cfg, mgr, log, cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	// The passthrough harness already knows how to post and read an OpenAI stream.
	s.pass = &openAIPassthroughState{ts: s.ts, backend: s.backend}
	return nil
}

func (s *directOptionsState) streamsWithOptions(model string, maxTokens int, temperature float64) error {
	return s.pass.stream(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"max_tokens":%d,"temperature":%g,"stream":true}`,
		model, maxTokens, temperature))
}

func (s *directOptionsState) streamsWithoutOptions(model string) error {
	return s.pass.stream(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"stream":true}`, model))
}

func (s *directOptionsState) streamsWithReasoning(model, level string) error {
	return s.pass.stream(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"reasoning_effort":%q,"stream":true}`, model, level))
}

func (s *directOptionsState) postsWithoutReasoning(model string) error {
	_, raw, err := s.pass.post(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"stream":false}`, model))
	if err != nil {
		return err
	}
	s.jsonBody = string(raw)
	return nil
}

// providerReceivedReasoning reads the level where each dialect carries it:
// reasoning_effort for OpenAI chat completions, reasoning.effort for Codex.
func (s *directOptionsState) providerReceivedReasoning(level string) error {
	if s.codex != nil {
		last := s.codex.last()
		if got := gjson.Get(last, "reasoning.effort").String(); got != level {
			return fmt.Errorf("Codex reasoning.effort = %q, want %q: %s", got, level, last)
		}
		return nil
	}
	last := s.backend.last()
	if got := gjson.Get(last, "reasoning_effort").String(); got != level {
		return fmt.Errorf("upstream reasoning_effort = %q, want %q: %s", got, level, last)
	}
	return nil
}

func (s *directOptionsState) jsonAnswerReportsReasoning(level string) error {
	if got := gjson.Get(s.jsonBody, "metadata.reasoning_effort").String(); got != level {
		return fmt.Errorf("metadata.reasoning_effort = %q, want %q: %s", got, level, s.jsonBody)
	}
	return nil
}

func (s *directOptionsState) upstreamCarried(maxTokens int, temperature float64) error {
	last := s.backend.last()
	if last == "" {
		return fmt.Errorf("no request reached the provider")
	}
	if got := gjson.Get(last, "max_tokens"); !got.Exists() || got.Int() != int64(maxTokens) {
		return fmt.Errorf("upstream max_tokens = %s, want %d: %s", got.Raw, maxTokens, last)
	}
	if got := gjson.Get(last, "temperature"); !got.Exists() || got.Float() != temperature {
		return fmt.Errorf("upstream temperature = %s, want %g: %s", got.Raw, temperature, last)
	}
	return nil
}

func (s *directOptionsState) modelStillConfigured(maxTokens int, temperature float64) error {
	ent := s.srv.activeCfg().FindModelEntry("local/llama-3.1-8b")
	if ent == nil {
		return fmt.Errorf("the model left the configuration")
	}
	if ent.MaxTokens != maxTokens || ent.Temperature != temperature {
		return fmt.Errorf("configuration now reads max_tokens %d and temperature %g, want %d and %g",
			ent.MaxTokens, ent.Temperature, maxTokens, temperature)
	}
	return nil
}

func initializeDirectOptionsScenario(sc *godog.ScenarioContext) {
	s := &directOptionsState{}
	var prevCodexBase string
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		prevCodexBase = os.Getenv("CODDY_CODEX_BASE_URL")
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		restoreEnv("CODDY_CODEX_BASE_URL", prevCodexBase)
		return ctx, nil
	})

	sc.Step(`^a coddy server whose direct model "([^"]+)" reasons at "([^"]+)" by default$`, s.startReasoningServer)
	sc.Step(`^a coddy server whose direct model "([^"]+)" is a codex model reasoning at "([^"]+)" by default$`, s.startCodexServer)
	sc.Step(`^an OpenAI client streams "([^"]+)" with reasoning_effort "([^"]+)"$`, s.streamsWithReasoning)
	sc.Step(`^an OpenAI client posts "([^"]+)" without reasoning_effort$`, s.postsWithoutReasoning)
	sc.Step(`^the provider received the reasoning level "([^"]+)"$`, s.providerReceivedReasoning)
	sc.Step(`^the JSON answer reports reasoning_effort "([^"]+)"$`, s.jsonAnswerReportsReasoning)
	sc.Step(`^a coddy server whose direct model is configured with max_tokens (\d+) and temperature ([\d.]+)$`, s.startServer)
	sc.Step(`^an OpenAI client streams "([^"]+)" with max_tokens (\d+) and temperature ([\d.]+)$`, s.streamsWithOptions)
	sc.Step(`^an OpenAI client streams "([^"]+)" without generation options$`, s.streamsWithoutOptions)
	sc.Step(`^the upstream request carried max_tokens (\d+) and temperature ([\d.]+)$`, s.upstreamCarried)
	sc.Step(`^the model is still configured with max_tokens (\d+) and temperature ([\d.]+)$`, s.modelStillConfigured)
}

func TestDirectCompletionOptions(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "direct-completion-options",
		ScenarioInitializer: initializeDirectOptionsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/direct_completion_options.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("direct completion options feature suite failed")
	}
}
