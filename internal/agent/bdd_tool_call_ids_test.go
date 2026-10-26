package agent

// Godog harness for features/tool_call_ids.feature: drives the real Agent through
// a provider that streams its calls the way an OpenAI-compatible endpoint does
// (the name, an argument fragment, the complete call) and hands out the same
// tool call id in every step, as a model that writes its own ids can. It then
// checks every place a call is found by its id: the rows announced to a client,
// the permission prompts, the records under tool_calls/ and the history.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// reusedIDProvider streams one write call per step, every one under the same id.
type reusedIDProvider struct {
	id     string
	paths  []string
	answer string
	i      int
}

func (p *reusedIDProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("stream required")
}

func (p *reusedIDProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, emit func(llm.StreamChunk)) (*llm.Response, error) {
	if p.i >= len(p.paths) {
		emit(llm.StreamChunk{TextDelta: p.answer})
		return &llm.Response{Content: p.answer, StopReason: "end_turn"}, nil
	}
	path := p.paths[p.i]
	p.i++
	args, _ := json.Marshal(map[string]string{"path": path, "content": "written by step " + path})
	emit(llm.StreamChunk{ToolCallNamed: &llm.ToolCall{ID: p.id, Name: "write"}})
	emit(llm.StreamChunk{ToolCallDelta: &llm.ToolCall{ID: p.id, Name: "write", InputJSON: string(args)}})
	tc := llm.ToolCall{ID: p.id, Name: "write", InputJSON: string(args)}
	emit(llm.StreamChunk{ToolCall: &tc})
	return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
}

// idRecordingSender keeps every update and approves every permission prompt,
// noting the call it was asked about.
type idRecordingSender struct {
	resumePermissionSender
	mu      sync.Mutex
	updates []interface{}
	prompts []string
}

func (s *idRecordingSender) SendSessionUpdate(_ string, update interface{}) error {
	s.mu.Lock()
	s.updates = append(s.updates, update)
	s.mu.Unlock()
	return nil
}

func (s *idRecordingSender) RequestPermission(_ context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.mu.Lock()
	s.prompts = append(s.prompts, params.ToolCall.ToolCallID)
	s.mu.Unlock()
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

type tcidFeatureState struct {
	root       string
	cwd        string
	sessionDir string
	st         *session.State
	sender     *idRecordingSender
	provider   *reusedIDProvider
	paths      []string
}

func (s *tcidFeatureState) reset() error {
	root, err := os.MkdirTemp("", "coddy-bdd-reused-id-*")
	if err != nil {
		return err
	}
	s.root = root
	s.cwd = filepath.Join(root, "workspace")
	s.sessionDir = filepath.Join(root, "bundle")
	s.sender = &idRecordingSender{}
	s.provider = nil
	s.paths = nil
	if err := os.MkdirAll(s.cwd, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(s.sessionDir, 0o755)
}

func (s *tcidFeatureState) close() {
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
	s.st = nil
}

func (s *tcidFeatureState) modelReusingID(id string) error {
	s.provider = &reusedIDProvider{id: id, answer: "both files written"}
	return nil
}

func (s *tcidFeatureState) writesInTwoSteps(first, second string) error {
	s.paths = []string{filepath.Join(s.cwd, first), filepath.Join(s.cwd, second)}
	s.provider.paths = s.paths
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeAsk},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	s.st = &session.State{ID: "sess_bdd_reused_id", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: s.sessionDir}
	ag := NewAgent(cfg, s.st, s.sender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	_, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "write both files"}})
	return err
}

func (s *tcidFeatureState) announcedUnder(first, second string) error {
	s.sender.mu.Lock()
	defer s.sender.mu.Unlock()
	var announced []string
	for _, u := range s.sender.updates {
		if v, ok := u.(acp.ToolCallUpdate); ok && v.SessionUpdate == acp.UpdateTypeToolCall {
			announced = append(announced, v.ToolCallID)
		}
	}
	if strings.Join(announced, ",") != first+","+second {
		return fmt.Errorf("the calls were announced under %v, want [%s %s]", announced, first, second)
	}
	return nil
}

