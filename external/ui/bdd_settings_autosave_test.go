//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The autosave of the Settings form (issue #485) is behaviour of the drawer and
// its store, so each step runs the Vitest test that drives it against a stubbed
// server.
func TestWebUISettingsAutosaveFeature(t *testing.T) {
	const file = "src/ui/settings/Settings.autosave.test.tsx"
	steps := []struct{ step, name string }{
		{`^several edits of an ordinary field go out as one save a moment after the last of them$`,
			"an ordinary change saves itself after a pause, several edits in one save"},
		{`^turning a gateway on waits for the highlighted Save while the rest of the form saves itself$`,
			"turning on a gateway waits for Save, highlighted, while the rest of the form saves itself"},
		{`^removing a provider waits for Save and Discard puts it back$`,
			"removing a provider waits for Save, and Discard puts it back"},
		{`^closing the drawer with a change waiting for Save asks to save it or keep editing$`,
			"closing Settings with a change waiting for Save asks first"},
		{`^a change waiting for Save is there again when the drawer opens$`,
			"a change waiting for Save is still there when the drawer opens again"},
		{`^the next save goes out under the revision the last save answered with$`,
			"the next save goes out under the revision the last one answered with"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_settings_autosave",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				name := s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_settings_autosave.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI settings autosave feature failed")
	}
}
