//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	tgfake "github.com/EvilFreelancer/tgfake/pkg/server"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
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

func TestGoalCommandReachesSessionInTelegram(t *testing.T) {
	for _, tc := range []struct{ name, text, chatType string }{
		{"private", "/goal ship the fix", "private"},
		{"group mention", "/goal@coddy_bot ship the fix", "group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := newScriptedRunner()
			bot := New(&config.TelegramGatewayConfig{Enabled: true, Token: "t", DefaultAccess: config.AccessAll},
				runner, "", slog.New(slog.DiscardHandler), t.TempDir(), nil)
			bot.botName = "coddy_bot"
			fake := newFakeAPI(t, tgfake.Options{})
			key := sessionstore.SessionKey(adapterName, resumeChatID, resumeUserID, config.IsolationIndividual, tc.chatType == "group")
			msg := commandMessage(tc.text)
			msg.Chat.Type = tc.chatType
			bot.processMessage(context.Background(), fake.api, msg, key)
			if len(runner.prompts) != 1 || runner.prompts[0] != "/goal ship the fix" {
				t.Fatalf("Telegram did not forward /goal: %q", runner.prompts)
			}
		})
	}
}

func TestTelegramShowsAGoalTurnAsANoteOfItsOwn(t *testing.T) {
	fake := newFakeAPI(t, tgfake.Options{})
	sender := newSender(fake.api, 7072, 0, slog.New(slog.DiscardHandler), richConfig{})
	if err := sender.SendSessionUpdate("sess_goal", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "First turn answer."},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendSessionUpdate("sess_goal", acp.GoalTurnUpdate{
		SessionUpdate: acp.UpdateTypeGoalTurn, Kind: acp.GoalTurnContinue, Index: 1, Limit: 10,
		Objective: "ship the fix", Reason: "tests still fail",
	}); err != nil {
		t.Fatal(err)
	}
	got := fake.fake.Chat(7072).Text()
	if !strings.Contains(got, "First turn answer.") || !strings.Contains(got, "Goal continuation 1 of 10: tests still fail") ||
		strings.Index(got, "First turn answer.") > strings.Index(got, "Goal continuation") {
		t.Fatalf("the first answer is not final above the goal note: %q", got)
	}
}

func TestTelegramPostsHowAGoalEnded(t *testing.T) {
	fake := newFakeAPI(t, tgfake.Options{})
	sender := newSender(fake.api, 7073, 0, slog.New(slog.DiscardHandler), richConfig{})
	for _, notice := range []string{"Goal set: x", "Goal check: the supervisor is reviewing the turn", "Goal blocked: which database?"} {
		_ = sender.SendSessionUpdate("s", acp.SessionGoalUpdate{SessionUpdate: acp.UpdateTypeSessionGoal, Notice: notice})
	}
	got := fake.fake.Chat(7073).Text()
	if !strings.Contains(got, "Goal blocked: which database?") || strings.Contains(got, "Goal set:") || strings.Contains(got, "Goal check:") {
		t.Fatalf("chat = %q", got)
	}
}

func TestGoalCommandSentAsAReplyStaysACommand(t *testing.T) {
	runner := newScriptedRunner()
	bot := New(&config.TelegramGatewayConfig{Enabled: true, Token: "t", DefaultAccess: config.AccessAll},
		runner, "", slog.New(slog.DiscardHandler), t.TempDir(), nil)
	bot.botName = "coddy_bot"
	fake := newFakeAPI(t, tgfake.Options{})
	key := sessionstore.SessionKey(adapterName, resumeChatID, resumeUserID, config.IsolationIndividual, false)
	msg := commandMessage("/goal pause")
	msg.ReplyToMessage = &tgbotapi.Message{MessageID: 9, Text: "an earlier answer", From: &tgbotapi.User{UserName: "coddy_bot"}}
	bot.processMessage(context.Background(), fake.api, msg, key)
	if len(runner.prompts) != 1 || runner.prompts[0] != "/goal pause" {
		t.Fatalf("a reply quoted the /goal command: %q", runner.prompts)
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

func TestReplyContext(t *testing.T) {
	if a, q := replyContext(nil); a != "" || q != "" {
		t.Fatalf("no reply: %q %q", a, q)
	}
	a, q := replyContext(&tgbotapi.Message{From: &tgbotapi.User{FirstName: "Anna", LastName: "K"}, Text: "hi"})
	if a != "Anna K" || q != "hi" {
		t.Fatalf("text reply: %q %q", a, q)
	}
	a, q = replyContext(&tgbotapi.Message{From: &tgbotapi.User{UserName: "boris"}, Caption: "a photo"})
	if a != "@boris" || q != "a photo" {
		t.Fatalf("caption reply: %q %q", a, q)
	}
	if _, q := replyContext(&tgbotapi.Message{From: &tgbotapi.User{FirstName: "x"}}); q != "" {
		t.Fatalf("a message without text quoted %q", q)
	}
}

func TestChangesSettings(t *testing.T) {
	cases := map[string]bool{
		"/clear": true, "/model": true, "/model x": true, "/resume": true, "/plan": true,
		"/think": true, "/help": false, "/context": false, "/mcp": false, "hello": false,
	}
	for text, want := range cases {
		msg := commandMessage(text)
		if !strings.HasPrefix(text, "/") {
			msg = &tgbotapi.Message{Text: text}
		}
		if got := changesSettings(msg); got != want {
			t.Errorf("changesSettings(%q) = %v, want %v", text, got, want)
		}
	}
}

// A settings command written after the bot's mention ("@bot /think") is the
// same command: in a group it is the admins' too.
func TestGroupSettingsCommandAfterAMentionIsAdminOnly(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{BotUsername: "coddy_bot"})
	runner := newScriptedRunner()
	b := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual, Admins: []int64{9}},
		runner, t.TempDir(), slog.New(slog.DiscardHandler), "", nil)
	b.botName = "coddy_bot"
	for _, text := range []string{"@coddy_bot /think", "@coddy_bot /model openai/x", "@coddy_bot /resume", "@coddy_bot /clear"} {
		msg := f.userMessage(-100, 5, text)
		key := sessionstore.SessionKey(adapterName, -100, 5, config.IsolationIndividual, true)
		b.processMessage(context.Background(), f.api, msg, key)
		if len(runner.prompts) != 0 {
			t.Fatalf("%q from a non-admin reached the session: %q", text, runner.prompts)
		}
		if replies := f.repliesTo(-100, msg.MessageID); len(replies) != 1 || !strings.Contains(replies[0].Text, "Only the bot's admins") {
			t.Fatalf("%q was not refused: %+v", text, replies)
		}
	}
}