func (s *tcidFeatureState) promptsName(first, second string) error {
	s.sender.mu.Lock()
	defer s.sender.mu.Unlock()
	if strings.Join(s.sender.prompts, ",") != first+","+second {
		return fmt.Errorf("the permission prompts named %v, want [%s %s]", s.sender.prompts, first, second)
	}
	return nil
}

func (s *tcidFeatureState) recordPerCall() error {
	dirs, err := session.ListToolCalls(s.sessionDir)
	if err != nil {
		return fmt.Errorf("list tool calls: %w", err)
	}
	if len(dirs) != len(s.paths) {
		return fmt.Errorf("the bundle holds %d tool call records %v, want %d", len(dirs), dirs, len(s.paths))
	}
	seen := make(map[string]bool, len(s.paths))
	for _, d := range dirs {
		args, err := session.ReadToolCallArgs(s.sessionDir, d)
		if err != nil {
			return fmt.Errorf("read the arguments of %s: %w", d, err)
		}
		for _, p := range s.paths {
			if strings.Contains(args, filepath.Base(p)) {
				seen[p] = true
			}
		}
	}
	for _, p := range s.paths {
		if !seen[p] {
			return fmt.Errorf("no tool call record holds the arguments for %s", filepath.Base(p))
		}
	}
	return nil
}

func (s *tcidFeatureState) historyPairsEachResult() error {
	var calls, results []string
	for _, m := range s.st.GetMessages() {
		for _, tc := range m.ToolCalls {
			calls = append(calls, tc.ID)
		}
		if m.Role == llm.RoleTool {
			results = append(results, m.ToolCallID)
		}
	}
	if len(calls) != len(s.paths) || calls[0] == calls[1] {
		return fmt.Errorf("the history holds the calls %v, want %d calls with different ids", calls, len(s.paths))
	}
	if strings.Join(results, ",") != strings.Join(calls, ",") {
		return fmt.Errorf("the history pairs results %v with calls %v", results, calls)
	}
	if issues := session.ValidateToolPairing(s.st.GetMessages()); len(issues) > 0 {
		return fmt.Errorf("the history fails the pairing check: %v", issues)
	}
	for _, p := range s.paths {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("the call writing %s did not run: %w", filepath.Base(p), err)
		}
	}
	return nil
}

func (s *tcidFeatureState) turnEndedWithAnswer() error {
	msgs := s.st.GetMessages()
	if len(msgs) == 0 {
		return fmt.Errorf("the transcript is empty")
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content, s.provider.answer) {
		return fmt.Errorf("the turn ended on %s %q, want the model's answer", last.Role, last.Content)
	}
	return nil
}

func initializeToolCallIDsScenario(sc *godog.ScenarioContext) {
	s := &tcidFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a session bundle in ask mode and a model that names every call "([^"]*)"$`, s.modelReusingID)
	sc.Step(`^the model writes "([^"]*)" in one step and "([^"]*)" in the next$`, s.writesInTwoSteps)
	sc.Step(`^the calls are announced under the ids "([^"]*)" and "([^"]*)"$`, s.announcedUnder)
	sc.Step(`^the permission prompts name the calls "([^"]*)" and "([^"]*)"$`, s.promptsName)
	sc.Step(`^the bundle keeps a record for each call with its own arguments$`, s.recordPerCall)
	sc.Step(`^the history pairs each result with its own call$`, s.historyPairsEachResult)
	sc.Step(`^the turn ended with the model's answer$`, s.turnEndedWithAnswer)
}

func TestToolCallIDsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "tool-call-ids",
		ScenarioInitializer: initializeToolCallIDsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/tool_call_ids.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("tool call ids feature suite failed")
	}
}
