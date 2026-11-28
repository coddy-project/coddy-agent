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
			sc.Step(`^the short model menu exposes named vendor groups with radio choices$`, func() error {
				return runVitestScenario(file, "short model menus expose named vendor groups to assistive technology")
			})
			sc.Step(`^picking /model focuses the filter in a named dialog, where Down then Enter chooses a result, and Escape or selection returns focus to the message field$`, func() error {
				return runVitestScenario(file, "composer picker accessibility /model opens a filter dialog for a long list and returns to the message field")
			})
			sc.Step(`^opening the filtered model picker with ArrowUp focuses its last result$`, func() error {
				return runVitestScenario(file, "ArrowUp opens the filtered model picker at its last result")
			})
			sc.Step(`^opening the branch picker with ArrowUp focuses its last option$`, func() error {
				return runVitestScenario("src/ui/chat/WorkspaceChips.test.tsx", "WorkspaceChips opens the branch picker at its last option on ArrowUp")
			})
			sc.Step(`^the branch picker moves through options without selecting until Enter and returns focus to its chip$`, func() error {
				return runVitestScenario("src/ui/chat/WorkspaceChips.test.tsx", "WorkspaceChips moves from the branch filter through options without choosing until Enter")
			})
			sc.Step(`^opening the folder browser exposes a named dialog and traps keyboard focus within it$`, func() error {
				return runVitestScenario("src/ui/chat/WorkspaceChips.test.tsx", "WorkspaceChips cancels the folder browser modal without picking")
			})
			sc.Step(`^pressing Escape closes the folder browser and returns focus to the workspace chip$`, func() error {
				return runVitestScenario("src/ui/chat/WorkspaceChips.test.tsx", "WorkspaceChips folds the new folder row on Escape first, then closes the browser")
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
