package agent

// Godog harness for features/plan_mode.feature: drives the real Agent.Run in
// plan mode with the fake LLM provider of the ask mode suite. One scenario
// replays a call to a tool outside the plan tool set and verifies the
// execution-time refusal; the other runs plan_write, a tool the set admits, so
// the refusal is provably not a blanket one. It builds on the state of the ask
// mode harness (bdd_ask_mode_test.go): same session, same sender, same stub
// provider.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
)

const (
	bddPlanCallID = "call_plan"
	bddPlanSlug   = "refactor"
	bddPlanDoc    = "---\nname: Refactor\noverview: Split the package\ntodos:\n  - Move the helpers\n---\n\n# Refactor\n\nMove the helpers into their own package.\n"
)

type planModeFeatureState struct {
	askModeFeatureState
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
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy session in "([^"]+)" mode$`, s.sessionInMode)
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