// A message of somebody who is not the bot's admin runs a restricted turn,
// and the chat approves nothing for it; an admin's turn is unrestricted.
func TestNonAdminTurnIsRestricted(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{BotUsername: "coddy_bot"})
	runner := newScriptedRunner()
	b := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual, Admins: []int64{9}},
		runner, t.TempDir(), slog.New(slog.DiscardHandler), "", nil)
	b.botName = "coddy_bot"
	for _, uid := range []int64{5, 9} {
		key := sessionstore.SessionKey(adapterName, uid, uid, config.IsolationIndividual, false)
		b.processMessage(context.Background(), f.api, f.userMessage(uid, uid, "hello"), key)
	}
	if len(runner.restricted) != 2 || !runner.restricted[0] || runner.restricted[1] {
		t.Fatalf("restricted turns: %v, want [true false] for a user and an admin", runner.restricted)
	}
	s := b.chatSender(f.api, 5, 0, richConfig{})
	s.refuseApprovals = true
	res, err := s.RequestPermission(context.Background(), acp.PermissionRequestParams{SessionID: "x"})
	if err != nil || res == nil || res.OptionID != "reject" {
		t.Fatalf("a non-admin's own agent was approved: %+v %v", res, err)
	}
}

func TestAppCommandIsAdminOnly(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	b := miniAppBot(t, "https://coddy.example.com/", "")
	b.cfg.Admins = []int64{1}
	msg := f.userMessage(4242, 4242, "/app")
	b.processMessage(t.Context(), f.api, msg, sessionstore.SessionKey(adapterName, 4242, 4242, config.IsolationIndividual, false))
	replies := f.repliesTo(4242, msg.MessageID)
	if len(replies) != 1 || !strings.Contains(replies[0].Text, "Only the bot's admins") {
		t.Fatalf("/app from a non-admin: %+v", replies)
	}
}

// "@bot /command" is the command: a command nobody handles is dropped rather
// than sent to the session, /permissions too, and an admin's /clear clears.
func TestCommandAfterAMentionIsTheCommand(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{BotUsername: "coddy_bot"})
	runner := newScriptedRunner()
	b := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual, Admins: []int64{9}},
		runner, t.TempDir(), slog.New(slog.DiscardHandler), "", nil)
	b.botName = "coddy_bot"
	for _, text := range []string{"@coddy_bot /plugin install evil/repo", "@coddy_bot /compact", "@coddy_bot /permissions bypass", "@coddy_bot /export x.md"} {
		for _, uid := range []int64{5, 9} {
			b.processMessage(context.Background(), f.api, f.userMessage(uid, uid, text), sessionstore.SessionKey(adapterName, uid, uid, config.IsolationIndividual, false))
		}
	}
	if len(runner.prompts) != 0 {
		t.Fatalf("commands after a mention reached the session: %q", runner.prompts)
	}
	key := sessionstore.SessionKey(adapterName, 9, 9, config.IsolationIndividual, false)
	before := b.store.Get(key)
	msg := f.userMessage(9, 9, "@coddy_bot /clear")
	b.processMessage(context.Background(), f.api, msg, key)
	if b.store.Peek(key) == before {
		t.Fatal("an admin's \"@bot /clear\" did not start a new session")
	}
}

// In a shared group session everybody shares the asking session: only the
// bot's admins may answer what the agent asks.
func TestPermissionTapFromANonAdminInASharedGroupIsIgnored(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	b := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationShared, Admins: []int64{9}},
		newScriptedRunner(), t.TempDir(), slog.New(slog.DiscardHandler), "", nil)
	key := sessionstore.SessionKey(adapterName, -100, 0, config.IsolationShared, true)
	sid := b.store.Get(key)
	p := &chatPrompt{sessionID: sid, options: []acp.PermissionOption{{OptionID: "allow", Name: "Allow"}}, answer: make(chan *acp.PermissionResult, 1)}
	b.asks.mu.Lock()
	b.asks.pending["tok"] = p
	b.asks.mu.Unlock()
	tap := func(uid int64) {
		b.answerPermissionTap(f.api, &tgbotapi.CallbackQuery{
			From:    &tgbotapi.User{ID: uid},
			Message: &tgbotapi.Message{MessageID: 1, Chat: &tgbotapi.Chat{ID: -100, Type: "group"}},
		}, "tok:0")
	}
	tap(5)
	select {
	case <-p.answer:
		t.Fatal("a non-admin answered a permission request in a shared group")
	default:
	}
	tap(9)
	select {
	case res := <-p.answer:
		if res.OptionID != "allow" {
			t.Fatalf("the admin's answer: %+v", res)
		}
	default:
		t.Fatal("the admin's tap did not answer the request")
	}
}
