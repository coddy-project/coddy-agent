//go:build http

package httpserver

// Godog harness for features/session_settings.feature: settings commands and
// the permission dialog's session switch over /v1/responses, with the real
// agent loop and one scripted provider per configured model, so a scenario
// sees which model answered a request.

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
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// settingsProvider answers a scripted list of responses, then plain text.
type settingsProvider struct {
	mu    sync.Mutex
	name  string
	steps []*llm.Response
	calls int
}

func (p *settingsProvider) Complete(ctx context.Context, m []llm.Message, d []llm.ToolDefinition) (*llm.Response, error) {
	return p.Stream(ctx, m, d, func(llm.StreamChunk) {})
}

func (p *settingsProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.mu.Lock()
	p.calls++
	var step *llm.Response
	if len(p.steps) > 0 {
		step, p.steps = p.steps[0], p.steps[1:]
	}
	p.mu.Unlock()
	if step == nil {
		text := "answered by " + p.name
		onChunk(llm.StreamChunk{TextDelta: text})
		return &llm.Response{Content: text, StopReason: "end_turn"}, nil
	}
	for i := range step.ToolCalls {
		tc := step.ToolCalls[i]
		onChunk(llm.StreamChunk{ToolCall: &tc})
	}
	return step, nil
}

type settingsFeatureState struct {
	root      string
	cfg       *config.Config
	ts        *httptest.Server
	srv       *Server
	mgr       *session.Manager
	sessionID string
	mu        sync.Mutex
	providers map[string]*settingsProvider
	answer    string
	prompts   int
	events    []sseFrame
	stopWatch context.CancelFunc
}

func (s *settingsFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-settings-*")
	if err != nil {
		return err
	}
	s.root = root
	s.providers = map[string]*settingsProvider{}
	s.answer, s.prompts, s.events = "", 0, nil
	return nil
}

func (s *settingsFeatureState) close() {
	if s.stopWatch != nil {
		s.stopWatch()
		s.stopWatch = nil
	}
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *settingsFeatureState) provider(apiModel string) *settingsProvider {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.providers[apiModel]
	if !ok {
		p = &settingsProvider{name: apiModel}
		s.providers[apiModel] = p
	}
	return p
}

func (s *settingsFeatureState) startServer(a, b string) error {
	home := filepath.Join(s.root, "home")
	cwd := filepath.Join(s.root, "cwd")
	for _, d := range []string{filepath.Join(home, "memory"), cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	s.cfg = &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd, ConfigPath: filepath.Join(home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: a, MaxTokens: 100}, {Model: b, MaxTokens: 100}},
		Agent:     config.Agent{Model: a, MaxTurns: 8},
	}
	s.cfg.Tools.PermissionMode = config.PermModeAsk
	s.cfg.Prompts.ApplyDefaults()
	factory := func(in llm.ProviderInput) (llm.Provider, error) { return s.provider(in.Model), nil }
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		ag := agent.NewAgent(s.cfg, st, snd, slog.Default())
		ag.SetSubagentRuntime(s.mgr)
		ag.SetProviderFactory(factory)
		return ag.Run(ctx, prompt)
	}
	s.mgr = session.NewManager(s.cfg, noopSender{}, runner, slog.Default(), cwd, &session.FileStore{Root: filepath.Join(s.root, "sessions")})
	s.srv = New(s.cfg, s.mgr, slog.Default(), cwd)
	s.srv.agentProviderFactory = factory
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *settingsFeatureState) newSession() error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cfg.Paths.CWD})
	if err != nil {
		return err
	}
	s.sessionID = res.SessionID
	return nil
}

func (s *settingsFeatureState) watchEvents() error {
	ctx, cancel := context.WithCancel(context.Background())
	s.stopWatch = cancel
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/coddy/events", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	ready := make(chan struct{})
	go func() {
		defer func() { _ = res.Body.Close() }()
		once := sync.Once{}
		readSSEStream(res.Body, func(f sseFrame) {
			once.Do(func() { close(ready) })
			s.mu.Lock()
			s.events = append(s.events, f)
			s.mu.Unlock()
		})
		once.Do(func() { close(ready) })
	}()
	// The stream opens with a ready frame; the scenario acts after it.
	<-ready
	return nil
}

