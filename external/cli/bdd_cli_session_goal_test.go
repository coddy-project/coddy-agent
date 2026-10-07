//go:build cli

package cli

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// cliGoalState drives features/cli_session_goal.feature: the console harness
// over the stub runner, with the supervisor's check scripted. The stub runner
// announces a goal turn like the agent does, so the rows and the footer are
// what a real turn produces.
type cliGoalState struct {
	*cliTUIState
	mu      sync.Mutex
	verdict session.GoalCheckResult
}

// judge is the scripted supervisor check: whatever the scenario last said.
func (s *cliGoalState) judge(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verdict, nil
}

func (s *cliGoalState) aConsoleAppWhoseSupervisorFinds(reason string) error {
	if err := s.buildApp(); err != nil {
		return err
	}
	s.mu.Lock()
	s.verdict = session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: reason}
	s.mu.Unlock()
	s.mgr.SetGoalJudge(s.judge)
	return nil
}

func (s *cliGoalState) supervisorNextNeeds(question string) error {
	s.mu.Lock()
	s.verdict = session.GoalCheckResult{Verdict: session.GoalVerdictNeedsUser, Reason: question}
	s.mu.Unlock()
	return nil
}

func (s *cliGoalState) operatorSetsGoal(objective string) error {
	s.typeText("/goal " + objective)
	s.press("\r")
	return s.waitScreen("/goal "+objective, 3*time.Second)
}

// agentFinishesTurn ends the stub turn that runs now: the supervisor checks
// it when it returns.
func (s *cliGoalState) agentFinishesTurn() error {
	s.directives <- stubDirective{kind: "end"}
	return s.waitTurnEnd(3 * time.Second)
}

func (s *cliGoalState) transcriptShowsGoalRow(note string) error {
	return s.waitScreen("◎ "+note, 3*time.Second)
}

// footerShowsNoGoal waits for the goal note to leave the footer. The rows of
// the transcript spell "Goal" with a capital, the footer does not.
func (s *cliGoalState) footerShowsNoGoal() error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		frame := s.screenText()
		if !strings.Contains(frame, "◎ goal ") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the footer still names a goal:\n%s", frame)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// operatorOpensGoalMenu types a bare /goal and waits for the menu.
func (s *cliGoalState) operatorOpensGoalMenu() error {
	s.typeText("/goal")
	s.press("\r")
	return s.waitScreen("Session goal", 3*time.Second)
}

// pickInModal moves the highlight of the selector on screen down to the row
// labelled label, the way the arrow keys do, and presses enter.
func (s *cliGoalState) pickInModal(label string) error {
	index := -1
	if err := s.onLoop(2*time.Second, func() {
		sel, ok := s.app.modal.(*selectorModal)
		if !ok {
			return
		}
		cur := sel.list.SelectedItem()
		for i := 0; i < 8 && cur != nil; i++ {
			if cur.Label == label {
				index = i
				return
			}
			sel.list.MoveDown()
			cur = sel.list.SelectedItem()
		}
	}); err != nil {
		return err
	}
	if index < 0 {
		return fmt.Errorf("no %q on the menu on screen:\n%s", label, s.screenText())
	}
	s.press("\r")
	return nil
}

func (s *cliGoalState) operatorPicksInGoalMenu(label string) error {
	return s.pickInModal(label)
}

func (s *cliGoalState) operatorConfirms(label string) error {
	if err := s.waitScreen("Clear the goal?", 3*time.Second); err != nil {
		return err
	}
	return s.pickInModal(label)
}

func initializeCLISessionGoalScenario(sc *godog.ScenarioContext) {
	s := &cliGoalState{cliTUIState: &cliTUIState{}}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.shutdown()
		return ctx, nil
	})

	sc.Step(`^a coddy console app whose supervisor finds "([^"]*)"$`, s.aConsoleAppWhoseSupervisorFinds)
	sc.Step(`^the console app starts$`, s.theConsoleAppStarts)
	sc.Step(`^the operator sets the goal "([^"]*)"$`, s.operatorSetsGoal)
	sc.Step(`^the transcript shows the goal row "([^"]*)"$`, s.transcriptShowsGoalRow)
	sc.Step(`^the transcript shows "([^"]*)"$`, func(text string) error { return s.waitScreen(text, 3*time.Second) })
	sc.Step(`^the footer shows "([^"]*)"$`, func(text string) error { return s.waitScreen(text, 3*time.Second) })
	sc.Step(`^the footer shows no goal$`, s.footerShowsNoGoal)
	sc.Step(`^the agent finishes its turn$`, s.agentFinishesTurn)
	sc.Step(`^the operator opens the goal menu$`, s.operatorOpensGoalMenu)
	sc.Step(`^the goal menu shows "([^"]*)"$`, func(text string) error { return s.waitScreen(text, 3*time.Second) })
	sc.Step(`^the operator picks "([^"]*)" in the goal menu$`, s.operatorPicksInGoalMenu)
	sc.Step(`^the operator confirms "([^"]*)"$`, s.operatorConfirms)
	sc.Step(`^the supervisor next finds that it needs "([^"]*)"$`, s.supervisorNextNeeds)
	sc.Step(`^the operator starts a new session$`, s.operatorStartsNewSession)
	sc.Step(`^the operator resumes the session before$`, s.operatorResumesSessionBefore)
}

func TestCLISessionGoalFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cli-session-goal",
		ScenarioInitializer: initializeCLISessionGoalScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/cli_session_goal.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cli session goal feature suite failed")
	}
}
