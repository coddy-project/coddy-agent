//go:build gateway || gateway.telegram

package telegram

// The one stand-in for Telegram this package's tests share: the fake Bot API
// of internal/tgfake on httptest, plus a tgbotapi client pointed at it. A test
// that needs Telegram to behave in a new way extends the fake; it does not
// write another http.HandlerFunc.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

// fakeToken is the token stubBot sends. A test that wants the path checked
// hands it to the fake as tgfake.Options.Token: any other token is a 401.
const fakeToken = "TESTTOKEN"

type fakeAPI struct {
	fake *tgfake.Server
	srv  *httptest.Server
	api  *tgbotapi.BotAPI
}

// openFakeAPI starts the fake for a harness that has no *testing.T (the godog
// worlds); the caller closes it.
func openFakeAPI(opts tgfake.Options) *fakeAPI {
	if opts.MaxPollWait == 0 {
		opts.MaxPollWait = 250 * time.Millisecond
	}
	fake := tgfake.New(opts)
	srv := httptest.NewServer(fake.Handler())
	return &fakeAPI{fake: fake, srv: srv, api: stubBot(srv.URL)}
}

// newFakeAPI is openFakeAPI closed by the test's cleanup.
func newFakeAPI(t *testing.T, opts tgfake.Options) *fakeAPI {
	t.Helper()
	f := openFakeAPI(opts)
	t.Cleanup(f.close)
	return f
}

// close releases the long polls before the listener, or httptest would wait
// for them.
func (f *fakeAPI) close() {
	f.fake.Close()
	f.srv.Close()
}

// stubBot builds a BotAPI pointed at a local server, bypassing the getMe call
// NewBotAPIWithClient would make.
func stubBot(srvURL string) *tgbotapi.BotAPI {
	bot := &tgbotapi.BotAPI{Token: fakeToken, Client: &http.Client{}, Buffer: 100}
	bot.SetAPIEndpoint(srvURL + apiEndpointSuffix)
	return bot
}

// userMessage puts a message into the fake's chat and returns it in the shape
// a handler is given, so that what the bot replies to is a message Telegram
// knows. The update it leaves in the fake stays pending: the harnesses that
// use this call the handlers themselves and nothing polls.
func (f *fakeAPI) userMessage(chatID, userID int64, text string) *tgbotapi.Message {
	chatType := "private"
	if chatID < 0 {
		chatType = "group"
	}
	_, id := f.fake.InjectMessage(tgfake.IncomingMessage{ChatID: chatID, ChatType: chatType, UserID: userID, Text: text})
	msg := &tgbotapi.Message{
		MessageID: id,
		From:      &tgbotapi.User{ID: userID},
		Chat:      &tgbotapi.Chat{ID: chatID, Type: chatType},
		Text:      text,
	}
	if strings.HasPrefix(text, "/") {
		command, _, _ := strings.Cut(text, " ")
		msg.Entities = []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(command)}}
	}
	return msg
}

// tap presses the button a person would see under that label and returns the
// callback query for it: the message is the one that really carries the
// keyboard, the payload is what the bot put behind the button.
func (f *fakeAPI) tap(chatID, userID int64, label string) (*tgbotapi.CallbackQuery, error) {
	view := f.fake.Chat(chatID)
	msgID, data, ok := view.FindButton(label)
	if !ok {
		return nil, fmt.Errorf("no button %q in chat %d:\n%s", label, chatID, view.Text())
	}
	_, id, err := f.fake.InjectCallback(tgfake.IncomingCallback{ChatID: chatID, UserID: userID, MessageID: msgID, Data: data})
	if err != nil {
		return nil, err
	}
	return &tgbotapi.CallbackQuery{
		ID:      id,
		From:    &tgbotapi.User{ID: userID},
		Message: &tgbotapi.Message{MessageID: msgID, Chat: &tgbotapi.Chat{ID: chatID, Type: view.Type}},
		Data:    data,
	}, nil
}
