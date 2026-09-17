//go:build http

package httpserver

// Godog harness for features/direct_completion_options.feature: POST
// /v1/chat/completions against the REAL openai provider pointed at the
// recording stub of the passthrough harness, so the generation options are
// asserted on the request that left coddy, not on what coddy echoes back.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"
	"github.com/tidwall/gjson"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type directOptionsState struct {
	root      string
	backend   *recordingOpenAIBackend
	backendTS *httptest.Server
	srv       *Server
	ts        *httptest.Server
	pass      *openAIPassthroughState
}

func (s *directOptionsState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-direct-options-*")
	if err != nil {
		return err
	}
	s.root = root
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
}

func (s *directOptionsState) startServer(maxTokens int, temperature float64) error {
	home := filepath.Join(s.root, "home")
	cwd := filepath.Join(s.root, "workspace")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.backend = &recordingOpenAIBackend{}
	s.backendTS = httptest.NewServer(s.backend)
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: s.backendTS.URL, APIKey: "test-key"}},
		Models:    []config.ModelEntry{{Model: "local/llama-3.1-8b", MaxTokens: maxTokens, Temperature: temperature}},
		Agent:     config.Agent{Model: "local/llama-3.1-8b"},
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
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

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
