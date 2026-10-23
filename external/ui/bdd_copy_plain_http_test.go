//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// Copying on a page without navigator.clipboard (issue #566) is a property of
// rendered controls and of the helper's DOM calls, so each step runs the Vitest
// test that renders the control with the clipboard API removed and reads what
// the page copied. jsdom has no iOS; the same controls are tapped in WebKit and
// Chromium by npm run check:overflow.
func TestWebUICopyPlainHTTPFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^a code block copies and says Copied only when that worked$`,
			"src/ui/markdown/Markdown.test.tsx",
			"a code block copies without a clipboard API and says Copied only when that worked"},
		{`^inline code copies and says Copied only when that worked$`,
			"src/ui/markdown/Markdown.test.tsx",
			"inline code copies without a clipboard API and says Copied only when that worked"},
		{`^an inline formula copies and says Copied only when that worked$`,
			"src/ui/markdown/DiagramsAndMath.test.tsx",
			"formulas an inline formula copies without a clipboard API and says Copied only when that worked"},
		{`^the copy button of a message copies$`,
			"src/ui/messages/MessageCopyIconButton.test.tsx",
			"message copy works without a clipboard API, refused or not"},
		{`^a skill and a mention in a sent message copy$`,
			"src/ui/messages/UserMessage.test.tsx",
			"the message, a skill and a mention copy without a clipboard API"},
		{`^the copy items of the artefact menu copy$`,
			"src/ui/messages/ToolArtifacts.test.tsx",
			"the copy items of the menu work without a clipboard API"},
		{`^the path of a changed file copies and says so only when that worked$`,
			"src/ui/changes/EditsView.test.tsx",
			"the header copies the path without a clipboard API and says so only when that worked"},
		{`^Copy path of the Files window copies$`,
			"src/ui/files/FilesView.test.tsx",
			"Copy path works without a clipboard API"},
		{`^the System page copies the config path and says Copied only when that worked$`,
			"src/ui/settings/SystemSection.test.tsx",
			"this installation without a clipboard API it copies through the page and says Copied only when that worked"},
		{`^the helper copies inside the tap from a read-only 16px selection$`,
			"src/ui/clipboard.test.ts",
			"without a clipboard API it copies inside the tap, from a read-only 16px selection"},
		{`^no component copies text by itself$`,
			"src/ui/clipboard.test.ts",
			"no component copies text by itself"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_copy_plain_http",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a page that has no clipboard API$`, func() error { return nil })
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_copy_plain_http.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI copy on plain HTTP feature failed")
	}
}
