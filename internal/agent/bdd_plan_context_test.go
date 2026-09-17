package agent

// Godog harness for features/plan_context_handoff.feature: a real Agent over a
// real session bundle, parked on a permission gate exactly the way the HTTP
// surface leaves one (the messages plus session.WritePendingPermission), then
// resumed through ResumeAfterPermission - the same call the HTTP layer makes
// when the user answers the prompt.

import (
	"context"
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

// planHandoffToken stands for the plan text plans.RunContextText builds.
const planHandoffToken = "PLAN_HANDOFF_TOKEN_A41C"

type pxScriptProvider struct {
	seen [][]llm.Message
}

func (p *pxScriptProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: "summary", StopReason: "end_turn"}, nil
}

func (p *pxScriptProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	onChunk(llm.StreamChunk{TextDelta: "done"})
	return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
}

type planContextState struct {
	tmpDirs  []string
	cwd      string
	store    *session.FileStore
	sid      string
	sd       string
	st       *session.State
	provider *pxScriptProvider
}

func (s *planContextState) reset() error {
	s.close()
	s.provider = &pxScriptProvider{}
	var err error
	if s.cwd, err = s.tempDir(); err != nil {
		return err
	}
	root, err := s.tempDir()
	if err != nil {
		return err
	}
	s.store = &session.FileStore{Root: root}
	s.sid = "sess_plan_handoff"
	s.sd, err = s.store.EnsureLayout(s.sid)
	return err
}

func (s *planContextState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.st = nil
}

func (s *planContextState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-planctx-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

func (s *planContextState) cfg() *config.Config {
	c := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	c.Agent.ApplyDefaults()
	c.Prompts.ApplyDefaults()
	return c
}

func (s *planContextState) newAgent() *Agent {
	ag := NewAgent(s.cfg(), s.st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	return ag
}

// parkedOnPermission leaves the session exactly as the HTTP surface does when a
// turn stops on a permission prompt: the user turn and the assistant's gated
// tool call in the transcript, the gate persisted in the bundle, and the plan
// hand-off that RunPlan put on the session before the turn started.
func (s *planContextState) parkedOnPermission() error {
	if err := s.reset(); err != nil {
		return err
	}
	s.st = &session.State{
		ID: s.sid, CWD: s.cwd, Mode: session.ModeAgent, SessionDir: s.sd,
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Implement the plan."},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
				ID: "call_gated", Name: "run_command", InputJSON: `{"command":"printf HELLO"}`,
			}}},
		},
	}
	s.st.SetPendingPlanContext(planHandoffToken)
	// The turn that stopped had already rendered its system prompt - that is
	// where the hand-off reached the model, and where it used to be consumed.
	if !strings.Contains(s.newAgent().buildSystemPrompt("agent", nil, nil, "", nil), planHandoffToken) {
		return fmt.Errorf("the turn that ran the plan never carried the hand-off")
	}
	if err := s.store.Save(s.st); err != nil {
		return err
	}
	// The arguments the prompt showed: the gate writes them when the call
	// starts, and the approval binds to them rather than to the transcript.
	if err := session.WriteToolCallArgs(s.sd, "call_gated", `{"command":"printf HELLO"}`); err != nil {
		return err
	}
	return session.WritePendingPermission(s.sd, acp.PermissionRequestParams{
		SessionID: s.sid,
		ToolCall: acp.PermissionToolCall{
			ToolCallID: "call_gated", Title: "Run: run_command", Kind: "run_command", Status: "pending",
		},
		Options: []acp.PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
			{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
		},
	}, "run_command", `{"command":"printf HELLO"}`)
}

// restart drops everything held in memory and rebuilds the session out of the
// bundle, which is all a resume after a process restart has to work with.
func (s *planContextState) restart() error {
	if s.st == nil {
		return fmt.Errorf("no session to restart")
	}
	snap, err := s.store.ReadSnapshot(s.sid)
	if err != nil {
		return fmt.Errorf("reload session bundle: %w", err)
	}
	s.st = &session.State{
		ID: s.sid, CWD: s.cwd, Mode: session.ModeAgent, SessionDir: s.sd,
		Messages: snap.Messages,
		Plan:     snap.Plan,
	}
	return nil
}

func (s *planContextState) approveTheTool() error {
	_, err := s.newAgent().ResumeAfterPermission(context.Background(), "call_gated", &acp.PermissionResult{
		Outcome: "allow", OptionID: "allow",
	})
	return err
}

func (s *planContextState) continuationCarriesPlanText() error {
	if len(s.provider.seen) == 0 {
		return fmt.Errorf("the turn was never continued: the provider saw no request")
	}
	sys := s.provider.seen[len(s.provider.seen)-1][0]
	if sys.Role != llm.RoleSystem {
		return fmt.Errorf("the continuation does not start with a system message")
	}
	if !strings.Contains(sys.Content, planHandoffToken) {
		return fmt.Errorf("the continuation lost the plan hand-off")
	}
	return nil
}

// --- the release half -------------------------------------------------------

func (s *planContextState) runningAPlan() error {
	if err := s.reset(); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.cwd, "main.go"), []byte("package main\n"), 0o644); err != nil {
		return err
	}
	s.st = &session.State{ID: s.sid, CWD: s.cwd, Mode: session.ModeAgent, SessionDir: s.sd}
	s.st.SetPendingPlanContext(planHandoffToken)
	return nil
}

func (s *planContextState) turnFinishesWithNoGate() error {
	_, err := s.newAgent().Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "Implement the plan."}})
	return err
}

func (s *planContextState) firstTurnCarriesPlanText() error {
	if len(s.provider.seen) == 0 {
		return fmt.Errorf("the provider saw no request")
	}
	if !strings.Contains(s.provider.seen[0][0].Content, planHandoffToken) {
		return fmt.Errorf("the turn that ran the plan never carried the hand-off")
	}
	return nil
}

func (s *planContextState) nextTurnCarriesNoPlanText() error {
	before := len(s.provider.seen)
	if _, err := s.newAgent().Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "anything else"}}); err != nil {
		return err
	}
	if len(s.provider.seen) <= before {
		return fmt.Errorf("the second turn made no request")
	}
	for i := before; i < len(s.provider.seen); i++ {
		if strings.Contains(s.provider.seen[i][0].Content, planHandoffToken) {
			return fmt.Errorf("request %d of the next turn still carries the plan hand-off", i)
		}
	}
	return nil
}

func initializePlanContextScenario(sc *godog.ScenarioContext) {
	s := &planContextState{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return ctx, err
	})

	sc.Step(`^a session parked on a permission prompt in the middle of a plan run$`, s.parkedOnPermission)
	sc.Step(`^the process is restarted, so nothing is left in memory$`, s.restart)
	sc.Step(`^the user approves the tool$`, s.approveTheTool)
	sc.Step(`^the request that continues the turn carries the plan text$`, s.continuationCarriesPlanText)

	sc.Step(`^a session running a saved design plan$`, s.runningAPlan)
	sc.Step(`^the turn finishes with no permission left pending$`, s.turnFinishesWithNoGate)
	sc.Step(`^the request of that turn carries the plan text$`, s.firstTurnCarriesPlanText)
	sc.Step(`^the next turn carries no plan text$`, s.nextTurnCarriesNoPlanText)
}

func TestPlanContextHandoffFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "plan-context-handoff",
		ScenarioInitializer: initializePlanContextScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/plan_context_handoff.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("plan context hand-off feature suite failed")
	}
}
