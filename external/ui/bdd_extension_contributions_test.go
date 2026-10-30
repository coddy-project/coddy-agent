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
		sc.Step(`^an extension message renderer matching one frame event$`, func() {})
		sc.Step(`^a live turn stream carries a frame of that event$`, func() {})
		sc.Step(`^the renderer draws one ext_frame row in the transcript$`, func() error {
			return runVitestScenario("src/ui/extensions/ExtensionMessageView.test.tsx", "a message renderer draws an ext_frame row in the transcript")
		})
		sc.Step(`^an extension permission preview for a named tool$`, func() {})
		sc.Step(`^a call of that tool waits for permission$`, func() {})
		sc.Step(`^the preview appears under the host call preview and the host options remain$`, func() error {
			return runVitestScenario("src/ui/extensions/ExtensionPermissionPreview.test.tsx", "the preview appears under the host call preview and the host options remain")
		})
		sc.Step(`^an extension panel and a menu action that opens it$`, func() {})
		sc.Step(`^the user selects the menu action$`, func() {})
		sc.Step(`^the panel opens with the extension content$`, func() error {
			return runVitestScenario("src/ui/extensions/runtimeContributions.test.ts", "the widened extension API a menu action opens its panel")
		})
		sc.Step(`^an extension that overrides theme colours$`, func() {})
		sc.Step(`^the override replaces the accent colour$`, func() {})
		sc.Step(`^the app repaints live and removing the override restores the theme$`, func() error {
			return runVitestScenario("src/ui/extensions/themeOverrides.test.ts", "theme overrides and effects an override paints its tokens and remove drops the layer")
		})
		sc.Step(`^an extension session filter that hides archived rows$`, func() {})
		sc.Step(`^the History listing holds archived and active sessions$`, func() {})
		sc.Step(`^the listing shows only the rows every extension filter passes$`, func() error {
			return runVitestScenario("src/ui/extensions/ExtensionSessionFilters.test.ts", "session filters the listing shows only the rows every extension filter passes")
		})
		sc.Step(`^sessions of two projects$`, func() {})
		sc.Step(`^the History listing opens in one of them$`, func() {})
		sc.Step(`^it lists the sessions of that project$`, func() error {
			if err := runVitestScenario("src/ui/sessions/projectFilter.test.ts", "the project filter a row of the project: the folder, under it, or the same repository"); err != nil {
				return err
			}
			return runVitestScenario("src/ui/sessions/projectFilter.test.ts", "the project filter the filter narrows only in force and only with a folder")
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("extension contributions feature failed")
	}
}
