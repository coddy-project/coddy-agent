package agent

// Godog harness for features/plan_mode.feature: drives the real Agent.Run in
// plan mode with the fake LLM provider of the ask mode suite. One scenario
// replays a call to a tool outside the plan tool set and verifies the
// execution-time refusal; another runs plan_write, a tool the set admits, so
// the refusal is provably not a blanket one. The last one runs a saved plan
// through the session manager, which is what switches the turn to agent mode.
// It builds on the state of the ask mode harness (bdd_ask_mode_test.go): same
// session, same sender, same stub provider.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	bddPlanCallID = "call_plan"
	bddPlanSlug   = "refactor"
	bddPlanDoc    = "---\nname: Refactor\noverview: Split the package\ntodos:\n  - Move the helpers\n---\n\n# Refactor\n\nMove the helpers into their own package.\n"
)

type planModeFeatureState struct {
	askModeFeatureState
	// mgr runs the scenario that goes through the session manager: a plan run
	// is a prompt the manager turns into an agent-mode turn.
	mgr *session.Manager
}

// sessionWithPlanArmed opens a session through the session manager, in agent
// mode, and arms plan mode for the next turns the way an operator does, with a
// prompt that is only "/plan --count=N" and runs no turn.
func (s *planModeFeatureState) sessionWithPlanArmed(turns int) error {
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	root, err := s.tempDir()
	if err != nil {
		return err
	}
	s.target = filepath.Join(cwd, "notes.md")
	s.cfg = &config.Config{
		Paths:     config.Paths{Home: root, CWD: cwd, ConfigPath: filepath.Join(root, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
		Sessions:  config.Sessions{Dir: filepath.Join(root, "sessions")},
	}
	s.cfg.Hooks.ApplyDefaults(s.cfg.Paths)
	s.cfg.Subagents.ApplyDefaults(s.cfg.Paths)
	s.cfg.Prompts.ApplyDefaults()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		if s.provider == nil {
			return "", fmt.Errorf("no model prepared")
		}
		loop := NewAgent(s.cfg, st, snd, slog.Default())
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil })
		return loop.Run(ctx, prompt)
	}
	store := &session.FileStore{Root: s.cfg.Sessions.Dir}
	s.mgr = session.NewManager(s.cfg, resumePermissionSender{}, runner, slog.Default(), cwd, store)
	ctx := context.Background()
	res, err := s.mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: cwd})
	if err != nil {
		return err
	}
	s.st = s.mgr.SessionByID(res.SessionID)
	if s.st == nil {
		return fmt.Errorf("session %s missing", res.SessionID)
	}
	out, err := s.mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: fmt.Sprintf("/plan --count=%d", turns)}},
	})
	if err != nil {
		return err
	}
	if out.SettingsNotice == "" {
		return fmt.Errorf("the /plan command was not taken as a settings command")
	}
	if got := s.st.GetMode(); got != string(session.ModeAgent) {
		return fmt.Errorf("the session is in %q mode, want agent with plan armed", got)
	}
	return nil
}

func (s *planModeFeatureState) savedDesignPlan() error {
	sd := strings.TrimSpace(s.st.GetPersistedSessionDir())
	if sd == "" {
		return fmt.Errorf("the session has no bundle")
	}
	_, err := plans.Write(sd, bddPlanSlug, bddPlanDoc)
	return err
}

// userRunsPlan presses Run plan: the prompt carries the slug in its metadata,
// as the web UI and the ACP clients send it.
func (s *planModeFeatureState) userRunsPlan() error {
	if s.mgr == nil {
		return fmt.Errorf("no session manager prepared")
	}
	res, err := s.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: s.st.GetID(),
		Meta:      map[string]interface{}{plans.MetaRunPlanSlug: bddPlanSlug},
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Implement the plan."}},
	})
	s.runErr = err
	if res != nil {
		s.stop = string(res.StopReason)
	}
	return nil
}

func (s *planModeFeatureState) sessionInAgentModeWithNothingArmed() error {
	if got := s.st.GetMode(); got != string(session.ModeAgent) {
		return fmt.Errorf("the session is in %q mode after the plan run", got)
	}
	for _, o := range s.st.TurnOverrides() {
		if o.Setting == session.SettingMode {
			return fmt.Errorf("the mode %q is still held for the next turns: %+v", o.Value, o)
		}
	}
	return nil
}

func (s *planModeFeatureState) modelRequestsToolOnceThenAnswers(tool string) error {
	var args any
	switch tool {
	case "write":
		args = map[string]string{"path": s.target, "content": bddAskFileBody}
	case "plan_write":
		args = map[string]string{"slug": bddPlanSlug, "content": bddPlanDoc}
	default:
		return fmt.Errorf("the suite only simulates write and plan_write, got %q", tool)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	s.provider = &bddAskProvider{
		toolCall: &llm.ToolCall{ID: bddPlanCallID, Name: tool, InputJSON: string(raw)},
	}
	return s.buildAgent()
}

func (s *planModeFeatureState) userAsksForPlan() error {
	if s.ag == nil {
		return fmt.Errorf("no agent prepared")
	}
	s.stop, s.runErr = s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "plan how to split this package"}})
	return nil
}

func (s *planModeFeatureState) toolCallAnsweredWithPlanRefusal() error {
	return s.toolCallAnsweredWith(bddPlanCallID, "not available in Plan mode")
}

func (s *planModeFeatureState) toolCallAnsweredWithoutRefusal() error {
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == bddPlanCallID {
			if strings.Contains(m.Content, "not available in") {
				return fmt.Errorf("the call was refused: %q", m.Content)
			}
			return nil
		}
	}
	return fmt.Errorf("no tool result recorded for %s", bddPlanCallID)
}

func (s *planModeFeatureState) planDocumentIsWritten() error {
	doc, err := plans.Read(s.st.SessionDir, bddPlanSlug)
	if err != nil {
		return fmt.Errorf("the plan document was not written: %v", err)
	}
	if doc.Slug != bddPlanSlug {
		return fmt.Errorf("unexpected plan document %+v", doc)
	}
	return nil
}

func initializePlanModeScenario(sc *godog.ScenarioContext) {
	s := &planModeFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.mgr = nil
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy session in "([^"]+)" mode$`, s.sessionInMode)
	sc.Step(`^a coddy session with plan mode armed for the next (\d+) turns$`, s.sessionWithPlanArmed)
	sc.Step(`^a saved design plan$`, s.savedDesignPlan)
	sc.Step(`^the user runs the plan$`, s.userRunsPlan)
	sc.Step(`^the file is written$`, s.fileIsWritten)
	sc.Step(`^the session is in agent mode with no mode armed for the next turns$`, s.sessionInAgentModeWithNothingArmed)
	sc.Step(`^a model that requests the "([^"]+)" tool once, then answers$`, s.modelRequestsToolOnceThenAnswers)
	sc.Step(`^the user asks for a plan$`, s.userAsksForPlan)
	sc.Step(`^the file is not written$`, s.fileIsNotWritten)
	sc.Step(`^the plan document is written$`, s.planDocumentIsWritten)
	sc.Step(`^the tool call is answered with the plan-mode refusal$`, s.toolCallAnsweredWithPlanRefusal)
	sc.Step(`^the tool call is answered without a refusal$`, s.toolCallAnsweredWithoutRefusal)
	sc.Step(`^the turn ends with the model's answer$`, s.turnEndsWithAnswer)
}

func TestPlanModeFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "plan-mode",
		ScenarioInitializer: initializePlanModeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/plan_mode.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("plan mode feature suite failed")
	}
}
