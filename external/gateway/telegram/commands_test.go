//go:build gateway || gateway.telegram

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

// An id that fills callback_data exactly travels as itself; one byte more and
// it has to become a digest, because a truncated id resolves to nothing.
func TestModelCallbackValueSwitchesToADigestAtTheLimit(t *testing.T) {
	prefix := len(callbackActionModel) + 1 // "model:"
	exact := strings.Repeat("m", telegramCallbackDataMax-prefix)
	if got := modelCallbackValue(exact); got != exact {
		t.Fatalf("id that fits was rewritten: %q", got)
	}
	over := exact + "m"
	got := modelCallbackValue(over)
	if got == over {
		t.Fatal("id over the limit travelled verbatim")
	}
	if !strings.HasPrefix(got, callbackDigestMarker) {
		t.Fatalf("digest form %q does not carry the marker", got)
	}
	if prefix+len(got) > telegramCallbackDataMax {
		t.Fatalf("digest payload is %d bytes with the prefix, over the limit", prefix+len(got))
	}
}

func TestResolveModelCallback(t *testing.T) {
	long := strings.Repeat("n", 80)
	otherLong := strings.Repeat("o", 80)
	models := []config.ModelEntry{
		{Model: "openai/gpt-4o"},
		{Model: long},
		{Model: otherLong},
	}

	cases := []struct {
		name    string
		payload string
		want    string
		wantOK  bool
	}{
		{"configured id", "openai/gpt-4o", "openai/gpt-4o", true},
		{"digest of a long id", modelCallbackValue(long), long, true},
		{"digest of another long id", modelCallbackValue(otherLong), otherLong, true},
		{"id that is not configured", "openai/gpt-4o-mini", "", false},
		// A truncated id is what the old keyboard sent; nothing must match it.
		{"truncated id", long[:57], "", false},
		{"digest of a model since removed", modelCallbackValue(strings.Repeat("z", 80)), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveModelCallback(models, tc.payload)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("resolveModelCallback(%q) = (%q, %v), want (%q, %v)", tc.payload, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// Every button the keyboard offers has to be accepted back, whatever the id
// length, and none may exceed what Telegram will carry.
func TestModelKeyboardButtonsRoundTrip(t *testing.T) {
	models := []config.ModelEntry{
		{Model: "openai/gpt-4o"},
		{Model: "neuraldeep/qwen3-235b-a22b-instruct-2507-fp8-extended-context-preview"},
	}
	kb := buildModelKeyboard(models, models[0].Model)

	seen := 0
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData == nil {
				t.Fatal("button without callback data")
			}
			data := *btn.CallbackData
			if len(data) > telegramCallbackDataMax {
				t.Fatalf("callback_data is %d bytes: %q", len(data), data)
			}
			action, payload, ok := strings.Cut(data, ":")
			if !ok || action != callbackActionModel {
				t.Fatalf("unexpected callback data %q", data)
			}
			model, resolved := resolveModelCallback(models, payload)
			if !resolved {
				t.Fatalf("payload %q does not resolve back to a configured model", payload)
			}
			if want := strings.TrimPrefix(btn.Text, "✓ "); model != want {
				t.Fatalf("button %q carries model %q", btn.Text, model)
			}
			seen++
		}
	}
	if seen != len(models) {
		t.Fatalf("keyboard offered %d buttons, want %d", seen, len(models))
	}
}

// failingRunner is the stub runner with the failures a model tap can meet on
// the session side, switched on once the keyboard is in the chat.
type failingRunner struct {
	*stubRunner
	ensureErr error
	setErr    error
}

func (r *failingRunner) EnsureHTTPSession(ctx context.Context, sessionID, cwd string) (*session.State, error) {
	if r.ensureErr != nil {
		return nil, r.ensureErr
	}
	return r.stubRunner.EnsureHTTPSession(ctx, sessionID, cwd)
}

func (r *failingRunner) HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	if r.setErr != nil {
		return nil, r.setErr
	}
	return r.stubRunner.HandleSessionSetConfigOption(ctx, params)
}

// A model tap that fails says why in the chat, as a reply to the keyboard it
// came from. The query already has its one answer, the acknowledgement sent
// as the tap arrives, and Telegram refuses a second: an alert would never be
// seen.
func TestModelTapFailuresReplyInTheChat(t *testing.T) {
	cases := []struct {
		name  string
		spoil func(r *failingRunner)
		want  string
	}{
		{"session cannot be loaded", func(r *failingRunner) { r.ensureErr = errors.New("bundle unreadable") },
			"❌ Session error: bundle unreadable"},
		{"model dropped from the configuration", func(r *failingRunner) { r.cfg.Models = r.cfg.Models[:1] },
			"❌ That model is no longer configured."},
		{"manager refuses the model", func(r *failingRunner) { r.setErr = errors.New("model is not available") },
			"❌ model is not available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, tgfake.Options{})
			runner := &failingRunner{stubRunner: newStubRunner(&config.Config{
				Models: []config.ModelEntry{{Model: "openai/gpt-4o"}, {Model: "rpa/qwen3.6-35b-a3b"}},
				Agent:  config.Agent{Model: "openai/gpt-4o"},
			})}
			bot := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll}, runner, t.TempDir(),
				slog.New(slog.DiscardHandler), "", nil)
			key := sessionstore.SessionKey(adapterName, modelSwitchChatID, modelSwitchUserID, config.IsolationIndividual, false)
			bot.processMessage(t.Context(), f.api, f.userMessage(modelSwitchChatID, modelSwitchUserID, "/model"), key)
			tc.spoil(runner)
			cbq, err := f.tap(modelSwitchChatID, modelSwitchUserID, "rpa/qwen3.6-35b-a3b")
			if err != nil {
				t.Fatal(err)
			}
			bot.handleCallback(t.Context(), f.api, cbq)

			replies := f.repliesTo(modelSwitchChatID, cbq.Message.MessageID)
			if len(replies) != 1 || replies[0].Text != tc.want {
				t.Fatalf("replies to the keyboard = %+v, want one saying %q:\n%s", replies, tc.want, f.fake.Chat(modelSwitchChatID).Text())
			}
			requireOneAnswer(t, f, cbq.ID)
		})
	}
}

