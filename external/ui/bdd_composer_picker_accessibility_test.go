//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestComposerPickerAccessibilityFeature(t *testing.T) {
	const file = "src/ui/chat/ComposerModelMenu.test.tsx"
	suite := godog.TestSuite{
		Name: "composer_picker_accessibility",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^the Mode chip names the current value first and opens a radio menu that supports arrow keys, Home and End$`, func() error {
				return runVitestScenario(file, "composer picker accessibility Mode is a radio menu with keyboard navigation")
			})
			sc.Step(`^committing with Enter or a click, or dismissing with Escape, returns focus to the Mode chip$`, func() error {
				return runVitestScenario(file, "composer picker accessibility Mode selection and Escape return focus to the chip")
			})
			sc.Step(`^picking /model opens a radio menu, and Escape or selection returns focus to the message field$`, func() error {
				return runVitestScenario(file, "composer picker accessibility /model opens a radio menu for a short list and returns to the message field")
			})
			sc.Step(`^picking /model focuses the filter in a named dialog, where Down then Enter chooses a result, and Escape or selection returns focus to the message field$`, func() error {
				return runVitestScenario(file, "composer picker accessibility /model opens a filter dialog for a long list and returns to the message field")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_composer_picker_accessibility.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("composer picker accessibility feature failed")
	}
}
