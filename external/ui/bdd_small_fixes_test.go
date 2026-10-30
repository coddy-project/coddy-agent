//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// The five fixes of issue #368 are properties of rendered components and of
// the stylesheet, so each step runs the Vitest test that renders or reads them.
func TestWebUISmallFixesFeature(t *testing.T) {
	steps := []struct{ step, file, name string }{
		{`^the head and the usage of the context window keep the edges of its bar$`,
			"src/ui/chat/contextBreakdownPopoverCss.test.ts",
			"the head and the usage keep the meter's edges, and the divider spans the panel"},
		{`^the title of the context window is spelled like the head of every other window$`,
			"src/ui/chat/contextBreakdownPopoverCss.test.ts",
			"the title of the context window is spelled like the head of every other window"},
		{`^the percent and the tokens stand over the bar and compaction at the foot of the context window$`,
			"src/ui/chat/ContextBreakdownPopover.test.tsx",
			"context popover uses modal head chrome, the percent and the tokens over the bar and compaction at its foot"},
		{`^every head of a window, a panel or a popover takes one height$`,
			"src/ui/panelHeadCss.test.ts",
			"every head of a window, a panel or a popover takes one height"},
		{`^a tooltip opens on hover only where there is a real pointer$`,
			"src/ui/tooltipTouchCss.test.ts",
			"a tooltip opens on hover only where there is a real pointer"},
		{`^a tooltip opens by focus only for a keyboard$`,
			"src/ui/tooltipTouchCss.test.ts",
			"a tooltip opens by focus only for a keyboard"},
		{`^the Russian dictionary keeps the project's wording$`,
			"src/ui/i18n/ruWording.test.ts",
			"the Russian dictionary keeps the project's wording"},
		{`^the worktree chip of the Russian composer reads worktree$`,
			"src/ui/chat/WorkspaceChips.test.tsx",
			"WorkspaceChips localizes workspace controls in Russian"},
		{`^the usage banner is an opaque plate in every theme$`,
			"src/ui/chat/usageBannerCss.test.ts",
			".usage-banner is an opaque plate mixed into the theme canvas"},
		{`^a chat at its newest message stays there when the banner rises over the composer$`,
			"src/ui/chat/ChatScreen.test.tsx",
			"a banner rising over the composer keeps a transcript at the newest message there"},
		{`^on a tablet the documentation search and page stretch across the sheet$`,
			"src/ui/docs/docsReaderCss.test.ts",
			"on the stacked shell the search and the page stretch across the sheet"},
		{`^a wide tablet shows On this page beside the page$`,
			"src/ui/docs/docsReaderCss.test.ts",
			"a wide tablet shows On this page beside the page"},
		{`^a narrow tablet folds On this page into a button above the page$`,
			"src/ui/docs/DocsView.test.tsx",
			"DocsView folds On this page into a button that a section link closes"},
		{`^on the desktop the documentation search ends where the text does$`,
			"src/ui/docs/docsReaderCss.test.ts",
			"on the desktop the header uses the page's own columns"},
		{`^the pages of the contents are indented under their group title$`,
			"src/ui/docs/docsReaderCss.test.ts",
			"the pages of the contents are indented under their group title"},
		{`^a read with an offset and a limit shows the lines next to the path$`,
			"src/ui/messages/ToolCallMessage.test.tsx",
			"a read of part of a file shows the lines next to the path, like a mention"},
		{`^a read of the whole file shows the path alone$`,
			"src/ui/messages/ToolCallMessage.test.tsx",
			"a read of the whole file shows the path alone, as before"},
		{`^an archived row leaves History at once and the list is not read again$`,
			"src/ui/App.archiveSession.test.tsx",
			"archiving from History takes the row out at once and keeps the rows scrolling loaded"},
		{`^a refused archive puts the row back where it stood, with a note on it$`,
			"src/ui/App.archiveSession.test.tsx",
			"a refused archive puts the row back where it was and says so on it"},
		{`^the next page after an archive skips no conversation$`,
			"src/ui/App.archiveSession.test.tsx",
			"the page after an archive starts where the list now ends, so nothing is skipped"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_small_fixes",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for _, s := range steps {
				file, name := s.file, s.name
				sc.Step(s.step, func() error { return runVitestScenario(file, name) })
			}
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_small_fixes.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI small fixes feature failed")
	}
}
