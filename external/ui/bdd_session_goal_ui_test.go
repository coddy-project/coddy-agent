//go:build http && ui

package ui

// Godog harness for features/session_goal_web_ui.feature: each step is a
// Vitest test of the SPA, run through runVitestScenario.

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestSessionGoalWebUIFeature(t *testing.T) {
	const app = "src/ui/App.goal.test.tsx"
	steps := map[string][2]string{
		`^the goal of the session read shows on the chip and its turns as goal rows$`: {app, "the goal of the session read shows on the chip, and its turns as goal rows"},
		`^the chip opens the popover with everything the supervisor knows$`:           {"src/ui/chat/ComposerGoal.test.tsx", "the chip opens the popover with everything the supervisor knows"},
		`^Pause reaches the goal route and the answer moves the chip$`:                {app, "Pause reaches PATCH /goal and the answer moves the chip"},
		`^Resume sends /goal resume as a prompt and leaves the draft alone$`:          {app, "Resume sends /goal resume as a prompt and leaves the draft alone"},
		`^Clear asks first, then reaches the goal route and the chip goes$`:           {app, "Clear asks, then reaches DELETE /goal, and the chip goes"},
		`^a bare /goal opens the goal popover and sends nothing$`:                     {app, "a bare /goal opens the goal popover and sends nothing"},
		`^on the stream the kickoff row replaces the prompt and each continuation opens a turn of its own$`: {
			"src/ui/chat/consumeComposerSse.goal.test.ts", "a /goal stream: the kickoff row replaces the prompt, each continuation opens a turn of its own"},
		`^after a reload a goal turn is a goal row that opens a turn of its own$`: {
			"src/ui/chat/transcriptFromMessages.test.ts", "a goal turn after a reload is a goal row, never a user bubble, and opens a turn of its own"},
		`^a prompt typed after a goal turn keeps the index the server knows it by$`: {
			"src/ui/messages/MessageList.test.tsx", "a goal turn is a goal row, not a user bubble, and a later prompt keeps the server's index"},
		`^the events stream moves the chip and an older or another session's snapshot does not$`: {
			app, "the events stream moves the chip; an older or another session's snapshot does not"},
	}
	suite := godog.TestSuite{
		Name: "session_goal_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for pattern, test := range steps {
				file, name := test[0], test[1]
				sc.Step(pattern, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/session_goal_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("session goal web UI feature failed")
	}
}
