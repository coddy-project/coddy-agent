package agent

import (
	"fmt"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestModelSwitchUserRequestFeature(t *testing.T) {
	var h *settingsHarness
	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a session with models "([^"]*)" and "([^"]*)"$`, func(a, b string) {
				h = newSettingsHarness(t, a, b)
			})
			sc.Step(`^the user asks the agent to switch to "([^"]*)"$`, func(model string) {
				h.provider("a").steps = []scriptStep{
					toolStep(llm.ToolCall{ID: "switch", Name: "switch_model", InputJSON: fmt.Sprintf(`{"model":%q}`, model)}),
				}
				h.provider("b").steps = []scriptStep{answerStep("now on b"), answerStep("still on b")}
				h.prompt(t, "Please switch to "+model)
			})
			sc.Step(`^the user sends another message$`, func() { h.prompt(t, "Continue") })
			sc.Step(`^the first model served one request and the chosen model served two$`, func() error {
				if a, b := h.provider("a").calls, h.provider("b").calls; a != 1 || b != 2 {
					return fmt.Errorf("model calls: a=%d b=%d, want 1 and 2", a, b)
				}
				return nil
			})
			sc.Step(`^the session remembers "([^"]*)"$`, func(model string) error {
				if got := h.mgr.SessionByID(h.sessionID).GetSelectedModelID(); got != model {
					return fmt.Errorf("selected model = %q, want %q", got, model)
				}
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/model_switch_user_request.feature"}, TestingT: t, Strict: true},
	}
	if status := suite.Run(); status != 0 {
		t.Fatal("model switch feature failed")
	}
}
