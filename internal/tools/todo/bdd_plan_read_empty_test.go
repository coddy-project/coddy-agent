package todo_test

// Godog harness for features/todo_plan_read_empty.feature: the plan read tool
// runs against the real registry with a stub sender (no LLM, no session); the
// scenario asserts the model is told there is no plan instead of getting an
// empty result.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
)

type planReadEmptyState struct {
	env    *apptools.Env
	output string
	runErr error
}

func (s *planReadEmptyState) reset() {
	s.env = nil
	s.output = ""
	s.runErr = nil
}

func (s *planReadEmptyState) aSessionWithoutATodoPlan() error {
	plan := []acp.PlanEntry{}
	s.env = envWithPlan(&mockSender{}, &plan)
	return nil
}

func (s *planReadEmptyState) theModelCallsPlanReadWithNoArguments() error {
	s.output, s.runErr = apptools.NewRegistry().Execute(context.Background(), todo.ToolNamePlanRead, `{}`, s.env)
	return nil
}

func (s *planReadEmptyState) theAnswerIsNotEmpty() error {
	if s.runErr != nil {
		return fmt.Errorf("plan read failed: %v", s.runErr)
	}
	if strings.TrimSpace(s.output) == "" {
		return fmt.Errorf("the answer is empty")
	}
	return nil
}

func (s *planReadEmptyState) theAnswerSaysThereIsNoActivePlan() error {
	if !strings.Contains(s.output, "no active items") {
		return fmt.Errorf("answer %q does not say there is no active plan", s.output)
	}
	return nil
}

func (s *planReadEmptyState) theAnswerNamesTheTool(name string) error {
	if !strings.Contains(s.output, name) {
		return fmt.Errorf("answer %q does not name %s", s.output, name)
	}
	return nil
}

func initializePlanReadEmptyScenario(sc *godog.ScenarioContext) {
	s := &planReadEmptyState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.Step(`^a session without a todo plan$`, s.aSessionWithoutATodoPlan)
	sc.Step(`^the model calls coddy_todo_plan_read with no arguments$`, s.theModelCallsPlanReadWithNoArguments)
	sc.Step(`^the answer is not empty$`, s.theAnswerIsNotEmpty)
	sc.Step(`^the answer says there is no active todo plan$`, s.theAnswerSaysThereIsNoActivePlan)
	sc.Step(`^the answer names ([a-z_]+) as the way to start one$`, s.theAnswerNamesTheTool)
}

func TestTodoPlanReadEmptyFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "todo-plan-read-empty",
		ScenarioInitializer: initializePlanReadEmptyScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/todo_plan_read_empty.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("todo plan read empty feature suite failed")
	}
}
