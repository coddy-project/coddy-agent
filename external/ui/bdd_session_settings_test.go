//go:build http && ui

package ui

import (
	"regexp"
	"testing"

	"github.com/cucumber/godog"
)

// The composer's mode, model and level follow the session on screen: that is
// App behaviour, asserted in Vitest against a stubbed server that applies a
// prompt's mode, model and level the way the real one does; each step names
// the test.
func TestWebUISessionSettingsFeature(t *testing.T) {
	const file = "src/ui/App.settingsPerSession.test.tsx"
	steps := []string{
		"a new chat starts in Agent mode, not in the mode of the chat just left",
		"a mode picked on the start page creates the chat in that mode",
		"a prompt sent while the opened session's settings are read carries nothing of the previous session",
		"a mode picked in a session is sent with its next prompt, and stays in that session",
		"coming back to a chat whose turn this tab runs shows that chat's own settings",
	}
	suite := godog.TestSuite{
		Name: "web_ui_session_settings",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, name := range steps {
				sc.Step("^"+regexp.QuoteMeta(name)+"$", func() error {
					return runVitestScenario(file, name)
				})
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_session_settings.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI session settings feature failed")
	}
}
