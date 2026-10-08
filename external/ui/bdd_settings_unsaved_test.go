//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The unsaved-changes state of the Settings form (issue #485) is behaviour of
// the drawer, so each step runs the Vitest test that drives it against a
// stubbed server.
func TestWebUISettingsUnsavedFeature(t *testing.T) {
	const file = "src/ui/settings/Settings.unsaved.test.tsx"
	steps := []struct{ step, name string }{
		{`^an edit highlights Save and says it is not saved until Save is pressed$`,
			"an edit makes Save stand out and says it is not saved until Save is pressed"},
		{`^an edit put back as it was leaves Save as it was$`,
			"an edit put back as it was leaves Save as it was"},
		{`^closing the drawer over unsaved edits asks to save them or keep editing$`,
			"closing Settings with unsaved edits asks to save them or keep editing"},
		{`^Escape over unsaved edits asks first$`,
			"Escape over unsaved edits asks first instead of closing"},
		{`^leaving the page over unsaved edits asks the browser, and nothing is saved into another server$`,
			"leaving the page with unsaved edits asks the browser, a switch to another server does not, and Save never writes there"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_settings_unsaved",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				name := s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_settings_unsaved.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI settings unsaved feature failed")
	}
}
