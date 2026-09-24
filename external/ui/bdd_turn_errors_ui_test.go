//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// Where the transcript places the server's UI log rows is decided by the pure
// feed in chat/uiLogNotices.ts, so each step runs the Vitest test that walks
// a history through it.
func TestTurnErrorsWebUIFeature(t *testing.T) {
	const feed = "src/ui/chat/uiLogNotices.test.ts"
	suite := godog.TestSuite{
		Name: "turn_errors_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the error of a turn that follows two compaction summaries is shown at the end of that turn$`, func() error {
				return runVitestScenario(feed, "the server counts compaction summaries as turns, and so does the feed")
			})
			sc.Step(`^an error stamped past the end of the history is still shown$`, func() error {
				return runVitestScenario(feed, "a notice stamped past the end of the history is still shown")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/turn_errors_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("turn errors web UI feature failed")
	}
}
