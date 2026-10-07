package tgfake

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/cucumber/godog"
)

func TestFakeMiniAppFeature(t *testing.T) {
	const token = "123456:MINIAPP"
	suite := godog.TestSuite{
		Name: "Telegram stand Mini Apps",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			var s *stand
			var launched LaunchedWebApp
			sc.Given(`^an offline Telegram stand a bot has called with its token$`, func() {
				s = newStand(t, Options{})
				s.callToken(token, "getMe", nil)
			})
			sc.When(`^the bot sends the person a button that opens (\S+) as a Mini App$`, func(app string) error {
				_, body := s.callToken(token, "sendMessage", url.Values{"chat_id": {"4242"}, "text": {"This conversation in the web UI"},
					"reply_markup": {`{"inline_keyboard":[[{"text":"Open in Coddy","web_app":{"url":"` + app + `"}}]]}`}})
				if body["ok"] != true {
					return fmt.Errorf("the button was refused: %v", body)
				}
				return nil
			})
			sc.When(`^the person opens that button$`, func() error {
				msgs := s.fake.Chat(4242).Messages
				if len(msgs) == 0 || len(msgs[len(msgs)-1].Keyboard) == 0 || msgs[len(msgs)-1].Keyboard[0][0].WebApp == nil {
					return fmt.Errorf("no web_app button in the chat:\n%s", s.fake.Chat(4242).Text())
				}
				var err error
				launched, err = s.fake.LaunchWebApp(WebAppLaunch{ChatID: 4242, URL: msgs[len(msgs)-1].Keyboard[0][0].WebApp.URL})
				return err
			})
			sc.Then(`^the app is given (\S+) with Telegram's launch parameters in the fragment$`, func(app string) error {
				base, params := launchFragment(t, launched.URL)
				if base != app {
					return fmt.Errorf("the app was given %q, want %q", base, app)
				}
				if params.Get("tgWebAppData") == "" || params.Get("tgWebAppVersion") == "" {
					return fmt.Errorf("launch parameters missing from %q", launched.URL)
				}
				return nil
			})
			sc.Then(`^the launch data checks out against the bot's token$`, func() {
				checkInitData(t, token, launched.InitData)
			})
			sc.When(`^the bot sets its menu button to open (\S+)$`, func(app string) error {
				_, body := s.callToken(token, "setChatMenuButton", url.Values{
					"menu_button": {`{"type":"web_app","text":"Coddy","web_app":{"url":"` + app + `"}}`}})
				if body["ok"] != true {
					return fmt.Errorf("the menu button was refused: %v", body)
				}
				return nil
			})
			sc.Then(`^the chat shows a menu button that opens (\S+)$`, func(app string) error {
				menu := s.fake.Chat(4242).MenuButton
				if menu == nil || menu.Type != "web_app" || menu.WebApp == nil || menu.WebApp.URL != app {
					return fmt.Errorf("the chat's menu button is %+v", menu)
				}
				return nil
			})
			sc.Then(`^opening the menu button gives a launch that checks out against the bot's token$`, func() error {
				var err error
				launched, err = s.fake.LaunchWebApp(WebAppLaunch{ChatID: 4242})
				if err != nil {
					return err
				}
				checkInitData(t, token, launched.InitData)
				return nil
			})
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/tgfake_mini_app.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("Telegram stand Mini App feature failed")
	}
}