// The adapter's logger has to arrive tagged, or logger.levels naming
// gateway.telegram scopes nothing and the debug trail stays invisible.
func TestBotLoggerCarriesTheTelegramComponent(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b := New(&config.TelegramGatewayConfig{}, nil, "",
		logger.Component(base, logger.ComponentGatewayTelegram), "", nil)

	b.log.Debug("probe")

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}
	if got := rec[logger.ComponentKey]; got != logger.ComponentGatewayTelegram {
		t.Fatalf("component = %v, want %q", got, logger.ComponentGatewayTelegram)
	}
}

// newOriginTestBot builds a Bot over the scripted runner, with a store of its
// own, for the origin checks below.
func newOriginTestBot(t *testing.T) (*Bot, *scriptedRunner) {
	t.Helper()
	runner := newScriptedRunner()
	base, _, err := logger.New(config.Logger{
		Level:   config.LogLevelError,
		Format:  config.LogFormatText,
		Outputs: []string{config.LogOutputStderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	bot := New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t",
		DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	}, runner, t.TempDir(), logger.Component(base, logger.ComponentGatewayTelegram), "", nil)
	return bot, runner
}

// A gateway names only the conversations it starts. A chat key the store has
// seen before belongs to a session that was stamped when it began, and an id
// this gateway did not mint is somebody else's conversation to name - an empty
// origin means "not recorded", not "opened on this host", so the write-once
// guard in SetOrigin cannot tell those apart on its own.
func TestEnsureSessionStampsOnlyTheChatsItStarts(t *testing.T) {
	bot, _ := newOriginTestBot(t)

	first, err := bot.ensureSession(t.Context(), "chat:1")
	if err != nil {
		t.Fatal(err)
	}
	if got := first.GetOrigin(); got != session.GatewayOrigin("telegram") {
		t.Fatalf("a chat the gateway started has origin %q", got)
	}

	// A session that already exists under a key the gateway did not mint keeps
	// whatever it was: reaching it again must not relabel it.
	adopted := bot.store.Get("chat:2")
	local, err := bot.runner.EnsureHTTPSession(t.Context(), adopted, bot.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got := local.GetOrigin(); got != "" {
		t.Fatalf("a session created outside the gateway starts with origin %q", got)
	}
	again, err := bot.ensureSession(t.Context(), "chat:2")
	if err != nil {
		t.Fatal(err)
	}
	if got := again.GetOrigin(); got != "" {
		t.Fatalf("the gateway relabelled a session it did not start: origin %q", got)
	}
}

// --- the web UI as the bot's Mini App --------------------------------------

func TestMiniAppLinkCarriesTheSessionInTheQuery(t *testing.T) {
	for _, tc := range []struct{ base, session, want string }{
		{"https://coddy.example.com/", "sess_1", "https://coddy.example.com/?session=sess_1"},
		{"https://coddy.example.com", "sess_1", "https://coddy.example.com?session=sess_1"},
		{"https://coddy.example.com/coddy/?team=a", "sess_1", "https://coddy.example.com/coddy/?session=sess_1&team=a"},
		{"https://coddy.example.com/", "", "https://coddy.example.com/"},
	} {
		if got := miniAppLink(tc.base, tc.session); got != tc.want {
			t.Errorf("miniAppLink(%q, %q) = %q, want %q", tc.base, tc.session, got, tc.want)
		}
	}
}

func miniAppBot(t *testing.T, app string, store string) *Bot {
	t.Helper()
	return New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll, MiniApp: config.TelegramMiniAppConfig{URL: app}, Admins: []int64{4242}},
		newStubRunner(&config.Config{}), t.TempDir(), slog.New(slog.DiscardHandler), store, nil)
}

