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
				if err := runVitestScenario(layout, "phone composer (max-width: 520px) the selector chips are one sideways-scrolling strip beside the send button"); err != nil {
					return err
				}
				return runVitestScenario(layout, "phone composer (max-width: 520px) the send button and the context ring never shrink")
			})
			sc.Step(`^the context chips scroll sideways in one strip beside the improve-prompt button$`, func() error {
				if err := runVitestScenario(composer, "the context chips sit in their own strip and the enhance button stays outside it"); err != nil {
					return err
				}
				return runVitestScenario(layout, "phone composer (max-width: 520px) the context chips are one sideways-scrolling strip and do not squeeze")
			})
			sc.Step(`^the composer text is large enough that iOS Safari does not zoom into it$`, func() error {
				return runVitestScenario(layout, "text fields do not make iOS Safari zoom the composer and its highlight mirror are 16px together")
			})
			sc.Step(`^the brand gives way and the top bar icons never slide over it$`, func() error {
				return runVitestScenario(layout, "phone top bar (max-width: 520px) the brand is what gives way: it may shrink and clips, the icons never slide over it")
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
