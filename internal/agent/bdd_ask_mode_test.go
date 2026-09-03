package agent

// Godog harness for features/ask_mode.feature: drives the real Agent.Run in
// ask mode with a fake LLM provider. One scenario checks the tool definitions
// the model is offered; another replays a call to a hidden tool and verifies
// the execution-time refusal, which is what keeps the read-only promise when a
// model echoes a call from history recorded in another mode. A control
// scenario runs the same call in agent mode so the refusal is provably the
// only thing standing between the call and the file.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	bddAskAnswer     = "The repository runs a ReAct loop; nothing was changed."
	bddAskHiddenCall = "call_hidden"
	bddAskFileBody   = "written by the model"
)

// bddAskProvider answers directly, or requests one tool call on its first
// turn and answers on the next. It records the tool definitions and messages
// it was given on every turn.
type bddAskProvider struct {
	toolCall *llm.ToolCall
	calls    int
	offered  [][]llm.ToolDefinition
	seen     [][]llm.Message
}

func (p *bddAskProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used by the ask mode suite")
}

func (p *bddAskProvider) Stream(_ context.Context, messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.calls++
	p.offered = append(p.offered, append([]llm.ToolDefinition(nil), defs...))
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	if p.toolCall != nil && p.calls == 1 {
		tc := *p.toolCall
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: bddAskAnswer})
	return &llm.Response{Content: bddAskAnswer, StopReason: "end_turn"}, nil
}

// askModeSender records session updates and counts permission prompts, so a
// scenario can assert that a refused call never reached the permission gate.
type askModeSender struct {
	resumePermissionSender
	updates     []interface{}
	permissions int
}

func (s *askModeSender) SendSessionUpdate(_ string, update interface{}) error {
	s.updates = append(s.updates, update)
	return nil
}

func (s *askModeSender) RequestPermission(ctx context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.permissions++
	return s.resumePermissionSender.RequestPermission(ctx, p)
}

type askModeFeatureState struct {
	tmpDirs  []string
	st       *session.State
	ag       *Agent
	cfg      *config.Config
	provider *bddAskProvider
	sender   *askModeSender
	// target is the file the simulated model tries to write.
	target string

	stop   string
	runErr error
}

func (s *askModeFeatureState) reset() error {
	s.close()
	s.sender = &askModeSender{}
	s.provider = nil
	s.target = ""
	s.stop = ""
	s.runErr = nil
	return nil
}

func (s *askModeFeatureState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.st = nil
	s.ag = nil
	s.cfg = nil
}

func (s *askModeFeatureState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-ask-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

func (s *askModeFeatureState) sessionInMode(mode string) error {
	if !session.IsValidMode(mode) {
		return fmt.Errorf("unknown session mode %q", mode)
	}
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.target = filepath.Join(cwd, "notes.md")
	s.st = &session.State{
		ID:         "sess_bdd_ask_" + mode,
		CWD:        cwd,
		Mode:       session.Mode(mode),
		SessionDir: sessionDir,
	}
	s.cfg = &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
	}
	return nil
}

func (s *askModeFeatureState) buildAgent() error {
	if s.st == nil {
		return fmt.Errorf("no session prepared")
	}
	s.ag = NewAgent(s.cfg, s.st, s.sender, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return s.provider, nil
	}
	return nil
}

func (s *askModeFeatureState) modelAnswersDirectly() error {
	s.provider = &bddAskProvider{}
	return s.buildAgent()
}

func (s *askModeFeatureState) modelRequestsToolOnceThenAnswers(tool string) error {
	if tool != "write" {
		return fmt.Errorf("the suite only simulates the write tool, got %q", tool)
	}
	args, err := json.Marshal(map[string]string{"path": s.target, "content": bddAskFileBody})
	if err != nil {
		return err
	}
	s.provider = &bddAskProvider{
		toolCall: &llm.ToolCall{ID: bddAskHiddenCall, Name: tool, InputJSON: string(args)},
	}
	return s.buildAgent()
}

func (s *askModeFeatureState) userAsksQuestion() error {
	if s.ag == nil {
		return fmt.Errorf("no agent prepared")
	}
	s.stop, s.runErr = s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "how does the agent loop work?"}})
	return nil
}

