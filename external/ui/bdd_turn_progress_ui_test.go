//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestTurnProgressWebUIFeature(t *testing.T) {
	const dots = "src/ui/messages/TypingDotsMessage.test.tsx"
	const screen = "src/ui/chat/ChatScreen.test.tsx"
	suite := godog.TestSuite{
		Name: "turn_progress_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^before the first token the live line shows the turn clock and the waiting phrase alone$`, func() error {
				return runVitestScenario(dots, "before the first token the line is the turn clock and the waiting phrase")
			})
			sc.Step(`^the live line shows the generated tokens once there are any, shortened past a thousand$`, func() error {
				return runVitestScenario(dots, "generated tokens appear once there are any, shortened past a thousand")
			})
			sc.Step(`^the live line of a running turn carries the server's clock and token count$`, func() error {
				return runVitestScenario(screen, "the live line of a running turn carries the server's clock and token count")
			})
			sc.Step(`^a turn_progress frame reaches the tab on its own clock$`, func() error {
				return runVitestScenario("src/ui/chat/consumeComposerSse.order.test.ts",
					"turn_progress reaches the caller on this machine's clock, replayed frames aged")
			})
			sc.Step(`^the live line names the running background tasks and opens the Tasks panel$`, func() error {
				return runVitestScenario(dots, "running background tasks are named on the line and open the Tasks panel")
			})
			sc.Step(`^with a turn and tasks both running the chip under the transcript steps aside$`, func() error {
				return runVitestScenario(screen, "with a turn and tasks both running, the live line names the tasks and the chip steps aside")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/turn_progress_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("turn progress web UI feature failed")
	}
}
