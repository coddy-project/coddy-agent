//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_mini_app.feature: Bot.Start
// against the fake Bot API of internal/tgfake with mini_app.url set, a
// scripted agent behind the session runner, and the gateway's session file on
// disk so the second start of the last scenario reads what the first wrote.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

const miniAppChat = int64(4242)

type miniAppWorld struct {
	f       *fakeAPI
	runner  *scriptedRunner
	dir     string
	appURL  string
	bot     *Bot
	cancel  context.CancelFunc
	done    chan error
	setOnce int // setChatMenuButton calls the first run made
}

func (w *miniAppWorld) fakeAndGateway(app string) error {
	w.f = openFakeAPI(tgfake.Options{BotUsername: "coddy_mini_bot"})
	dir, err := os.MkdirTemp("", "coddy-tg-miniapp-")
	if err != nil {
		return err
	}
	w.dir = dir
	w.appURL = app
	w.runner = newScriptedRunner()
	return nil
}

func (w *miniAppWorld) start(app string) error {
	base, _, err := logger.New(config.Logger{Level: config.LogLevelError, Format: config.LogFormatText, Outputs: []string{config.LogOutputStderr}})
	if err != nil {
		return err
	}
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "123456:miniapp", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
		MiniApp: config.TelegramMiniAppConfig{URL: app},
		// Only the bot's admins open the web UI from the chat.
		Admins: []int64{miniAppChat},
	}, w.runner, w.dir, logger.Component(base, logger.ComponentGatewayTelegram), filepath.Join(w.dir, "gateway_sessions.json"), nil)
	w.bot.apiBase = w.f.srv.URL
	polls := len(w.f.fake.Calls("getUpdates"))
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan error, 1)
	go func() { w.done <- w.bot.Start(ctx) }()
	if !w.f.fake.WaitCall("getUpdates", polls+1, pollingSettle) {
		return fmt.Errorf("the bot never polled; calls: %v", w.f.fake.Calls(""))
	}
	return nil
}

func (w *miniAppWorld) stop() error {
	if w.cancel == nil {
		return nil
	}
	w.cancel()
	select {
	case <-w.done:
	case <-time.After(pollingStopFor):
		return fmt.Errorf("Start did not return within %v of the stop", pollingStopFor)
	}
	w.cancel = nil
	return nil
}

func (w *miniAppWorld) close() {
	_ = w.stop()
	if w.f != nil {
		w.f.close()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
}

func (w *miniAppWorld) botStarts() error { return w.start(w.appURL) }

func (w *miniAppWorld) menuButtonOpens(app string) error {
	menu := w.f.fake.MenuButton(0)
	if menu.Type != "web_app" || menu.WebApp == nil || menu.WebApp.URL != app {
		return fmt.Errorf("the bot's menu button is %+v, want a web_app button to %s", menu, app)
	}
	return nil
}

// await polls the chat until check holds.
func (w *miniAppWorld) await(what string, check func(tgfake.ChatView) bool) error {
	deadline := time.Now().Add(pollingSettle)
	for {
		view := w.f.fake.Chat(miniAppChat)
		if check(view) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s; the chat holds:\n%s", what, view.Text())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (w *miniAppWorld) personTalked() error {
	w.f.fake.InjectMessage(tgfake.IncomingMessage{ChatID: miniAppChat, UserID: miniAppChat, Text: "hello"})
	return w.await("the bot never answered hello", func(v tgfake.ChatView) bool {
		for _, m := range v.Messages {
			if m.From == "bot" && strings.Contains(m.Text, "plain answer") {
				return true
			}
		}
		return false
	})
}

func (w *miniAppWorld) personSends(text string) error {
	w.f.fake.InjectMessage(tgfake.IncomingMessage{ChatID: miniAppChat, UserID: miniAppChat, Text: text})
	return nil
}

func (w *miniAppWorld) answersWithSessionButton(app string) error {
	key := sessionstore.SessionKey(adapterName, miniAppChat, miniAppChat, config.IsolationIndividual, false)
	session := w.bot.store.Peek(key)
	if session == "" {
		return fmt.Errorf("the chat has no session")
	}
	want := app + "?session=" + url.QueryEscape(session)
	return w.await("no button opening "+want+" as a Mini App", func(v tgfake.ChatView) bool {
		for _, m := range v.Messages {
			for _, row := range m.Keyboard {
				for _, b := range row {
					if b.WebApp != nil && b.WebApp.URL == want {
						return true
					}
				}
			}
		}
		return false
	})
}

func (w *miniAppWorld) ranOnceAndSetMenuButton() error {
	if err := w.start(w.appURL); err != nil {
		return err
	}
	if err := w.menuButtonOpens(w.appURL); err != nil {
		return err
	}
	w.setOnce = len(w.f.fake.Calls("setChatMenuButton"))
	return w.stop()
}

func (w *miniAppWorld) startsWithoutMiniApp() error { return w.start("") }

func (w *miniAppWorld) menuButtonShowsCommands() error {
	if !w.f.fake.WaitCall("setChatMenuButton", w.setOnce+1, pollingSettle) {
		return fmt.Errorf("the bot never put its menu button back; menu button: %+v", w.f.fake.MenuButton(0))
	}
	if menu := w.f.fake.MenuButton(0); menu.Type != "commands" {
		return fmt.Errorf("the bot's menu button is %+v, want its commands", menu)
	}
	return nil
}

func TestGatewayTelegramMiniAppFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "gateway_telegram_mini_app",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &miniAppWorld{}
			sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
				w.close()
				return ctx, err
			})
			sc.Given(`^a fake Bot API and a gateway whose Mini App is (\S+)$`, w.fakeAndGateway)
			sc.When(`^the bot starts$`, w.botStarts)
			sc.Given(`^the bot is running$`, w.botStarts)
			sc.Then(`^the bot's menu button opens (\S+) as a Mini App$`, w.menuButtonOpens)
			sc.Given(`^the person has talked to the bot$`, w.personTalked)
			sc.When(`^the person sends (/\w+)$`, w.personSends)
			sc.Then(`^the bot answers with a button that opens the chat's session in (\S+) as a Mini App$`, w.answersWithSessionButton)
			sc.Given(`^the bot has run once and set its menu button$`, w.ranOnceAndSetMenuButton)
			sc.When(`^the bot starts again without a Mini App$`, w.startsWithoutMiniApp)
			sc.Then(`^the bot's menu button shows its commands again$`, w.menuButtonShowsCommands)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../../features/gateway_telegram_mini_app.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("gateway_telegram_mini_app feature failed")
	}
}
