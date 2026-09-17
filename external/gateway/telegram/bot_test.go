//go:build gateway || gateway.telegram

package telegram

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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
