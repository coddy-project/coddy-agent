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
			sc.Step(`^the live line of a running turn counts the tasks without the memory run$`, func() error {
				return runVitestScenario(screen, "the live line of a running turn names the running tasks and opens the Tasks panel")
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

func TestBackgroundTasksWebUIFeature(t *testing.T) {
	const header = "src/ui/chat/ChatHeader.test.tsx"
	const screen = "src/ui/chat/ChatScreen.test.tsx"
	suite := godog.TestSuite{
		Name: "background_tasks_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the tasks control is in the header of a chat that never ran a task, without counts$`, func() error {
				return runVitestScenario(header, "the tasks control is in the header of a chat that never ran a task, without counts")
			})
			sc.Step(`^with tasks the header control says how many are running out of how many there are$`, func() error {
				return runVitestScenario(header, "with tasks the control says how many are running out of how many there are")
			})
			sc.Step(`^once everything has finished the header control keeps the total and drops the live mark$`, func() error {
				return runVitestScenario(header, "once everything has finished the control keeps the total and drops the live mark")
			})
			sc.Step(`^the header control opens the Tasks panel and a second click closes it$`, func() error {
				return runVitestScenario(screen, "the header control opens the Tasks panel and puts it away again")
			})
			sc.Step(`^the transcript ends with the conversation and the header control is the way to the tasks$`, func() error {
				return runVitestScenario(screen, "the transcript ends with the conversation: the way to the tasks is the header control")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/background_tasks_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("background tasks web UI feature failed")
	}
}
