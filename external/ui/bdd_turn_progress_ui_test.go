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
	const panel = "src/ui/tasks/BackgroundTasksPanel.test.tsx"
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
			sc.Step(`^running and finished tasks are the same card with a dot, a tag, a title and a meta line$`, func() error {
				return runVitestScenario(panel, "running tasks stand above the counter, finished ones behind it, all as the same card")
			})
			sc.Step(`^a card names what runs in a tag on the left and the work in its title$`, func() error {
				return runVitestScenario(panel, "a card names what runs in a tag on the left and the work in its title")
			})
			sc.Step(`^a click on a card expands it in place and another folds it$`, func() error {
				return runVitestScenario(panel, "the card is one control: a click expands it in place, another folds it")
			})
			sc.Step(`^an open command card shows the command with a copy control, the output and how it ended$`, func() error {
				return runVitestScenario(panel, "an expanded command card shows the command with a copy control, the output and how it ended")
			})
			sc.Step(`^an open subagent card offers the child transcript and shows the run's log$`, func() error {
				return runVitestScenario(panel, "an expanded subagent card opens the child transcript and shows the run's log, not a command")
			})
			sc.Step(`^any number of cards stay open at once, each with its own output$`, func() error {
				return runVitestScenario(panel, "any number of cards stay open side by side, each with its own output")
			})
			sc.Step(`^a card the shell points at opens on its own$`, func() error {
				return runVitestScenario(panel, "a card the shell points at opens on its own, its section with it")
			})
			sc.Step(`^Stop on a running card stops the task without opening the card$`, func() error {
				return runVitestScenario(panel, "only a running task offers Stop, and Stop is not part of the card's own control")
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
