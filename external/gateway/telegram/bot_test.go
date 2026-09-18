//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

func TestTelegramAPIEndpoint(t *testing.T) {
	cases := []struct {
		name, base, want string
	}{
		{"empty is the real API", "", tgbotapi.APIEndpoint},
		{"whitespace is empty", "  \t", tgbotapi.APIEndpoint},
		{"origin", "http://127.0.0.1:18790", "http://127.0.0.1:18790/bot%s/%s"},
		{"trailing slash dropped", "http://127.0.0.1:18790/", "http://127.0.0.1:18790/bot%s/%s"},
		{"surrounding whitespace dropped", " https://tg.example.internal ", "https://tg.example.internal/bot%s/%s"},
		{"path prefix kept", "http://proxy.local/telegram", "http://proxy.local/telegram/bot%s/%s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := telegramAPIEndpoint(tc.base); got != tc.want {
				t.Fatalf("telegramAPIEndpoint(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

// A woken turn is the bot's only when one of its chats is bound to the session
// and the bot is connected to Telegram; anything else is handed back untouched,
// so the process can run it where it belongs.
func TestRunBackgroundWakeDeclinesWhatIsNotTheBots(t *testing.T) {
	runner := newScriptedRunner()
	bot := New(&config.TelegramGatewayConfig{Enabled: true, Token: "t", DefaultAccess: config.AccessAll},
		runner, "", slog.New(slog.DiscardHandler), "", nil)
	fake := newFakeAPI(t, tgfake.Options{})
	key := sessionstore.SessionKey(adapterName, 7071, 7071, config.IsolationIndividual, false)
	sessionID := bot.store.Get(key)

	// Not connected: nothing can be delivered, whoever owns the session.
	if handled, err := bot.RunBackgroundWake(context.Background(), agent.Wake{SessionID: sessionID}); handled || err != nil {
		t.Fatalf("a disconnected bot took the wake: %v, %v", handled, err)
	}

	bot.setAPI(fake.api)
	if handled, err := bot.RunBackgroundWake(context.Background(), agent.Wake{SessionID: "sess_somebody_else"}); handled || err != nil {
		t.Fatalf("the bot took the wake of a session no chat holds: %v, %v", handled, err)
	}
	if len(runner.prompts) != 0 {
		t.Fatalf("a declined wake ran a turn: %q", runner.prompts)
	}

	// Its own chat's session is run there, with the wake as the prompt.
	if handled, err := bot.RunBackgroundWake(context.Background(), agent.Wake{SessionID: sessionID}); !handled || err != nil {
		t.Fatalf("the bot declined its own chat's wake: %v, %v", handled, err)
	}
	if len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], "background task") {
		t.Fatalf("prompts = %q, want the wake instruction", runner.prompts)
	}
	if !strings.Contains(fake.fake.Chat(7071).Text(), runner.answer) {
		t.Fatalf("the woken turn's answer never reached the chat:\n%s", fake.fake.Chat(7071).Text())
	}
}

// A group conversation is keyed by the chat; a private one by the user alone.
func TestIsGroupKey(t *testing.T) {
	for key, want := range map[string]bool{
		sessionstore.SessionKey(adapterName, 42, 42, config.IsolationIndividual, false):  false,
		sessionstore.SessionKey(adapterName, -100, 42, config.IsolationShared, true):     true,
		sessionstore.SessionKey(adapterName, -100, 42, config.IsolationIndividual, true): true,
		sessionstore.SessionKey(adapterName, -100, 42, config.IsolationAdmin, true):      true,
		"": false,
	} {
		if got := isGroupKey(key); got != want {
			t.Errorf("isGroupKey(%q) = %v, want %v", key, got, want)
		}
	}
}
