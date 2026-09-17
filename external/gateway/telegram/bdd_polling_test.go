//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_polling.feature: runs Bot.Start
// end to end - getMe, setMyCommands, the getUpdates loop - against the fake
// Bot API of internal/tgfake served on httptest, with a scripted agent behind
// the session runner. No LLM and no network beyond the local server.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

const (
	pollingChatID  = int64(4242)
	pollingUserID  = int64(4242)
	pollingSettle  = 5 * time.Second
	pollingStopFor = 10 * time.Second
)

// pollingRunner is the scripted agent of the identity feature with a model
// switch that lands: what the keyboard scenario needs from the session side.
type pollingRunner struct {
	*scriptedRunner
}

func newPollingRunner() *pollingRunner {
	r := &pollingRunner{scriptedRunner: newScriptedRunner()}
	r.cfg = &config.Config{
		Models: []config.ModelEntry{{Model: "openai/gpt-4o"}, {Model: "rpa/qwen3.6-35b-a3b"}},
		Agent:  config.Agent{Model: "openai/gpt-4o"},
	}
	return r
}

func (r *pollingRunner) HandleSessionSetConfigOption(_ context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.live[params.SessionID]
	if st == nil {
		return nil, fmt.Errorf("session not found: %s", params.SessionID)
	}
	if params.ConfigID != "model" {
		return nil, fmt.Errorf("unknown config option: %q", params.ConfigID)
	}
	for _, m := range r.cfg.Models {
		if m.Model == params.Value {
			st.SetSelectedModelID(params.Value)
			return &acp.SessionSetConfigOptionResult{}, nil
		}
	}
	return nil, fmt.Errorf("unknown model value: %q", params.Value)
}

// pollingWorld holds the fake, the bot and the run of Start.
type pollingWorld struct {
	runner     *pollingRunner
	f          *fakeAPI
	fake       *tgfake.Server // f.fake, named for the steps that read the chat
	bot        *Bot
	dir        string
	cancel     context.CancelFunc
	done       chan error
	lastUpdate int
}

func (w *pollingWorld) fakeBotAPI(username string) error {
	w.f = openFakeAPI(tgfake.Options{BotUsername: username})
	w.fake = w.f.fake
	return nil
}

func (w *pollingWorld) gatewayPointedAtIt() error {
	if w.f == nil {
		return fmt.Errorf("no fake Bot API to point at")
	}
	dir, err := os.MkdirTemp("", "coddy-tg-poll-")
	if err != nil {
		return err
	}
	w.dir = dir
	w.runner = newPollingRunner()
	base, _, err := logger.New(config.Logger{Level: config.LogLevelError, Format: config.LogFormatText, Outputs: []string{config.LogOutputStderr}})
	if err != nil {
		return err
	}
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "123456:polling", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	}, w.runner, dir, logger.Component(base, logger.ComponentGatewayTelegram), "", nil)
	// The same origin the operator would export as CODDY_TELEGRAM_API_BASE.
	w.bot.apiBase = w.f.srv.URL
	return nil
}

func (w *pollingWorld) subscribedToMessagesOnly() error {
	w.fake.SetAllowedUpdates([]string{"message", "edited_message"})
	return nil
}

func (w *pollingWorld) agentAnswersWith(answer string) error {
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	w.runner.answer = answer
	return nil
}

