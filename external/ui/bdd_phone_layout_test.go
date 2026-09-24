//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestWebUIPhoneFeature(t *testing.T) {
	const composer = "src/ui/chat/Composer.test.tsx"
	const layout = "src/ui/phoneLayoutCss.test.ts"
	const nav = "src/ui/nav/NavRail.test.tsx"
	const grid = "src/ui/layoutGridCss.test.ts"
	const breakpoints = "src/ui/shellBreakpoint.test.ts"
	const wrap = "src/ui/messages/transcriptWrapCss.test.ts"
	const toolRow = "src/ui/messages/ToolCallMessage.test.tsx"
	suite := godog.TestSuite{
		Name: "web_ui_phone",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^in a narrow desktop window Enter sends the draft$`, func() error {
				return runVitestScenario(composer, "a narrow desktop window: Enter sends")
			})
			sc.Step(`^Shift\+Enter leaves the newline to the browser$`, func() error {
				return runVitestScenario(composer, "Shift+Enter leaves the newline to the browser and does not send")
			})
			sc.Step(`^Ctrl\+Enter inserts a newline at the caret instead of sending$`, func() error {
				return runVitestScenario(composer, "Ctrl+Enter inserts a newline at the caret and does not send")
			})
			sc.Step(`^on a touch-only phone Return inserts a newline and the Send button sends$`, func() error {
				return runVitestScenario(composer, "a touch-only phone: Return inserts a newline and the Send button sends")
			})
			sc.Step(`^the on-screen keyboard labels its Enter key send, or enter on a touch-only phone$`, func() error {
				return runVitestScenario(composer, "the keyboard's Enter key is labelled send, or enter on a touch-only phone")
			})
			sc.Step(`^the selector chips scroll sideways in one strip and never run under Send$`, func() error {
				if err := runVitestScenario(layout, "phone composer the selector chips are one sideways-scrolling strip beside the send button"); err != nil {
					return err
				}
				return runVitestScenario(layout, "phone composer the send button and the context ring never shrink")
			})
			sc.Step(`^the context chips scroll sideways in one strip beside the improve-prompt button$`, func() error {
				if err := runVitestScenario(composer, "the context chips sit in their own strip and the enhance button stays outside it"); err != nil {
					return err
				}
				return runVitestScenario(layout, "phone composer the context chips are one sideways-scrolling strip and do not squeeze")
			})
			sc.Step(`^the composer text is large enough that iOS Safari does not zoom into it$`, func() error {
				return runVitestScenario(layout, "text fields do not make iOS Safari zoom the composer and its highlight mirror are 16px together")
			})
			sc.Step(`^on a touch-only device opening the start screen or a chat leaves the composer unfocused$`, func() error {
				return runVitestScenario(composer, "a touch-only device keeps the keyboard closed on the start screen and in a chat")
			})
			sc.Step(`^a narrow desktop window still focuses the composer$`, func() error {
				return runVitestScenario(composer, "a narrow desktop window still focuses the field: it has a keyboard")
			})
			sc.Step(`^the composer block and its scroll-to-bottom button rise above an overlaying keyboard$`, func() error {
				return runVitestScenario("src/ui/chat/ChatScreen.test.tsx", "on the stacked shell the composer block rises above an overlaying keyboard")
			})
			sc.Step(`^the scroll-to-bottom button never moves the chat up$`, func() error {
				return runVitestScenario("src/ui/chat/ChatScreen.test.tsx", "the scroll-to-bottom button never moves the transcript up")
			})
			sc.Step(`^the brand gives way and the top bar icons never slide over it$`, func() error {
				return runVitestScenario(layout, "phone top bar the brand is what gives way: it may shrink and clips, the icons never slide over it")
			})
			sc.Step(`^a phone bar short of room keeps History, folds the rest behind More and lists sign-out last$`, func() error {
				return runVitestScenario(nav, "NavRail on a phone: the More menu folds what does not fit behind More, sign-out last under a separator")
			})
			sc.Step(`^picking a folded item opens it and closes the menu$`, func() error {
				return runVitestScenario(nav, "NavRail on a phone: the More menu picking a folded item opens it and closes the menu")
			})
			sc.Step(`^the start screen never widens the page$`, func() error {
				return runVitestScenario(layout, "the start screen never widens the page the hero column is a track that cannot grow past its container")
			})
			sc.Step(`^the phone tier ends at 599px and the tablet tier starts at 600px$`, func() error {
				if err := runVitestScenario(breakpoints, "the phone tier is the compact window class: up to 599px"); err != nil {
					return err
				}
				return runVitestScenario(breakpoints, "the tiers come in order: phone, tablet, desktop, wide")
			})
			sc.Step(`^every width the stylesheet asks about is an edge of the grid or a threshold it lists$`, func() error {
				for _, name := range []string{
					"the layout grid every width query of the stylesheet is a tier edge or a listed component threshold",
					"the layout grid DESIGN.md names every tier edge the stylesheet uses, and every threshold it lists is in use",
					"the layout grid the code asks for a width only through shellBreakpoint.ts",
				} {
					if err := runVitestScenario(grid, name); err != nil {
						return err
					}
				}
				return nil
			})
			sc.Step(`^a settings tile on a phone spells its whole name$`, func() error {
				return runVitestScenario(layout, "phone settings a section tile spells its whole name: the title wraps to two lines instead of an ellipsis")
			})
			sc.Step(`^a tool row named after an MCP tool wraps its label inside the row and moves its target and duration under it together$`, func() error {
				for _, name := range []string{
					"a tool row never widens the transcript the label keeps its line while it fits, and wraps inside the row when it does not",
					"a tool row never widens the transcript the head wraps, so what trails a full-width label moves under it",
					"a tool row never widens the transcript what trails the label moves as one group, and only when the label's line lacks the room it needs",
				} {
					if err := runVitestScenario(wrap, name); err != nil {
						return err
					}
				}
				return runVitestScenario(toolRow, "the target, the failure marker and the duration trail the label as one group")
			})
			sc.Step(`^a long link or identifier in an answer breaks instead of widening the page$`, func() error {
				if err := runVitestScenario(wrap, "an answer never widens the transcript a long link or word in the prose breaks where it has to"); err != nil {
					return err
				}
				return runVitestScenario(wrap, "an answer never widens the transcript inline code is at most a line wide and wraps inside its chip")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/web_ui_phone.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("web UI phone feature failed")
	}
}