func (s *askModeFeatureState) everyOfferedToolIsReadOnly() error {
	if len(s.provider.offered) == 0 {
		return fmt.Errorf("the model was never called")
	}
	allowed := ToolSetForMode("ask")
	defs := s.provider.offered[0]
	if len(defs) == 0 {
		return fmt.Errorf("ask mode offered no tools at all; expected the read-only set")
	}
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Name] = true
		if !allowed.Allows(d.Name) {
			return fmt.Errorf("tool %q offered in ask mode is outside the read-only allowlist", d.Name)
		}
	}
	for _, want := range []string{"read", "glob", "grep", "print_tree"} {
		if !names[want] {
			return fmt.Errorf("read-only tool %q missing from the ask mode offer: %v", want, names)
		}
	}
	return nil
}

func (s *askModeFeatureState) fileIsNotWritten() error {
	if _, err := os.Stat(s.target); err == nil {
		return fmt.Errorf("%s exists: the hidden write ran in ask mode", s.target)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *askModeFeatureState) fileIsWritten() error {
	body, err := os.ReadFile(s.target)
	if err != nil {
		return fmt.Errorf("the write did not happen: %v", err)
	}
	if string(body) != bddAskFileBody {
		return fmt.Errorf("unexpected file body %q", body)
	}
	return nil
}

func (s *askModeFeatureState) toolCallAnsweredWithRefusal() error {
	var toolMsg *llm.Message
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == bddAskHiddenCall {
			mm := m
			toolMsg = &mm
			break
		}
	}
	if toolMsg == nil {
		return fmt.Errorf("no tool result recorded for %s", bddAskHiddenCall)
	}
	if !strings.Contains(toolMsg.Content, "not available in Ask mode") {
		return fmt.Errorf("tool result is not the ask-mode refusal: %q", toolMsg.Content)
	}
	if s.sender.permissions != 0 {
		return fmt.Errorf("the refused call still raised %d permission prompt(s)", s.sender.permissions)
	}
	cancelled := false
	for _, u := range s.sender.updates {
		if up, ok := u.(acp.ToolCallStatusUpdate); ok && up.ToolCallID == bddAskHiddenCall && up.Status == "cancelled" {
			cancelled = true
		}
	}
	if !cancelled {
		return fmt.Errorf("no cancelled tool_call_update was sent for %s", bddAskHiddenCall)
	}
	if len(s.provider.seen) < 2 {
		return fmt.Errorf("the model was not re-prompted after the refusal")
	}
	for _, m := range s.provider.seen[1] {
		if m.Role == llm.RoleTool && m.ToolCallID == bddAskHiddenCall && strings.Contains(m.Content, "not available in Ask mode") {
			return nil
		}
	}
	return fmt.Errorf("the refusal was not replayed to the model as the tool result")
}

func (s *askModeFeatureState) turnEndsWithAnswer() error {
	if s.runErr != nil {
		return fmt.Errorf("turn failed: %v", s.runErr)
	}
	if s.stop != string(acp.StopReasonEndTurn) {
		return fmt.Errorf("stop reason = %q, want end_turn", s.stop)
	}
	msgs := s.st.GetMessages()
	if len(msgs) == 0 {
		return fmt.Errorf("empty transcript")
	}
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content, bddAskAnswer) {
		return fmt.Errorf("final answer missing: %+v", last)
	}
	return nil
}

func initializeAskModeScenario(sc *godog.ScenarioContext) {
	s := &askModeFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy session in "([^"]+)" mode$`, s.sessionInMode)
	sc.Step(`^a model that answers directly$`, s.modelAnswersDirectly)
	sc.Step(`^a model that requests the "([^"]+)" tool once, then answers$`, s.modelRequestsToolOnceThenAnswers)
	sc.Step(`^the user asks a question$`, s.userAsksQuestion)
	sc.Step(`^every tool offered to the model is read-only$`, s.everyOfferedToolIsReadOnly)
	sc.Step(`^the file is not written$`, s.fileIsNotWritten)
	sc.Step(`^the file is written$`, s.fileIsWritten)
	sc.Step(`^the tool call is answered with the ask-mode refusal$`, s.toolCallAnsweredWithRefusal)
	sc.Step(`^the turn ends with the model's answer$`, s.turnEndsWithAnswer)
}

func TestAskModeFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "ask-mode",
		ScenarioInitializer: initializeAskModeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/ask_mode.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("ask mode feature suite failed")
	}
}
