package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestReActRetryBudgetFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "react-retry-budget",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var f *retryBudgetFixture
			sc.Step(`^a ReAct agent with retries disabled$`, func() { zero := 0; f = newRetryBudgetFixture(t, &zero, 3, "reasoning") })
			sc.Step(`^a ReAct agent with two retries available$`, func() { two := 2; f = newRetryBudgetFixture(t, &two, 10, "reasoning", "reasoning", "answer") })
			sc.Step(`^the model returns signed reasoning with no answer$`, func() { f.run() })
			sc.Step(`^the model answers after two reasoning-only responses$`, func() { f.run() })
			count := func(want int) error {
				if got := f.requestCount(); got != want {
					return fmt.Errorf("upstream requests = %d, want %d", got, want)
				}
				return nil
			}
			sc.Step(`^exactly one upstream request was sent$`, func() error { return count(1) })
			sc.Step(`^exactly three upstream requests were sent$`, func() error { return count(3) })
			sc.Step(`^the turn reports that the model produced no reply$`, func() error {
				if f.stop != string(acp.StopReasonRefused) || f.err == nil || !strings.Contains(f.err.Error(), "no reply") {
					return fmt.Errorf("stop=%s err=%v", f.stop, f.err)
				}
				return nil
			})
			sc.Step(`^the signed reasoning remains in the transcript$`, func() error {
				for _, m := range f.st.GetMessages() {
					if m.Role == llm.RoleAssistant && m.ReasoningSignature == "fixture-signature" && m.Reasoning == "thinking without an answer" {
						return nil
					}
				}
				return fmt.Errorf("signed reasoning missing")
			})
			sc.Step(`^the recovery requests contain no empty assistant text$`, func() error { return f.noEmptyAssistantText() })
			sc.Step(`^the turn finishes with the answer$`, func() error {
				if f.stop != string(acp.StopReasonEndTurn) || f.err != nil {
					return fmt.Errorf("stop=%s err=%v", f.stop, f.err)
				}
				for _, m := range f.st.GetMessages() {
					if m.Role == llm.RoleAssistant && m.Content == "The answer." {
						return nil
					}
				}
				return fmt.Errorf("answer missing")
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/react_retry_budget.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("ReAct retry budget feature failed")
	}
}