// send posts a message as the browser does and keeps the answer text. With
// answerWith set it streams, answering every permission prompt with the first
// option and counting them.
func (s *settingsFeatureState) send(text, answerWith string) error {
	stream := answerWith != ""
	body, _ := json.Marshal(map[string]any{"model": "agent", "input": text, "stream": stream})
	req, err := http.NewRequest(http.MethodPost, s.ts.URL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", s.sessionID)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("POST /v1/responses: %s %s", res.Status, raw)
	}
	if !stream {
		var parsed struct {
			Output []struct {
				Text string `json:"text"`
			} `json:"output"`
		}
		if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
			return err
		}
		s.answer = ""
		for _, o := range parsed.Output {
			s.answer += o.Text
		}
		return nil
	}
	var answerErr error
	first := true
	readSSEStream(res.Body, func(f sseFrame) {
		if f.event != "permission" {
			return
		}
		s.prompts++
		var params acp.PermissionRequestParams
		if err := json.Unmarshal([]byte(f.data), &params); err != nil {
			answerErr = err
			return
		}
		option := "allow"
		if first {
			option = answerWith
			first = false
		}
		payload, _ := json.Marshal(map[string]string{"toolCallId": params.ToolCall.ToolCallID, "optionId": option})
		ans, err := http.Post(s.ts.URL+"/coddy/sessions/"+s.sessionID+"/permission", "application/json", bytes.NewReader(payload))
		if err != nil {
			answerErr = err
			return
		}
		_ = ans.Body.Close()
	})
	return answerErr
}

func (s *settingsFeatureState) userSends(text string) error { return s.send(text, "") }

func (s *settingsFeatureState) userSendsAndAnswers(text, option string) error {
	return s.send(text, option)
}

func (s *settingsFeatureState) answerSays(want string) error {
	if !strings.Contains(s.answer, want) {
		return fmt.Errorf("answer %q does not say %q", s.answer, want)
	}
	return nil
}

func (s *settingsFeatureState) sessionModelIs(want string) error {
	snap, err := s.mgr.SessionSettings(s.sessionID)
	if err != nil {
		return err
	}
	if snap.Model != want {
		return fmt.Errorf("session model = %q, want %q", snap.Model, want)
	}
	return nil
}

func (s *settingsFeatureState) noModelCalled() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, p := range s.providers {
		if p.calls > 0 {
			return fmt.Errorf("model %s answered %d requests", name, p.calls)
		}
	}
	return nil
}

// browserToldModel waits for the snapshot on the events stream: the answer to
// the command comes back on the POST, while the stream's reader appends its
// frame on its own goroutine, so the frame can land after the answer.
func (s *settingsFeatureState) browserToldModel(want string) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		told, seen := s.toldModel(want)
		if told {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no session_settings event named model %q among %d events", want, seen)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// toldModel reports whether a session_settings frame for this session names
// the model, and how many frames the stream has delivered so far.
func (s *settingsFeatureState) toldModel(want string) (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.events {
		if f.event != "session_settings" {
			continue
		}
		var body struct {
			SessionID string              `json:"sessionId"`
			Settings  acp.SessionSettings `json:"settings"`
		}
		if json.Unmarshal([]byte(f.data), &body) == nil && body.SessionID == s.sessionID && body.Settings.Model == want && body.Settings.Version > 0 {
			return true, len(s.events)
		}
	}
	return false, len(s.events)
}

func (s *settingsFeatureState) modelAnswered(name string, want int) error {
	if got := s.provider(name).calls; got != want {
		return fmt.Errorf("model %s answered %d requests, want %d", name, got, want)
	}
	return nil
}

func (s *settingsFeatureState) serverAsks() error {
	s.cfg.Tools.PermissionMode = config.PermModeAsk
	return nil
}

func (s *settingsFeatureState) serverBypasses() error {
	s.cfg.Tools.PermissionMode = config.PermModeBypass
	return nil
}

func (s *settingsFeatureState) modelRunsTwoCommands() error {
	s.provider("a").steps = []*llm.Response{{
		ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "run_command", InputJSON: `{"command":"echo first"}`},
			{ID: "c2", Name: "run_command", InputJSON: `{"command":"echo second"}`},
		},
		StopReason: "tool_use",
	}}
	return nil
}

