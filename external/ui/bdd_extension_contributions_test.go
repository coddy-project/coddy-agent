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
		sc.Step(`^an extension watching focus and unloading$`, func() {})
		sc.Step(`^the window focus changes and unloading starts$`, func() {})
		sc.Step(`^the extension receives focus and beforeunload events$`, func() error {
			return runVitestScenario("src/ui/extensions/ExtensionsProvider.test.tsx", "focus, blur and unload reach lifecycle listeners")
		})
		sc.Step(`^a running chat with an answer and a generation rate$`, func() {})
		sc.Step(`^the extension reads the chat status$`, func() {})
		sc.Step(`^it receives the activity and parsed progress fields$`, func() error {
			return runVitestScenario("src/ui/extensions/chatStatus.test.ts", "the parsed chat status preparing, writing and the generation rate come from host state")
		})
		sc.Step(`^a composer with model and send controls$`, func() {})
		sc.Step(`^the extension asks for the fine composer anchors$`, func() {})
		sc.Step(`^the card, model and send anchor boxes are available$`, func() error {
			return runVitestScenario("src/ui/chat/Composer.test.tsx", "the composer exposes its card, model and send extension anchors")
		})
		sc.Step(`^a configured provider with credentials and advertised models$`, func() {})
		sc.Step(`^the extension lists providers and their models$`, func() {})
		sc.Step(`^provider rows omit credentials and model rows are normalized$`, func() error {
			if err := runVitestScenario("src/ui/extensions/providers.test.ts", "the providers API list returns only display-safe fields and freezes the result"); err != nil {
				return err
			}
			return runVitestScenario("src/ui/extensions/providers.test.ts", "the providers API models normalizes token counts, dedupes ids and freezes every row")
		})
		sc.Step(`^an extension that injects a private token$`, func() {})
		sc.Step(`^it reads the versioned token contract$`, func() {})
		sc.Step(`^the host token shape stays fixed and unknown overrides are refused$`, func() error {
			if err := runVitestScenario("src/ui/extensions/themeTokens.test.ts", "injected extension styles cannot change the token contract shape"); err != nil {
				return err
			}
			return runVitestScenario("src/ui/extensions/themeOverrides.test.ts", "theme overrides and effects an override update also rejects an unknown token")
		})
		sc.Step(`^a streaming browser response and a WebSocket endpoint$`, func() {})
		sc.Step(`^the extension reads chunks and its lifetime ends$`, func() {})
		sc.Step(`^chunks arrive incrementally and the socket closes$`, func() error {
			if err := runVitestScenario("src/ui/extensions/browserNetwork.test.ts", "browserFetch returns the native response and exposes chunks as they arrive"); err != nil {
				return err
			}
			return runVitestScenario("src/ui/extensions/browserNetwork.test.ts", "webSocket closes on lifetime abort and removes every abort listener")
		})
		sc.Step(`^an enabled extension with a stylesheet$`, func() {})
		sc.Step(`^the initial extension load starts$`, func() {})
		sc.Step(`^the stylesheet is installed before the app mounts, without waiting for activation$`, func() error {
			return runVitestScenario("src/ui/extensions/ExtensionsProvider.test.tsx", "the first App commit waits for extension CSS, not module activation")
		})
		sc.Step(`^host and extension controls in the composer$`, func() {})
		sc.Step(`^an alter pass retitles, reorders and hides entries by namespaced key$`, func() {})
		sc.Step(`^the mixed control list follows the pass and conditional controls stay live$`, func() error {
			return runVitestScenario("src/ui/chat/Composer.extensions.test.tsx", "composer host controls in the extension pipeline mixed controls alter by namespaced key and follow conditional props")
		})
		sc.Step(`^nested schema-driven Settings rows and row alter passes$`, func() {})
		sc.Step(`^a pass hides, retitles and reorders rows$`, func() {})
		sc.Step(`^drawing and search agree and configuration values stay untouched$`, func() error {
			if err := runVitestScenario("src/ui/extensions/settingsRowAlter.test.tsx", "Settings row alter passes hide removes a nested row and its search entry without deleting its value"); err != nil {
				return err
			}
			return runVitestScenario("src/ui/extensions/settingsRowAlter.test.tsx", "Settings row alter passes retitle and reorder apply to nested objects and list item fields")
		})
		sc.Step(`^an enabled package with a manifest theme and its CSS$`, func() {})
		sc.Step(`^the operator selects the theme and the package disappears$`, func() {})
		sc.Step(`^the dynamic theme applies and the app falls back to the built-in dark theme$`, func() error {
			if err := runVitestScenario("src/ui/extensions/themeRegistry.test.ts", "ExtensionThemeRegistry applies another dynamic theme on demand and removes it for a built-in"); err != nil {
				return err
			}
			return runVitestScenario("src/ui/extensions/themeRegistry.test.ts", "ExtensionThemeRegistry falls back to dark and rewrites the cookie when the selection is revoked")
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("extension contributions feature failed")
	}
}
