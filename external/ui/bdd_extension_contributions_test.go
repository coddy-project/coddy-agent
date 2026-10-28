//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestExtensionContributionsFeature(t *testing.T) {
	suite := godog.TestSuite{Name: "ui_extension_contributions", Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/ui_extension_contributions.feature"}, TestingT: t, Strict: true}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^extension listeners for navigation steps and turns$`, func() {})
		sc.Step(`^the user changes tabs and a model step and turn finish$`, func() {})
		sc.Step(`^the extension receives the completed events with their context$`, func() error {
			return runVitestScenario("src/ui/extensions/contributions.test.tsx", "navigation steps and turns reach listeners with context")
		})
		sc.Step(`^an extension renderer for a named MCP tool$`, func() {})
		sc.Step(`^the tool input and result change$`, func() {})
		sc.Step(`^the custom card updates and original details remain available$`, func() error {
			return runVitestScenario("src/ui/extensions/contributions.test.tsx", "custom MCP card updates and preserves original details")
		})
		sc.Step(`^an extension field in the Agent settings section$`, func() {})
		sc.Step(`^the user edits its value$`, func() {})
		sc.Step(`^the field updates the extension namespace in the configuration draft$`, func() error {
			return runVitestScenario("src/ui/extensions/contributions.test.tsx", "registered Agent field updates its configuration namespace")
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("extension contributions feature failed")
	}
}
