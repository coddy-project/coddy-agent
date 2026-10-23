//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestTodoChecklistWebUIFeature(t *testing.T) {
	const app = "src/ui/App.todoChecklist.test.tsx"
	const card = "src/ui/messages/TodoChecklistMessage.test.tsx"
	const list = "src/ui/messages/MessageList.test.tsx"
	suite := godog.TestSuite{
		Name: "todo_checklist_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a conversation whose checklist has work left shows the list under its newest message$`, func() error {
				return runVitestScenario(app, "opening a conversation shows its checklist under the newest message")
			})
			sc.Step(`^the item in progress is marked, and its mark stands still while no turn runs$`, func() error {
				return runVitestScenario(card, "the item in progress is named on its row, and its mark spins only while a turn runs")
			})
			sc.Step(`^the plan frames of a running turn move the checklist and the mark of the item in progress spins$`, func() error {
				return runVitestScenario(app, "the plan frames of a running turn move the checklist, its item in progress spinning")
			})
			sc.Step(`^a plan frame of a turn reaches the tab, a design plan's frame does not$`, func() error {
				return runVitestScenario("src/ui/chat/consumeComposerSse.plan.test.ts",
					"a plan frame reaches the caller as the checklist, a design plan's frame does not")
			})
			sc.Step(`^the checklist stays while the turn that finished it runs and leaves once the turn is over$`, func() error {
				return runVitestScenario(app, "a finished checklist stays while its turn runs and leaves once the turn is over")
			})
			sc.Step(`^the checklist stands above the live line and only at the newest end of the transcript$`, func() error {
				return runVitestScenario(list, "the todo checklist stands above the live line, only at the newest end of the transcript")
			})
			sc.Step(`^a click on the list's head folds it to the item in progress and the choice is remembered$`, func() error {
				return runVitestScenario(card, "a click on the head folds the list to the item in progress, and the choice is remembered")
			})
			sc.Step(`^a status changes the mark in place and a list that stops showing fades out before it leaves$`, func() error {
				if err := runVitestScenario(card, "a status changes the mark in place and pops its glyph in"); err != nil {
					return err
				}
				return runVitestScenario(card, "a list that stops showing fades out before it leaves, and stays when shown again")
			})
			sc.Step(`^every motion of the list is drawn only for a reader who did not ask for less motion$`, func() error {
				return runVitestScenario("src/ui/messages/todoChecklistCss.test.ts",
					"every motion of the checklist is drawn only without a request for less motion")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/todo_checklist_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("todo checklist web UI feature failed")
	}
}
