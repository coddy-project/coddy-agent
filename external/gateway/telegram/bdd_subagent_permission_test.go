//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_subagent_permission.feature: a
// subagent's permission request is asked in the chat through the real sender,
// broker and callback handler, against a stub Telegram API. No LLM and no
// network beyond the local httptest server.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const (
	permissionChatID = int64(5151)
	permissionUserID = int64(5151)
)

type subagentPermissionWorld struct {
	mu      sync.Mutex
	srv     *httptest.Server
	api     *tgbotapi.BotAPI
	bot     *Bot
	calls   []apiCall
	nextMsg int

	sessionID string
	isGroup   bool
	answers   chan *acp.PermissionResult
}

func (w *subagentPermissionWorld) handler(rw http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	w.mu.Lock()
	w.calls = append(w.calls, apiCall{method: method, form: r.PostForm})
	w.nextMsg++
	id := w.nextMsg
	w.mu.Unlock()
	rw.Header().Set("Content-Type", "application/json")
	if method == "answerCallbackQuery" {
		_, _ = fmt.Fprint(rw, `{"ok":true,"result":true}`)
		return
	}
	_, _ = fmt.Fprintf(rw, `{"ok":true,"result":{"message_id":%d,"date":0,"chat":{"id":%d,"type":"private"}}}`, id, permissionChatID)
}

func (w *subagentPermissionWorld) reset() {
	w.close()
	w.srv = httptest.NewServer(http.HandlerFunc(w.handler))
	w.api = &tgbotapi.BotAPI{Token: "TESTTOKEN", Client: &http.Client{}, Buffer: 100}
	w.api.SetAPIEndpoint(w.srv.URL + "/bot%s/%s")
	runner := newStubRunner(&config.Config{})
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	}, runner, "", slog.New(slog.DiscardHandler), "", nil)
	w.bot.setAPI(w.api)
	w.calls = nil
	w.nextMsg = 0
	w.isGroup = false
	w.answers = make(chan *acp.PermissionResult, 1)
}

func (w *subagentPermissionWorld) close() {
	if w.bot != nil {
		w.bot.asks.stop()
	}
	if w.srv != nil {
		w.srv.Close()
		w.srv = nil
	}
}

func (w *subagentPermissionWorld) chatKey() string {
	return sessionstore.SessionKey(adapterName, permissionChatID, permissionUserID, config.IsolationIndividual, w.isGroup)
}

func (w *subagentPermissionWorld) groupWithSession() error {
	w.isGroup = true
	return w.chatWithSession()
}

func (w *subagentPermissionWorld) chatWithSession() error {
	w.sessionID = w.bot.store.Get(w.chatKey())
	return nil
}

func permissionParams(sessionID, name, command string) acp.PermissionRequestParams {
	return acp.PermissionRequestParams{
		SessionID: sessionID,
		ToolCall: acp.PermissionToolCall{
			ToolCallID: "call_" + name,
			Title:      "[subagent " + name + "] Run: " + command,
			Status:     "pending",
		},
		Options: []acp.PermissionOption{
			{OptionID: "allow", Name: "Allow", Kind: "allow_once"},
			{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
		},
		EffectivePermissionMode: config.PermModeAsk,
	}
}

func (w *subagentPermissionWorld) liveSubagentAsks(name, command string) error {
	sender := w.bot.chatSender(w.api, permissionChatID, 0, richConfig{})
	params := permissionParams(w.sessionID, name, command)
	go func() {
		res, err := sender.RequestPermission(context.Background(), params)
		if err != nil {
			res = nil
		}
		w.answers <- res
	}()
	return nil
}

func (w *subagentPermissionWorld) backgroundSubagentAsks(name, command string) error {
	req := agent.DetachedPermissionRequest{
		ParentSessionID: w.sessionID,
		ChildSessionID:  "sess_child_" + name,
		TaskID:          "bg_1",
		AgentName:       name,
		Params:          permissionParams("sess_child_"+name, name, command),
	}
	go func() {
		res, err := w.bot.RequestDetachedPermission(context.Background(), req)
		if err != nil {
			res = nil
		}
		w.answers <- res
	}()
	return nil
}

// promptMessage returns the sendMessage call that carries the permission
// request, waiting for it to arrive.
func (w *subagentPermissionWorld) promptMessage() (url.Values, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		for _, c := range w.calls {
			if c.method == "sendMessage" && strings.Contains(c.form.Get("text"), "[subagent ") {
				form := c.form
				w.mu.Unlock()
				return form, nil
			}
		}
		w.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return nil, fmt.Errorf("the chat never received a permission request; calls: %+v", w.calls)
}

func (w *subagentPermissionWorld) chatShowsRequest(name, first, second string) error {
	form, err := w.promptMessage()
	if err != nil {
		return err
	}
	if text := form.Get("text"); !strings.Contains(text, "[subagent "+name+"]") {
		return fmt.Errorf("the request does not name %q: %q", name, text)
	}
	if got := form.Get("chat_id"); got != fmt.Sprint(permissionChatID) {
		return fmt.Errorf("the request went to chat %s, want %d", got, permissionChatID)
	}
	var kb tgbotapi.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(form.Get("reply_markup")), &kb); err != nil {
		return fmt.Errorf("the request has no keyboard: %w", err)
	}
	var labels []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData == nil || len(*btn.CallbackData) > telegramCallbackDataMax {
				return fmt.Errorf("button %q carries unusable callback data", btn.Text)
			}
			labels = append(labels, btn.Text)
		}
	}
	if strings.Join(labels, ",") != first+","+second {
		return fmt.Errorf("buttons %v, want %q and %q", labels, first, second)
	}
	return nil
}

