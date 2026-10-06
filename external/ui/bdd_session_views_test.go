//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The views of a chat are rendered components and the shell's routing, so
// each step runs the Vitest test that drives them: the header buttons, the
// dock without a tab strip, the Files window.
func TestWebUISessionViewsFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^the header shows edits, files and background tasks as buttons in a row$`,
			"src/ui/chat/ChatHeader.test.tsx",
			"the header shows edits, files and background tasks as buttons in a row"},
		{`^the Edits button is there only while the session has edits$`,
			"src/ui/chat/ChatScreen.test.tsx",
			"the Edits button is there only while the session has edits"},
		{`^a button opens its view$`,
			"src/ui/chat/ChatHeader.test.tsx",
			"a button opens its view"},
		{`^the button of the view on show is pressed$`,
			"src/ui/chat/ChatHeader.test.tsx",
			"the button of the view on show is pressed"},
		{`^the edits open in the dock headed Edits, with no tab strip$`,
			"src/ui/App.workspaceViews.test.tsx",
			"the edits open in the dock, headed Edits, with no tab strip"},
		{`^the background tasks open in the dock with no tab strip$`,
			"src/ui/App.workspaceViews.test.tsx",
			"the background tasks open in the dock with no tab strip"},
		{`^the files open in a window over the chat, not in the dock$`,
			"src/ui/App.workspaceViews.test.tsx",
			"the files open in a window over the chat, not in the dock, and Escape closes it"},
		{`^the window shows the workspace tree beside an empty preview$`,
			"src/ui/files/FilesView.test.tsx",
			"the files window shows the workspace tree beside an empty preview"},
		{`^a file picked in the tree opens in a tab, and a second one beside it$`,
			"src/ui/files/FilesView.test.tsx",
			"a file picked in the tree opens in a tab, and a second one beside it"},
		{`^the filter searches the whole workspace$`,
			"src/ui/files/FilesView.test.tsx",
			"the filter searches the whole workspace, not only the folders opened"},
		{`^a files address opens the window on its file$`,
			"src/ui/App.workspaceViews.test.tsx",
			"a files address opens the window on its file"},
		{`^Ctrl\+Shift\+F opens the files window and closes it$`,
			"src/ui/App.workspaceViews.test.tsx",
			"Ctrl+Shift+F opens the files window, and closes it"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_session_views",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_session_views.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI session views feature failed")
	}
}
