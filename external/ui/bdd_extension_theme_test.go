//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestExtensionThemeFeature(t *testing.T) {
	suite := godog.TestSuite{Name: "ui_extension_theme", Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/ui_extension_theme.feature"}, TestingT: t, Strict: true}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^an extension that paints with the theme colours$`, func() {})
		sc.Step(`^the user switches the theme$`, func() {})
		sc.Step(`^the extension reads the new colours and receives the change$`, func() error {
			return runVitestScenario("src/ui/extensions/theme.test.ts", "an extension reads the theme colours and follows a theme switch")
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("extension theme feature failed")
	}
}