func (s *settingsFeatureState) modelRunsOneCommand() error {
	s.provider("a").steps = []*llm.Response{{
		ToolCalls:  []llm.ToolCall{{ID: "c1", Name: "run_command", InputJSON: `{"command":"echo only"}`}},
		StopReason: "tool_use",
	}}
	return nil
}

func (s *settingsFeatureState) switchPermissionOverAPI(mode string) error {
	body, _ := json.Marshal(map[string]string{"permissionMode": mode})
	req, err := http.NewRequest(http.MethodPatch, s.ts.URL+"/coddy/sessions/"+s.sessionID, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("PATCH: %s %s", res.Status, raw)
	}
	var out struct {
		Settings acp.SessionSettings `json:"settings"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return err
	}
	if out.Settings.PermissionMode != mode || out.Settings.ConfiguredPermissionMode != s.cfg.Tools.ResolvedPermMode() {
		return fmt.Errorf("PATCH answered settings %+v", out.Settings)
	}
	return nil
}

func (s *settingsFeatureState) promptsShown(want int) error {
	if s.prompts != want {
		return fmt.Errorf("%d permission prompts were shown, want %d", s.prompts, want)
	}
	return nil
}

func (s *settingsFeatureState) sessionPermissionIs(want string) error {
	snap, err := s.mgr.SessionSettings(s.sessionID)
	if err != nil {
		return err
	}
	if snap.PermissionMode != want {
		return fmt.Errorf("session permission mode = %q, want %q", snap.PermissionMode, want)
	}
	return nil
}

func (s *settingsFeatureState) bothCommandsRan() error {
	ran := 0
	for _, m := range s.mgr.SessionByID(s.sessionID).GetMessages() {
		if m.Role == llm.RoleTool && (strings.Contains(m.Content, "first") || strings.Contains(m.Content, "second")) {
			ran++
		}
	}
	if ran != 2 {
		return fmt.Errorf("%d of the two commands ran", ran)
	}
	return nil
}

func initializeSessionSettingsScenario(sc *godog.ScenarioContext) {
	s := &settingsFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})
	sc.Step(`^a running coddy server with the models "([^"]*)" and "([^"]*)"$`, s.startServer)
	sc.Step(`^a chat session$`, s.newSession)
	sc.Step(`^a browser watches the server events$`, s.watchEvents)
	sc.Step(`^the user sends "([^"]*)"$`, s.userSends)
	sc.Step(`^the user sends "([^"]*)" and answers the first prompt with "([^"]*)"$`, s.userSendsAndAnswers)
	sc.Step(`^the answer says "([^"]*)"$`, s.answerSays)
	sc.Step(`^the session model is "([^"]*)"$`, s.sessionModelIs)
	sc.Step(`^no model was called$`, s.noModelCalled)
	sc.Step(`^the browser was told the session model is "([^"]*)"$`, s.browserToldModel)
	sc.Step(`^the model "([^"]*)" answered (\d+) requests?$`, s.modelAnswered)
	sc.Step(`^the server asks before running commands$`, s.serverAsks)
	sc.Step(`^the server runs commands without asking$`, s.serverBypasses)
	sc.Step(`^the model runs two commands in one step$`, s.modelRunsTwoCommands)
	sc.Step(`^the model runs one command$`, s.modelRunsOneCommand)
	sc.Step(`^the session permission mode is switched to "([^"]*)" over the API$`, s.switchPermissionOverAPI)
	sc.Step(`^(\d+) permission prompts? (?:was|were) shown$`, s.promptsShown)
	sc.Step(`^the session permission mode is "([^"]*)"$`, s.sessionPermissionIs)
	sc.Step(`^both commands ran$`, s.bothCommandsRan)
}

func TestSessionSettingsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session_settings",
		ScenarioInitializer: initializeSessionSettingsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_settings.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session_settings feature failed")
	}
}