// lastKeyboard returns the first button of the newest bot message with a keyboard.
func lastButton(t *testing.T, f *fakeAPI, chatID int64) tgfake.InlineKeyboardButton {
	t.Helper()
	msgs := f.fake.Chat(chatID).Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].From == "bot" && len(msgs[i].Keyboard) > 0 {
			return msgs[i].Keyboard[0][0]
		}
	}
	t.Fatalf("no bot message with a keyboard:\n%s", f.fake.Chat(chatID).Text())
	return tgfake.InlineKeyboardButton{}
}

func TestAppCommandOpensTheChatsConversation(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	bot := miniAppBot(t, "https://coddy.example.com/", "")
	key := sessionstore.SessionKey(adapterName, 4242, 4242, config.IsolationIndividual, false)

	// No conversation yet: the start screen, and no session minted for it.
	bot.processMessage(t.Context(), f.api, f.userMessage(4242, 4242, "/app"), key)
	if b := lastButton(t, f, 4242); b.WebApp == nil || b.WebApp.URL != "https://coddy.example.com/" {
		t.Fatalf("/app before any conversation: %+v", b)
	}
	if got := bot.store.Peek(key); got != "" {
		t.Fatalf("/app minted session %q", got)
	}

	id := bot.store.Get(key)
	msg := f.userMessage(4242, 4242, "/app")
	bot.processMessage(t.Context(), f.api, msg, key)
	if b := lastButton(t, f, 4242); b.WebApp == nil || b.WebApp.URL != "https://coddy.example.com/?session="+id {
		t.Fatalf("/app in a private chat: %+v", b)
	}
	if replies := f.repliesTo(4242, msg.MessageID); len(replies) != 1 {
		t.Fatalf("/app should answer the message it was sent in, got %d replies", len(replies))
	}

	// A group gets a plain link: Telegram takes web_app buttons in private chats only.
	groupKey := sessionstore.SessionKey(adapterName, -100500, 4242, config.IsolationIndividual, true)
	groupID := bot.store.Get(groupKey)
	bot.processMessage(t.Context(), f.api, f.userMessage(-100500, 4242, "/app"), groupKey)
	if b := lastButton(t, f, -100500); b.WebApp != nil || b.URL != "https://coddy.example.com/?session="+groupID {
		t.Fatalf("/app in a group: %+v", b)
	}
}

func TestAppCommandWithoutAMiniAppNamesTheKey(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	bot := miniAppBot(t, "", "")
	key := sessionstore.SessionKey(adapterName, 4242, 4242, config.IsolationIndividual, false)
	msg := f.userMessage(4242, 4242, "/app")
	bot.processMessage(t.Context(), f.api, msg, key)
	replies := f.repliesTo(4242, msg.MessageID)
	if len(replies) != 1 || !strings.Contains(replies[0].Text, "gateways.telegram.mini_app.url") || replies[0].Keyboard != nil {
		t.Fatalf("/app without a Mini App: %+v", replies)
	}
}

