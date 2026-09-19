//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// What the keys of an input method do is React behaviour, so each step runs
// the Vitest test that renders the composer and sends it composing keydowns.
func TestComposerInputMethodFeature(t *testing.T) {
	const composer = "src/ui/chat/Composer.test.tsx"
	suite := godog.TestSuite{
		Name: "composer_input_method",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^Enter that confirms an input-method candidate does not send the draft$`, func() error {
				return runVitestScenario(composer, "Enter that confirms an IME candidate does not send")
			})
			sc.Step(`^the slash picker takes no row and keeps its highlight on keys the input method is composing with$`, func() error {
				return runVitestScenario(composer, "keys an input method is composing with leave the slash picker alone")
			})
			sc.Step(`^the @ picker takes no row and keeps its highlight on keys the input method is composing with$`, func() error {
				return runVitestScenario(composer, "keys an input method is composing with leave the @ picker alone")
			})
			sc.Step(`^the /compact option picker takes no row and keeps its highlight on keys the input method is composing with$`, func() error {
				return runVitestScenario("src/ui/chat/Composer.commandArg.test.tsx",
					"keys an input method is composing with leave the list and the draft alone")
			})
			sc.Step(`^the line-range picker stays open on an Escape the input method is composing with$`, func() error {
				return runVitestScenario(composer, "an Escape the input method is composing with leaves the line-range picker open")
			})
			sc.Step(`^a keyCode 229 with no composition behind it still takes a row of the @ picker$`, func() error {
				return runVitestScenario(composer, "a keyCode 229 with no composition behind it still takes the @ row")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/composer_input_method.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("composer input method feature failed")
	}
}
