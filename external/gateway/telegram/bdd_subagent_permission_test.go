//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_subagent_permission.feature: a
// subagent's permission request is asked in the chat through the real sender,
// broker and callback handler, against the fake Bot API (internal/tgfake). A
// tap presses the button the request really carries, on the message it came
// with, and the steps read the chat as the person sees it. No LLM and no
// network beyond the local httptest server.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

const (
	permissionChatID = int64(5151)
	permissionUserID = int64(5151)
	// permissionGroupID is the group of the group scenario. Telegram numbers
	// a group below zero and a private chat by the id of its user, and the
	// fake types a chat the bot writes to first by that sign: a group on a
	// positive id would be a private chat, and a tap there would not come
	// from a group.
	permissionGroupID = int64(-1005151)
)

type subagentPermissionWorld struct {
	f   *fakeAPI
	bot *Bot

	sessionID string
	isGroup   bool
	answers   chan *acp.PermissionResult
}

func (w *subagentPermissionWorld) reset() {
	w.close()
	w.f = openFakeAPI(tgfake.Options{})
	runner := newStubRunner(&config.Config{})
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
		// Approving what a subagent asks is an admin's.
		Admins: []int64{permissionUserID},
	}, runner, "", slog.New(slog.DiscardHandler), "", nil)
	w.bot.setAPI(w.f.api)
	w.isGroup = false
	w.answers = make(chan *acp.PermissionResult, 1)
}

func (w *subagentPermissionWorld) close() {
	if w.bot != nil {
		w.bot.asks.stop()
	}
	if w.f != nil {
		w.f.close()
		w.f = nil
	}
}

// chatID is the chat the scenario talks in: the user's private chat, or the
// group.
func (w *subagentPermissionWorld) chatID() int64 {
	if w.isGroup {
		return permissionGroupID
	}
	return permissionChatID
}

func (w *subagentPermissionWorld) chatKey() string {
	return sessionstore.SessionKey(adapterName, w.chatID(), permissionUserID, config.IsolationIndividual, w.isGroup)
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
	sender := w.bot.chatSender(w.f.api, w.chatID(), 0, richConfig{})
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

// promptMessage returns the message of the chat that carries the permission
// request, waiting for it to arrive: the subagent asks from a goroutine of
// its own. Reading it from the scenario's chat is what proves the request
// went there.
func (w *subagentPermissionWorld) promptMessage() (tgfake.MessageView, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		view := w.f.fake.Chat(w.chatID())
		for _, m := range view.Messages {
			if m.From == "bot" && !m.Deleted && strings.Contains(m.Text, "[subagent ") {
				return m, nil
			}
		}
		if !time.Now().Before(deadline) {
			return tgfake.MessageView{}, fmt.Errorf("chat %d never received a permission request:\n%s\ncalls: %+v",
				w.chatID(), view.Text(), w.f.fake.Calls(""))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (w *subagentPermissionWorld) chatShowsRequest(name, first, second string) error {
	m, err := w.promptMessage()
	if err != nil {
		return err
	}
	if !strings.Contains(m.Text, "[subagent "+name+"]") {
		return fmt.Errorf("the request does not name %q: %q", name, m.Text)
	}
	var labels []string
	for _, row := range m.Keyboard {
		for _, btn := range row {
			if btn.CallbackData == "" || len(btn.CallbackData) > telegramCallbackDataMax {
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

// ownerKeepsButtons asserts that the tap of somebody else left the request
// as it was: no call went out to take its buttons away, and the chat still
// shows them under it.
func (w *subagentPermissionWorld) ownerKeepsButtons() error {
	if calls := w.f.fake.Calls("editMessageReplyMarkup"); len(calls) != 0 {
		return fmt.Errorf("another member removed the owner's buttons: %+v", calls)
	}
	m, err := w.promptMessage()
	if err != nil {
		return err
	}
	if len(m.Keyboard) == 0 {
		return fmt.Errorf("the request no longer shows its buttons:\n%s", w.f.fake.Chat(w.chatID()).Text())
	}
	return nil
}

// tapAs presses the button labelled label as userID, through the fake: the
// query is one the fake issued, on the message that carries the request, from
// a chat of the type the fake gave it.
func (w *subagentPermissionWorld) tapAs(label string, userID int64) error {
	if _, err := w.promptMessage(); err != nil {
		return err
	}
	cbq, err := w.f.tap(w.chatID(), userID, label)
	if err != nil {
		return err
	}
	w.bot.handleCallback(context.Background(), w.f.api, cbq)
	return nil
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

// requestReads waits for the request in the chat to say how it was settled:
// the bot edits the message once the subagent has its answer.
func (w *subagentPermissionWorld) requestReads(word string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		m, err := w.promptMessage()
		if err != nil {
			return err
		}
		if m.Edited && strings.Contains(m.Text, word) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the request in the chat never read %q:\n%s", word, w.f.fake.Chat(w.chatID()).Text())
		}
		time.Sleep(10 * time.Millisecond)
	}
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