func TestCommandListAndHelpNameAppOnlyWithAMiniApp(t *testing.T) {
	has := func(cfg *config.TelegramGatewayConfig) bool {
		for _, c := range botCommands(cfg) {
			if c.Command == "app" {
				return true
			}
		}
		return false
	}
	without := &config.TelegramGatewayConfig{}
	with := &config.TelegramGatewayConfig{MiniApp: config.TelegramMiniAppConfig{URL: "https://coddy.example.com/"}}
	if has(without) || !has(with) {
		t.Fatalf("setMyCommands lists /app: without a Mini App %v, with one %v", has(without), has(with))
	}
	if strings.Contains(helpText(without, "b"), "/app") || !strings.Contains(helpText(with, "b"), "/app") {
		t.Fatal("/help should name /app exactly when the web UI is the bot's Mini App")
	}
	if cmds := botCommands(with); cmds[len(cmds)-1].Command != "clear" {
		t.Fatalf("/clear stays last: %+v", cmds)
	}
}

// The bot takes back only the menu button it set itself, and puts back what
// it replaced.
func TestMenuButtonSyncTakesBackOnlyItsOwnButton(t *testing.T) {
	setCalls := func(f *fakeAPI) int { return len(f.fake.Calls("setChatMenuButton")) }
	setBotFatherButton := func(t *testing.T, f *fakeAPI, url string) {
		t.Helper()
		if _, err := f.api.MakeRequest("setChatMenuButton", map[string]string{
			"menu_button": `{"type":"web_app","text":"Mine","web_app":{"url":"` + url + `"}}`}); err != nil {
			t.Fatal(err)
		}
	}
	no := false

	t.Run("configured: the button opens the web UI and is remembered", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		bot := miniAppBot(t, "https://coddy.example.com/", "")
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.Type != "web_app" || m.WebApp.URL != "https://coddy.example.com/" || m.Text != miniAppMenuText {
			t.Fatalf("menu button: %+v", m)
		}
		if set, _ := bot.store.MenuButton(f.api.Self.ID); set != "https://coddy.example.com/" {
			t.Fatalf("remembered %q", set)
		}
	})
	t.Run("turned off: the button set in BotFather before comes back", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		setBotFatherButton(t, f, "https://mine.example.com/")
		bot := miniAppBot(t, "https://coddy.example.com/", "")
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.WebApp == nil || m.WebApp.URL != "https://coddy.example.com/" {
			t.Fatalf("menu button after the takeover: %+v", m)
		}
		bot.cfg.MiniApp.MenuButton = &no
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.Type != "web_app" || m.WebApp == nil || m.WebApp.URL != "https://mine.example.com/" || m.Text != "Mine" {
			t.Fatalf("menu button after turning it off: %+v", m)
		}
		if set, before := bot.store.MenuButton(f.api.Self.ID); set != "" || before != "" {
			t.Fatalf("still remembered %q, %q", set, before)
		}
	})
	t.Run("url emptied with nothing before: the commands come back", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		bot := miniAppBot(t, "https://coddy.example.com/", "")
		bot.syncMenuButton(f.api)
		bot.cfg.MiniApp.URL = ""
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.Type != "commands" {
			t.Fatalf("menu button: %+v", m)
		}
	})
	t.Run("changed in BotFather meanwhile: left alone and forgotten", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		bot := miniAppBot(t, "https://coddy.example.com/", "")
		bot.syncMenuButton(f.api)
		setBotFatherButton(t, f, "https://mine.example.com/")
		bot.cfg.MiniApp.URL = ""
		before := setCalls(f)
		bot.syncMenuButton(f.api)
		if setCalls(f) != before {
			t.Fatal("a button the operator set was overwritten")
		}
		if m := f.fake.MenuButton(0); m.WebApp == nil || m.WebApp.URL != "https://mine.example.com/" {
			t.Fatalf("menu button: %+v", m)
		}
		if set, _ := bot.store.MenuButton(f.api.Self.ID); set != "" {
			t.Fatalf("still remembered %q", set)
		}
	})
	t.Run("never configured: the menu button is not touched at all", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		bot := miniAppBot(t, "", "")
		bot.syncMenuButton(f.api)
		if n := len(f.fake.Calls("")); n != 0 {
			t.Fatalf("a bot with no Mini App made %d Bot API calls about its menu button", n)
		}
	})
	t.Run("a refused set is not remembered", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		f.fake.SetFault(tgfake.Fault{Method: "setChatMenuButton", Code: 400, Description: "Bad Request: nope"})
		bot := miniAppBot(t, "https://coddy.example.com/", "")
		bot.syncMenuButton(f.api)
		if set, _ := bot.store.MenuButton(f.api.Self.ID); set != "" {
			t.Fatalf("a refused button was remembered: %q", set)
		}
	})
	t.Run("a button that cannot be read is not replaced", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		setBotFatherButton(t, f, "https://mine.example.com/")
		f.fake.SetFault(tgfake.Fault{Method: "getChatMenuButton", Code: 500, Description: "Internal Server Error", Times: 1})
		bot := miniAppBot(t, "https://coddy.example.com/", "")
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.WebApp == nil || m.WebApp.URL != "https://mine.example.com/" {
			t.Fatalf("a button that could not be read was replaced: %+v", m)
		}
		if set, before := bot.store.MenuButton(f.api.Self.ID); set != "" || before != "" {
			t.Fatalf("remembered %q, %q", set, before)
		}
		// The next start reads it, takes it over and keeps it to put back.
		bot.syncMenuButton(f.api)
		bot.cfg.MiniApp.MenuButton = &no
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.WebApp == nil || m.WebApp.URL != "https://mine.example.com/" || m.Text != "Mine" {
			t.Fatalf("menu button after turning it off: %+v", m)
		}
	})
	t.Run("a BotFather button to the same address comes back", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		setBotFatherButton(t, f, "https://coddy.example.com/")
		bot := miniAppBot(t, "https://coddy.example.com/", "")
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.Text != miniAppMenuText {
			t.Fatalf("menu button after the takeover: %+v", m)
		}
		bot.cfg.MiniApp.MenuButton = &no
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.Type != "web_app" || m.Text != "Mine" || m.WebApp == nil || m.WebApp.URL != "https://coddy.example.com/" {
			t.Fatalf("menu button after turning it off: %+v", m)
		}
	})
	t.Run("a BotFather button to the same address is not put back to advertise an open web UI", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		setBotFatherButton(t, f, "https://coddy.example.com/")
		bot := miniAppBot(t, "https://coddy.example.com/", "")
		bot.syncMenuButton(f.api)
		bot.SetWebUIGate(func() (bool, string) { return false, "the web UI asks for no sign-in" })
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.Type != "commands" {
			t.Fatalf("an open web UI is advertised again by the button put back: %+v", m)
		}
	})
	t.Run("a store lost under the bot's own button keeps nothing to put back", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		miniAppBot(t, "https://coddy.example.com/", "").syncMenuButton(f.api)
		lost := miniAppBot(t, "https://coddy.example.com/", "")
		lost.syncMenuButton(f.api)
		if _, before := lost.store.MenuButton(f.api.Self.ID); before != "" {
			t.Fatalf("the bot's own button was kept as the operator's: %q", before)
		}
	})
	t.Run("an open web UI is not advertised, and a button set before is taken back", func(t *testing.T) {
		f := newFakeAPI(t, tgfake.Options{})
		bot := miniAppBot(t, "https://coddy.example.com/", "")
		bot.syncMenuButton(f.api)
		bot.SetWebUIGate(func() (bool, string) { return false, "the web UI asks for no sign-in" })
		bot.syncMenuButton(f.api)
		if m := f.fake.MenuButton(0); m.Type != "commands" {
			t.Fatalf("an open web UI is still advertised: %+v", m)
		}
		fresh := miniAppBot(t, "https://coddy.example.com/", "")
		fresh.SetWebUIGate(func() (bool, string) { return false, "the web UI asks for no sign-in" })
		g := newFakeAPI(t, tgfake.Options{})
		fresh.syncMenuButton(g.api)
		if n := len(g.fake.Calls("setChatMenuButton")); n != 0 {
			t.Fatalf("an open web UI was advertised by %d setChatMenuButton calls", n)
		}
	})
}

func TestAppCommandWithholdsAnOpenWebUI(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	bot := miniAppBot(t, "https://coddy.example.com/", "")
	bot.SetWebUIGate(func() (bool, string) { return false, "the web UI asks for no sign-in" })
	key := sessionstore.SessionKey(adapterName, 4242, 4242, config.IsolationIndividual, false)
	bot.store.Get(key)
	msg := f.userMessage(4242, 4242, "/app")
	bot.processMessage(t.Context(), f.api, msg, key)
	replies := f.repliesTo(4242, msg.MessageID)
	if len(replies) != 1 || replies[0].Keyboard != nil || !strings.Contains(replies[0].Text, "asks for no sign-in") ||
		strings.Contains(replies[0].Text, "coddy.example.com") {
		t.Fatalf("/app with an open web UI: %+v", replies)
	}
}