func (w *pollingWorld) botStarted() error {
	if w.done != nil {
		return fmt.Errorf("the bot is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan error, 1)
	go func() { w.done <- w.bot.Start(ctx) }()
	// Start is up once the first poll is in: everything before it is
	// synchronous, and a step that asserts on a call the bot has not made
	// yet would race the goroutine.
	if !w.fake.WaitCall("getUpdates", 1, pollingSettle) {
		select {
		case err := <-w.done:
			return fmt.Errorf("Start returned before polling: %v", err)
		default:
		}
		return fmt.Errorf("the bot never polled getUpdates; calls: %s", w.methods())
	}
	return nil
}

func (w *pollingWorld) methods() string {
	var names []string
	for _, c := range w.fake.Calls("") {
		names = append(names, c.Method)
	}
	return strings.Join(names, ",")
}

func (w *pollingWorld) botAPIReceived(method string) error {
	if !w.fake.WaitCall(method, 1, pollingSettle) {
		return fmt.Errorf("the Bot API never received %s; calls: %s", method, w.methods())
	}
	return nil
}

func (w *pollingWorld) botKnowsItselfAs(name string) error {
	if w.bot.botName != name {
		return fmt.Errorf("bot name = %q, want %q", w.bot.botName, name)
	}
	return nil
}

func (w *pollingWorld) userSends(text string) error {
	upd, _ := w.fake.InjectMessage(tgfake.IncomingMessage{ChatID: pollingChatID, UserID: pollingUserID, Text: text})
	w.lastUpdate = upd
	return nil
}

// await polls the chat until check holds or the settle time passes.
func (w *pollingWorld) await(what string, check func(tgfake.ChatView) bool) error {
	deadline := time.Now().Add(pollingSettle)
	for {
		view := w.fake.Chat(pollingChatID)
		if check(view) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s; chat:\n%s\ncalls: %s", what, view.Text(), w.methods())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (w *pollingWorld) chatShowsBotMessage(want string) error {
	return w.await(fmt.Sprintf("no bot message contains %q", want), func(v tgfake.ChatView) bool {
		for _, m := range v.Messages {
			if m.From == "bot" && !m.Deleted && strings.Contains(m.Text, want) {
				return true
			}
		}
		return false
	})
}

func (w *pollingWorld) nextPollConfirms() error {
	want := strconv.Itoa(w.lastUpdate + 1)
	deadline := time.Now().Add(pollingSettle)
	for {
		calls := w.fake.Calls("getUpdates")
		if len(calls) > 0 && calls[len(calls)-1].Params["offset"] == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no getUpdates carried offset=%s after %d polls", want, len(calls))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (w *pollingWorld) userTapsButton(label string) error {
	if err := w.await(fmt.Sprintf("no keyboard offers %q", label), func(v tgfake.ChatView) bool {
		_, _, ok := v.FindButton(label)
		return ok
	}); err != nil {
		return err
	}
	upd, _, err := w.fake.InjectCallback(tgfake.IncomingCallback{ChatID: pollingChatID, UserID: pollingUserID, Label: label})
	if err != nil {
		return err
	}
	w.lastUpdate = upd
	return nil
}

func (w *pollingWorld) keyboardMarksCurrent(model string) error {
	return w.await(fmt.Sprintf("no edited keyboard marks %q as current", model), func(v tgfake.ChatView) bool {
		for _, m := range v.Messages {
			if !m.Edited {
				continue
			}
			for _, row := range m.Keyboard {
				for _, b := range row {
					if b.Text == "✓ "+model {
						return true
					}
				}
			}
		}
		return false
	})
}

func (w *pollingWorld) sessionModelIs(model string) error {
	deadline := time.Now().Add(pollingSettle)
	for {
		w.runner.mu.Lock()
		found := false
		for _, st := range w.runner.live {
			if st.GetSelectedModelID() == model {
				found = true
			}
		}
		w.runner.mu.Unlock()
		if found {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no session carries model %q", model)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (w *pollingWorld) botStopped() error {
	if w.cancel == nil {
		return fmt.Errorf("the bot was never started")
	}
	w.cancel()
	return nil
}

func (w *pollingWorld) startReturnedClean() error {
	select {
	case err := <-w.done:
		w.done = nil
		if err != nil {
			return fmt.Errorf("Start returned %v", err)
		}
		return nil
	case <-time.After(pollingStopFor):
		return fmt.Errorf("Start did not return within %v of the stop", pollingStopFor)
	}
}

func (w *pollingWorld) close() {
	if w.cancel != nil {
		w.cancel()
	}
	if w.done != nil {
		select {
		case <-w.done:
		case <-time.After(pollingStopFor):
		}
	}
	if w.f != nil {
		w.f.close()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
}

func initializePollingScenario(sc *godog.ScenarioContext) {
	w := &pollingWorld{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.close()
		return ctx, err
	})

	sc.Given(`^a fake Bot API whose bot is "([^"]*)"$`, w.fakeBotAPI)
	sc.Given(`^a telegram gateway over a scripted agent pointed at it$`, w.gatewayPointedAtIt)
	sc.Given(`^the agent answers with "([^"]*)"$`, w.agentAnswersWith)
	sc.Given(`^the Bot API remembers a subscription to messages only$`, w.subscribedToMessagesOnly)

	sc.When(`^the bot is started$`, w.botStarted)
	sc.When(`^the user sends "([^"]*)"$`, w.userSends)
	sc.When(`^the user taps the button for "([^"]*)"$`, w.userTapsButton)
	sc.When(`^the bot is stopped$`, w.botStopped)

	sc.Then(`^the Bot API received "([^"]*)"$`, w.botAPIReceived)
	sc.Then(`^the bot knows itself as "([^"]*)"$`, w.botKnowsItselfAs)
	sc.Then(`^the chat shows a bot message containing "([^"]*)"$`, w.chatShowsBotMessage)
	sc.Then(`^the next poll confirms that update$`, w.nextPollConfirms)
	sc.Then(`^the keyboard message marks "([^"]*)" as current$`, w.keyboardMarksCurrent)
	sc.Then(`^the session model is "([^"]*)"$`, w.sessionModelIs)
	sc.Then(`^Start returned without error$`, w.startReturnedClean)
}

func TestGatewayTelegramPollingFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway telegram polling",
		ScenarioInitializer: initializePollingScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_telegram_polling.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("gateway telegram polling feature suite failed")
	}
}