func (w *subagentPermissionWorld) userTaps(label string) error {
	return w.tapAs(label, permissionUserID)
}

func (w *subagentPermissionWorld) foreignUserTaps(label string) error {
	return w.tapAs(label, permissionUserID+1)
}

func (w *subagentPermissionWorld) ownerKeepsButtons() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, call := range w.calls {
		if call.method == "editMessageReplyMarkup" {
			return fmt.Errorf("another member removed the owner's buttons")
		}
	}
	return nil
}

func (w *subagentPermissionWorld) tapAs(label string, userID int64) error {
	form, err := w.promptMessage()
	if err != nil {
		return err
	}
	var kb tgbotapi.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(form.Get("reply_markup")), &kb); err != nil {
		return err
	}
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.Text != label {
				continue
			}
			chatType := "private"
			if w.isGroup {
				chatType = "supergroup"
			}
			w.bot.handleCallback(context.Background(), w.api, &tgbotapi.CallbackQuery{
				ID:   "cb_perm",
				From: &tgbotapi.User{ID: userID},
				Message: &tgbotapi.Message{
					MessageID: 1,
					Chat:      &tgbotapi.Chat{ID: permissionChatID, Type: chatType},
				},
				Data: *btn.CallbackData,
			})
			return nil
		}
	}
	return fmt.Errorf("no button labelled %q", label)
}

func (w *subagentPermissionWorld) subagentAnswered(option string) error {
	select {
	case res := <-w.answers:
		if res == nil || res.OptionID != option {
			return fmt.Errorf("the subagent was answered %+v, want %q", res, option)
		}
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("the subagent was never answered")
	}
}

func (w *subagentPermissionWorld) requestReads(word string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		for _, c := range w.calls {
			if c.method == "editMessageText" && strings.Contains(c.form.Get("text"), word) {
				w.mu.Unlock()
				return nil
			}
		}
		w.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("the request in the chat never read %q; calls: %+v", word, w.calls)
}

func initializeSubagentPermissionScenario(sc *godog.ScenarioContext) {
	w := &subagentPermissionWorld{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.close()
		return ctx, err
	})
	sc.Given(`^a telegram chat whose agent is running a turn$`, w.chatWithSession)
	sc.Given(`^a telegram chat with a session$`, w.chatWithSession)
	sc.Given(`^a telegram group with individual sessions$`, w.groupWithSession)
	sc.When(`^another group member taps "([^"]*)"$`, w.foreignUserTaps)
	sc.Then(`^the owner's permission buttons remain available$`, w.ownerKeepsButtons)
	sc.When(`^a subagent "([^"]*)" of that turn asks to run "([^"]*)"$`, w.liveSubagentAsks)
	sc.When(`^the background subagent "([^"]*)" of that session asks to run "([^"]*)"$`, w.backgroundSubagentAsks)
	sc.Then(`^the chat shows a permission request naming the subagent "([^"]*)" with the buttons "([^"]*)" and "([^"]*)"$`, w.chatShowsRequest)
	sc.When(`^the user taps "([^"]*)"$`, w.userTaps)
	sc.Then(`^the subagent is answered "([^"]*)"$`, w.subagentAnswered)
	sc.Then(`^the request in the chat reads "([^"]*)"$`, w.requestReads)
}

func TestSubagentPermissionFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway-telegram-subagent-permission",
		ScenarioInitializer: initializeSubagentPermissionScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_telegram_subagent_permission.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("telegram subagent permission feature suite failed")
	}
}
