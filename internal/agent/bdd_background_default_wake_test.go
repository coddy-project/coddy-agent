package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

type defaultWakeFeatureState struct {
	subagentsFeatureState
	woken  chan Wake
	taskID string
}

func (s *defaultWakeFeatureState) parentWithWaker() error {
	if err := s.parentSessionWithPermission("bypass"); err != nil {
		return err
	}
	w := NewBackgroundWaker(slog.Default(), func(ctx context.Context, wake Wake) error {
		_, err := s.mgr.HandleSessionPromptWithSender(ctx, wake.PromptParams(), s.client, wake.RunOpts())
		if err == nil {
			s.woken <- wake
		}
		return err
	})
	w.busyRetryFirst = 10 * time.Millisecond
	w.busyRetryMax = 30 * time.Millisecond
	w.Attach(bgtask.Default())
	return nil
}

func (s *defaultWakeFeatureState) approvedAgent(name string) error {
	if err := s.workspaceDefinition(name); err != nil {
		return err
	}
	if err := s.approveDefinition(name); err != nil {
		return err
	}
	s.setChildAnswer("REPORT: complete")
	return nil
}

func (s *defaultWakeFeatureState) startWithoutNotify(kind string) error {
	var call llm.ToolCall
	switch kind {
	case "command":
		call = commandCall("background", "echo bdd-wake", true)
	case "subagent":
		call = spawnCall("background", "reviewer", true)
	default:
		return fmt.Errorf("unknown background kind %q", kind)
	}
	results, err := s.runParentTurn(toolStep(call), answerStep("parent turn done"))
	if err != nil {
		return err
	}
	s.taskID = extractTaskID(results["background"])
	if s.taskID == "" {
		return fmt.Errorf("tool result names no background task: %q", results["background"])
	}
	return nil
}

func (s *defaultWakeFeatureState) receivesWake(kind string) error {
	select {
	case wake := <-s.woken:
		if len(wake.Tasks) != 1 || wake.Tasks[0].ID != s.taskID || string(wake.Tasks[0].Kind) != kind {
			return fmt.Errorf("wake = %+v, want task %s of kind %s", wake, s.taskID, kind)
		}
	case <-time.After(10 * time.Second):
		return fmt.Errorf("no parent turn started for %s", s.taskID)
	}
	for _, msg := range s.parent.GetMessages() {
		if msg.Role == llm.RoleUser && msg.BackgroundWake != nil && strings.Contains(msg.Content, s.taskID) {
			return nil
		}
	}
	return fmt.Errorf("the parent transcript has no woken turn for %s", s.taskID)
}

func TestBackgroundDefaultWakeFeature(t *testing.T) {
	s := &defaultWakeFeatureState{}
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s.woken = make(chan Wake, 2)
				return ctx, s.reset()
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				bgtask.Default().SubscribeKeyed(BackgroundWakerKey, nil)
				s.close()
				return ctx, nil
			})
			sc.Step(`^a parent session with a background waker$`, s.parentWithWaker)
			sc.Step(`^an approved subagent named "([^"]*)"$`, s.approvedAgent)
			sc.Step(`^the parent starts a background command without notify_on_finish and ends its turn$`, func() error { return s.startWithoutNotify("command") })
			sc.Step(`^the parent starts that subagent in the background without notify_on_finish and ends its turn$`, func() error { return s.startWithoutNotify("subagent") })
			sc.Step(`^completion starts a new parent turn for the command$`, func() error { return s.receivesWake("command") })
			sc.Step(`^completion starts a new parent turn for the subagent$`, func() error { return s.receivesWake("agent") })
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/background_default_wake.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("background default wake feature failed")
	}
}
