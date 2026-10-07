//go:build http && ui

package ui

import (
	"testing"

	"github.com/cucumber/godog"
)

// Godog harness for features/web_ui_telegram_mini_app.feature: every step is
// a vitest test of the SPA's Telegram module (ui/telegram) or of the sign-in
// screen, run by name.
func TestWebUITelegramMiniAppFeature(t *testing.T) {
	const tg = "src/ui/telegram/telegram.test.tsx"
	const auth = "src/ui/auth/AuthGate.test.tsx"
	steps := map[string][]string{
		`^a launch in the fragment or the query makes the tab a Mini App, and a start parameter alone does not$`: {
			"launch parameters a launch is tgWebAppData or tgWebAppVersion, in the fragment or the query"},
		`^the address keeps its own route and loses Telegram's parameters$`: {
			"launch parameters the address keeps its own route and loses Telegram's parameters"},
		`^a reload inside the Mini App is still a Mini App$`: {
			"launch parameters capture keeps the launch for the tab and cleans the address before the router reads it"},
		`^the bot's link opens the chat's conversation, in a Mini App or a browser$`: {
			"launch parameters the bot's link and a session start parameter open that conversation"},
		`^events go out through the client's proxy and come back through receiveEvent$`: {
			"bridge the native apps' proxy carries events out and receiveEvent brings them in"},
		`^in a web client's frame events go to the parent and only the parent is heard$`: {
			"bridge in a web client's iframe events go to the parent, and only the parent is heard",
			"bridge the parent's origin is the browser's record of it, else the page the frame came from, else Telegram Web"},
		`^the window is expanded, swipes are off, the header takes the theme's colours and ready goes last$`: {
			"the Mini App window the start sequence: requests, expand, swipes off, colours, back button, ready last",
			"the Mini App window the header takes the theme's colours, again when the theme changes"},
		`^the visible, stable and hidden heights follow the client and the safe areas add up$`: {
			"the Mini App window viewport events set the visible, stable and hidden heights; insets are summed"},
		`^the docked composer and the sheets are lifted by the keyboard or the hidden part, whichever is more$`: {
			"telegram.css the docked composer and the sheets are lifted by the keyboard or the hidden part, whichever is more",
			"the Mini App window a change of what Telegram hides is announced as a resize, once per change"},
		`^the page never clips its own overflow and the top inset is set where the stacked shell sets it$`: {
			"telegram.css the page never clips its own overflow: the stacked shell's sticky chrome needs body to scroll the viewport",
			"telegram.css the top inset is set where the stacked shell sets it",
			"telegram.css every rule is scoped to a Mini App and to the stacked shell"},
		`^the theme follows Telegram until the user picks one$`: {
			"the Mini App window the theme follows Telegram until the user picks one"},
		`^the back button is an Escape for whatever answers it$`: {
			"the back button a press is an Escape for whatever answers it",
			"the back button a press closes the composer's picker sheet wherever the focus is",
			"the back button a press closes the image viewer wherever the focus is",
			"the back button the client hears when the button should show, and a press is answered"},
		`^an Escape nothing claimed leaves the conversation and never skips a question$`: {
			"the back button an Escape nothing claimed leaves the conversation and never reaches the question card"},
		`^outside Telegram nothing is installed or sent$`: {
			"the Mini App window outside Telegram nothing is installed and nothing is sent",
			"launch parameters an ordinary browser is not a Mini App and its address is left alone"},
		`^the page loads nothing from another origin$`: {
			"telegram.css nothing of Telegram is left in styles.css or index.html"},
	}
	suite := godog.TestSuite{
		Name: "web_ui_telegram_mini_app",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			for pattern, names := range steps {
				names := names
				sc.Step(pattern, func() error {
					for _, name := range names {
						if err := runVitestScenario(tg, name); err != nil {
							return err
						}
					}
					return nil
				})
			}
			sc.Step(`^a sign-in the server accepted but the frame did not keep says so and offers a tab of its own$`, func() error {
				return runVitestScenario(auth, "SignInScreen says the browser did not keep the sign-in when the session does not come back")
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/web_ui_telegram_mini_app.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("web_ui_telegram_mini_app feature failed")
	}
}
