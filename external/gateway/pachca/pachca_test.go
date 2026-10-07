//go:build gateway || gateway.pachca

package pachca

// Unit tests of the edges: the events history walk, the client's 429 policy,
// splitting and rendering, addressing. The happy paths are the godog specs in
// bdd_test.go.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/pachcafake"
)

type testEnv struct {
	fake   *pachcafake.Server
	srv    *httptest.Server
	runner *scriptRunner
	bot    *Bot
	client *Client
	p      *poller
}

func newTestEnv(t *testing.T, opts pachcafake.Options, cfg *config.PachcaGatewayConfig) *testEnv {
	t.Helper()
	opts.Token = bddToken
	fake := pachcafake.New(opts)
	srv := httptest.NewServer(fake.Handler())
	t.Cleanup(srv.Close)
	if cfg == nil {
		cfg = &config.PachcaGatewayConfig{}
	}
	cfg.Enabled, cfg.Token = true, bddToken
	cfg.ApplyDefaults()
	base, _, _ := logger.New(config.Logger{Level: config.LogLevelError, Format: config.LogFormatText, Outputs: []string{config.LogOutputStderr}})
	runner := newScriptRunner()
	b := New(cfg, runner, t.TempDir(), base, "", "", nil)
	b.apiBase = srv.URL
	c := NewClient(srv.URL, bddToken, nil)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	b.selfID = fake.BotUserID()
	b.nickname = "coddy_bot"
	b.setClient(c)
	return &testEnv{fake: fake, srv: srv, runner: runner, bot: b, client: c, p: &poller{b: b, c: c, started: time.Now()}}
}

// startAt makes the bot begin after the newest event already in the log.
func (e *testEnv) startAt(t *testing.T) {
	t.Helper()
	if err := e.p.tick(context.Background(), context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.bot.state.watermark().empty() {
		t.Fatal("the first tick set no watermark")
	}
}

func (e *testEnv) dm(t *testing.T, userID int64, text string) pachcafake.Message {
	t.Helper()
	e.fake.AddUser(userID, fmt.Sprintf("u%d", userID), "U")
	return e.fake.UserPosts(pachcafake.Post{UserID: userID, ChatID: e.fake.PersonalChat(userID), Content: text})
}

// tickUntilQuiet runs ticks until a pass completes with nothing left.
func (e *testEnv) tickUntilQuiet(t *testing.T, max int) {
	t.Helper()
	for i := 0; i < max; i++ {
		if err := e.p.tick(context.Background(), context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	if err := waitUntil(cond, what); err != nil {
		t.Fatal(err)
	}
}

func TestPoll_BacklogLargerThanOneTickIsNotSkipped(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.dm(t, 1, "before the start")
	e.startAt(t)
	const n = eventsPageSize*pagesPerTick + 37
	for i := 0; i < n; i++ {
		e.dm(t, int64(1000+i), fmt.Sprintf("m%d", i))
	}
	// The first tick reads its page budget and stops short of the watermark:
	// nothing is dispatched and the watermark stays where it was.
	wm := e.bot.state.watermark()
	if err := e.p.tick(context.Background(), context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.bot.state.watermark() != wm || e.runner.promptCount() != 0 {
		t.Fatalf("an incomplete pass moved the watermark or dispatched: prompts=%d", e.runner.promptCount())
	}
	e.tickUntilQuiet(t, 3)
	waitFor(t, func() bool { return e.runner.promptCount() == n }, fmt.Sprintf("%d turns", n))
	seen := map[string]bool{}
	e.runner.mu.Lock()
	for _, p := range e.runner.prompts {
		seen[p] = true
	}
	e.runner.mu.Unlock()
	for i := 0; i < n; i++ {
		if !seen[fmt.Sprintf("m%d", i)] {
			t.Fatalf("message m%d was skipped", i)
		}
	}
	if left := len(e.fake.Events()); left != 1 {
		t.Fatalf("handled events are still in the history: %d (want only the one before the start)", left)
	}
}

func TestPoll_FirstStartSkipsWhatWasThereBefore(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.dm(t, 1, "old")
	e.dm(t, 2, "older still")
	e.startAt(t)
	e.tickUntilQuiet(t, 2)
	time.Sleep(50 * time.Millisecond)
	if n := e.runner.promptCount(); n != 0 {
		t.Fatalf("events from before the first start ran %d turn(s)", n)
	}
	if left := len(e.fake.Events()); left != 2 {
		t.Fatalf("events from before the first start were deleted: %d left", left)
	}
}

func TestPoll_EventsOfOneMillisecondAreAllHandled(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	e.fake.SetNow(func() time.Time { return at })
	e.dm(t, 1, "first")
	e.startAt(t)
	e.dm(t, 2, "second")
	e.dm(t, 3, "third")
	e.tickUntilQuiet(t, 2)
	waitFor(t, func() bool { return e.runner.promptCount() == 2 }, "both events of the same millisecond")
}

func TestPoll_WithoutDeleteScopeTheWatermarkCarries(t *testing.T) {
	scopes := []string{"messages:create", "messages:update", "messages:read", "chats:read", "profile:read", "webhooks:events:read"}
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true, Scopes: scopes}, nil)
	e.startAt(t)
	e.dm(t, 1, "hello")
	e.tickUntilQuiet(t, 3)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the turn")
	if !e.p.noDelete {
		t.Fatal("a refused delete did not switch the poller to the watermark alone")
	}
	e.tickUntilQuiet(t, 3)
	time.Sleep(50 * time.Millisecond)
	if n := e.runner.promptCount(); n != 1 {
		t.Fatalf("an event left in the history ran again: %d turns", n)
	}
	if len(e.fake.Events()) != 1 {
		t.Fatalf("the event should stay in the history, got %d", len(e.fake.Events()))
	}
}

func TestPoll_AStoppingBotLeavesTheEventForTheNextProcess(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.startAt(t)
	wm := e.bot.state.watermark()
	e.dm(t, 1, "late")
	e.bot.mu.Lock()
	e.bot.stopping = true
	e.bot.mu.Unlock()
	e.tickUntilQuiet(t, 1)
	if e.bot.state.watermark() != wm {
		t.Fatal("a refused event moved the watermark")
	}
	if len(e.fake.Events()) != 1 {
		t.Fatalf("a refused event was deleted: %d events left", len(e.fake.Events()))
	}
}

func TestPoll_AMessageDeliveredTwiceRunsOneTurn(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.startAt(t)
	m := e.dm(t, 1, "once")
	e.fake.LogRaw("message_new", e.fake.Events()[0].Payload)
	e.tickUntilQuiet(t, 2)
	time.Sleep(50 * time.Millisecond)
	if n := e.runner.promptCount(); n != 1 {
		t.Fatalf("message %d delivered twice ran %d turns", m.ID, n)
	}
}

func TestPoll_EditsAndDeletesAreIgnored(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.startAt(t)
	m := e.dm(t, 1, "original")
	e.tickUntilQuiet(t, 2)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the first turn")
	e.fake.UserEdits(m.ID, "edited")
	e.tickUntilQuiet(t, 2)
	time.Sleep(50 * time.Millisecond)
	if n := e.runner.promptCount(); n != 1 {
		t.Fatalf("an edit ran a turn: %d turns", n)
	}
}

func TestButton_AccessIsChecked(t *testing.T) {
	cfg := &config.PachcaGatewayConfig{DefaultAccess: config.AccessAdmins, Admins: []int64{1}}
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, cfg)
	e.startAt(t)
	e.dm(t, 1, "/model")
	e.tickUntilQuiet(t, 2)
	chat := e.fake.PersonalChat(1)
	var menu pachcafake.Message
	waitFor(t, func() bool {
		for _, m := range e.fake.Messages(chat) {
			if len(m.Buttons) > 0 {
				menu = m
				return true
			}
		}
		return false
	}, "the model menu")
	data := buttonFor(menu, "rpa/qwen3.6-35b-a3b")
	e.fake.AddUser(2, "stranger", "S")
	if err := e.fake.UserClicks(2, menu.ID, data); err != nil {
		t.Fatal(err)
	}
	e.tickUntilQuiet(t, 2)
	time.Sleep(50 * time.Millisecond)
	if got := e.bot.store.LastModel(); got != "" {
		t.Fatalf("a click from somebody without access switched the model to %q", got)
	}
}

func TestClient_ShortRateLimitIsWaitedOut(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{}, nil)
	var waited []time.Duration
	e.client.sleep = func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil }
	e.fake.AddFault(pachcafake.Fault{Route: "POST /messages", Status: 429, RetryAfter: 2})
	chat := e.fake.PersonalChat(5)
	if _, err := e.client.SendMessage(context.Background(), OutgoingMessage{Target: ChatTarget(chat), Content: "hi"}); err != nil {
		t.Fatalf("a short 429 should be waited out: %v", err)
	}
	if len(waited) != 1 || waited[0] != 2*time.Second {
		t.Fatalf("waited %v, want one pause of Retry-After", waited)
	}
	if n := len(e.fake.Messages(chat)); n != 1 {
		t.Fatalf("the retried POST created %d messages", n)
	}
}

func TestClient_DailyLimitAndLongPausesAreNotRetried(t *testing.T) {
	cases := []pachcafake.Fault{
		{Route: "POST /messages", Status: 429, RetryAfter: 3600, Body: `{"errors":[{"code":"rate_limit","message":"daily limit"}]}`, ContentType: "application/json"},
		{Route: "POST /messages", Status: 429, RetryAfter: 2, Body: `{"errors":[{"code":"rate_limit","message":"daily limit"}]}`, ContentType: "application/json"},
		{Route: "POST /messages", Status: 429, RetryAfter: 120},
	}
	for i, f := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			e := newTestEnv(t, pachcafake.Options{}, nil)
			slept := false
			e.client.sleep = func(context.Context, time.Duration) error { slept = true; return nil }
			e.fake.AddFault(f)
			_, err := e.client.SendMessage(context.Background(), OutgoingMessage{Target: ChatTarget(e.fake.PersonalChat(5)), Content: "hi"})
			if !IsRateLimited(err) || slept {
				t.Fatalf("want a rate limit error without a retry, got err=%v slept=%v", err, slept)
			}
		})
	}
}

func TestClient_ServerErrorOnPostIsNotRetried(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{}, nil)
	e.fake.AddFault(pachcafake.Fault{Route: "POST /messages", Status: 502})
	_, err := e.client.SendMessage(context.Background(), OutgoingMessage{Target: ChatTarget(e.fake.PersonalChat(5)), Content: "hi"})
	if !IsStatus(err, 502) {
		t.Fatalf("want the 502 back, got %v", err)
	}
	if n := len(e.fake.Calls("POST /messages")); n != 1 {
		t.Fatalf("a 502 on POST was retried: %d calls", n)
	}
}

func TestSender_TooLongIsSplitUntilItFits(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{MaxContentRunes: 300}, nil)
	chat := e.fake.PersonalChat(5)
	s := e.bot.newSender(context.Background(), e.client, ChatTarget(chat), 0)
	var words []string
	for i := 0; i < 250; i++ {
		words = append(words, fmt.Sprintf("w%03d", i))
	}
	text := strings.Join(words, " ")
	s.deliver(context.Background(), text, 0)
	var got []string
	for _, m := range e.fake.Messages(chat) {
		if utf8.RuneCountInString(m.Content) > 300 {
			t.Fatalf("a piece of %d runes was accepted", utf8.RuneCountInString(m.Content))
		}
		got = append(got, m.Content)
	}
	if len(got) < 4 {
		t.Fatalf("want the text split into pieces, got %d", len(got))
	}
	if strings.Join(strings.Fields(strings.Join(got, " ")), " ") != text {
		t.Fatal("splitting lost or reordered words")
	}
}

func TestSender_EmptyAnswerSettlesTheToolLine(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{}, nil)
	chat := e.fake.PersonalChat(5)
	s := e.bot.newSender(context.Background(), e.client, ChatTarget(chat), 0)
	s.currentTool = "read"
	s.stream()
	s.Flush()
	ms := e.fake.Messages(chat)
	if len(ms) != 1 || ms[0].Content != "Done." {
		t.Fatalf("want the tool line settled, got %+v", ms)
	}
}

func TestSplitMessage_ReopensACutFence(t *testing.T) {
	body := strings.Repeat("line of code\n", 60)
	text := "intro\n\n```go\n" + body + "```\n\nafter"
	chunks := splitMessage(text, 300)
	if len(chunks) < 2 {
		t.Fatalf("want several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if utf8.RuneCountInString(c) > 300 {
			t.Fatalf("chunk %d has %d runes", i, utf8.RuneCountInString(c))
		}
		if _, open := openFence(c); open {
			t.Fatalf("chunk %d leaves a fence open:\n%s", i, c)
		}
	}
	if !strings.HasPrefix(chunks[1], "```go\n") {
		t.Fatalf("the second chunk does not reopen the block: %q", chunks[1][:20])
	}
}

func TestRenderMarkdown(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"verbatim", "**bold** and `code`\n# Heading\n- item", "**bold** and `code`\n# Heading\n- item"},
		{"table", "| a | bb |\n|---|:--:|\n| ccc | d |", "```text\na    bb\nccc  d\n```"},
		{"quote", "> said\nplain", "│ said\nplain"},
		{"fence untouched", "```\n| a | b |\n|---|---|\n> q\n```", "```\n| a | b |\n|---|---|\n> q\n```"},
		{"unclosed fence", "```\n> q", "```\n> q"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderMarkdown(c.in); got != c.want {
				t.Fatalf("got\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

func TestMentionsAndCommands(t *testing.T) {
	if !mentions("hi @Coddy_Bot!", "coddy_bot", 7) || !mentions("hi <@7>", "coddy_bot", 7) {
		t.Fatal("a mention was missed")
	}
	if mentions("hi @coddy_bot_two", "coddy_bot", 7) || mentions("mail coddy_bot@x", "coddy_bot", 7) {
		t.Fatal("a longer nickname counted as a mention")
	}
	if got := stripMention("@coddy_bot  /clear", "coddy_bot", 7); got != "/clear" {
		t.Fatalf("stripMention: %q", got)
	}
	if got := stripMention("ask @coddy_bot_two and <@7> now", "coddy_bot", 7); got != "ask @coddy_bot_two and now" {
		t.Fatalf("stripMention kept the wrong parts: %q", got)
	}
	cmd, args := parseCommand("/Model openai/gpt-4o")
	if cmd != "model" || args != "openai/gpt-4o" {
		t.Fatalf("parseCommand: %q %q", cmd, args)
	}
	if cmd, _ := parseCommand("/usr/bin/env is a path"); cmd != "" {
		t.Fatalf("a path read as the command %q", cmd)
	}
	if knownCommand("permissions") {
		t.Fatal("/permissions must not reach the session from a chat")
	}
	if !knownCommand("plan") || !knownCommand("clear") {
		t.Fatal("a known command was not recognised")
	}
}

func TestButtonValue_LongModelIDTravelsAsADigest(t *testing.T) {
	long := "provider/" + strings.Repeat("x", 300)
	models := []config.ModelEntry{{Model: "short/one"}, {Model: long}}
	v := buttonValue(actionModel, long)
	if len(actionModel)+1+len(v) > buttonDataMax || !strings.HasPrefix(v, digestPrefix) {
		t.Fatalf("value %q does not fit a button", v)
	}
	if got, ok := resolveModelValue(models, v); !ok || got != long {
		t.Fatalf("digest resolved to %q, %v", got, ok)
	}
	if got, ok := resolveModelValue(models, "short/one"); !ok || got != "short/one" {
		t.Fatalf("a plain id resolved to %q, %v", got, ok)
	}
}

func TestTargetForKey(t *testing.T) {
	if tg, ok := targetForKey("pachca:user:42"); !ok || tg != UserTarget(42) {
		t.Fatalf("direct key: %+v", tg)
	}
	if tg, ok := targetForKey("pachca:chat:900:user:42"); !ok || tg != ChatTarget(900) {
		t.Fatalf("group key: %+v", tg)
	}
	if _, ok := targetForKey("$last_model"); ok {
		t.Fatal("a reserved entry resolved to a target")
	}
}

func TestPollState_PersistsTheWatermark(t *testing.T) {
	path := t.TempDir() + "/state.json"
	st := loadPollState(path)
	st.advance(mark{CreatedAt: "2026-10-04T12:00:00.000Z", ID: "b"})
	st.advance(mark{CreatedAt: "2026-10-04T12:00:00.000Z", ID: "a"}) // older: ignored
	again := loadPollState(path)
	if got := again.watermark(); got.ID != "b" {
		t.Fatalf("watermark after reload: %+v", got)
	}
}

func TestBot_AStartAfterAStopAnswersAgain(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.bot.pollEvery = bddPollFor
	run := func() (context.CancelFunc, chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- e.bot.Start(ctx) }()
		waitFor(t, func() bool { return e.bot.connectedClient() != nil && !e.bot.state.watermark().empty() }, "the bot to connect and read the history")
		return cancel, done
	}
	cancel, done := run()
	e.dm(t, 1, "first")
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the first turn")
	cancel()
	<-done
	// The hub starts the same Bot again after an error.
	cancel, done = run()
	defer func() { cancel(); <-done }()
	e.dm(t, 1, "second")
	waitFor(t, func() bool { return e.runner.promptCount() == 2 }, "a turn after the restart")
}

func TestSender_FinalAnswerOutlivesTheTurnContext(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{}, nil)
	chat := e.fake.PersonalChat(5)
	ctx, cancel := context.WithCancel(context.Background())
	s := e.bot.newSender(ctx, e.client, ChatTarget(chat), 0)
	s.responseBuf.WriteString("written before the timeout")
	cancel()
	s.Flush()
	ms := e.fake.Messages(chat)
	if len(ms) != 1 || ms[0].Content != "written before the timeout" {
		t.Fatalf("the answer of a cut-off turn was lost: %+v", ms)
	}
}

// group posts a message of userID into a group chat 600 and runs the poll.
func (e *testEnv) groupPost(t *testing.T, userID int64, text string, parent int64) pachcafake.Message {
	t.Helper()
	e.fake.AddUser(userID, fmt.Sprintf("u%d", userID), fmt.Sprintf("U%d", userID))
	e.fake.AddGroupChat(600, "dev")
	m := e.fake.UserPosts(pachcafake.Post{UserID: userID, ChatID: 600, Content: text, ParentMessageID: parent})
	e.tickUntilQuiet(t, 2)
	return m
}

func TestGroup_OnlyAMentionOrAReplyToTheBotIsAnswered(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.startAt(t)
	e.groupPost(t, 1, "/help", 0)
	e.groupPost(t, 1, "/plan do it", 0)
	time.Sleep(50 * time.Millisecond)
	if n := len(e.fake.Messages(600)); n != 2 {
		t.Fatalf("a command without a mention was answered in a group: %d messages", n)
	}
	if e.runner.promptCount() != 0 {
		t.Fatal("a settings command without a mention reached the session")
	}
	// A thread under the bot's message is not a reply to it.
	m := e.groupPost(t, 1, "@coddy_bot hi", 0)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the mention")
	var botMsg pachcafake.Message
	waitFor(t, func() bool {
		for _, x := range e.fake.Messages(600) {
			if x.UserID == e.fake.BotUserID() && x.ParentMessageID != nil && *x.ParentMessageID == m.ID {
				botMsg = x
				return true
			}
		}
		return false
	}, "the bot's answer")
	_, threadChat := e.fake.StartThread(botMsg.ID)
	e.fake.UserPosts(pachcafake.Post{UserID: 1, ChatID: threadChat, Content: "and more?"})
	e.tickUntilQuiet(t, 2)
	time.Sleep(50 * time.Millisecond)
	if n := e.runner.promptCount(); n != 1 {
		t.Fatalf("a thread message without a mention or a reply ran a turn: %d turns", n)
	}
}

func TestReply_AMentionAloneAsksAboutTheQuotedMessage(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.startAt(t)
	m := e.groupPost(t, 2, "deploy failed at step 3", 0)
	e.groupPost(t, 1, "@coddy_bot", m.ID)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the turn")
	e.runner.mu.Lock()
	got := e.runner.prompts[0]
	e.runner.mu.Unlock()
	if got != "> U2:\n> deploy failed at step 3" {
		t.Fatalf("prompt: %q", got)
	}
}

func TestReply_AnUnreadableMessageLeavesTheQuoteOut(t *testing.T) {
	scopes := []string{"messages:create", "messages:update", "chats:read", "profile:read", "webhooks:events:read", "webhooks:events:delete"}
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true, Scopes: scopes}, nil)
	e.startAt(t)
	m := e.groupPost(t, 2, "secret", 0)
	e.groupPost(t, 1, "@coddy_bot explain", m.ID)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the turn")
	e.runner.mu.Lock()
	got := e.runner.prompts[0]
	e.runner.mu.Unlock()
	if got != "explain" {
		t.Fatalf("prompt: %q", got)
	}
}

func TestReply_WithoutUsersScopeTheQuoteHasNoAuthor(t *testing.T) {
	scopes := []string{"messages:create", "messages:update", "messages:read", "chats:read", "profile:read", "webhooks:events:read", "webhooks:events:delete"}
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true, Scopes: scopes}, nil)
	e.startAt(t)
	m := e.groupPost(t, 2, "look", 0)
	e.groupPost(t, 1, "@coddy_bot ok?", m.ID)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the turn")
	e.runner.mu.Lock()
	got := e.runner.prompts[0]
	e.runner.mu.Unlock()
	if got != "> look\n\nok?" {
		t.Fatalf("prompt: %q", got)
	}
}

func TestPoll_ATransientLookupFailureKeepsTheEvent(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.startAt(t)
	e.fake.AddUser(2, "u2", "U2")
	e.fake.AddGroupChat(600, "dev")
	// A reply to the bot in a group needs the parent message; the lookup
	// fails twice and then answers.
	bm := e.fake.UserPosts(pachcafake.Post{UserID: 2, ChatID: 600, Content: "@coddy_bot hi"})
	e.tickUntilQuiet(t, 2)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the first turn")
	var answer pachcafake.Message
	waitFor(t, func() bool {
		for _, m := range e.fake.Messages(600) {
			if m.UserID == e.fake.BotUserID() && m.ParentMessageID != nil && *m.ParentMessageID == bm.ID {
				answer = m
				return true
			}
		}
		return false
	}, "the answer")
	e.bot.posted.forget(answer.ID) // so the reply needs a lookup
	e.fake.AddFault(pachcafake.Fault{Route: "GET /messages", Status: 503, Times: 2})
	e.fake.UserPosts(pachcafake.Post{UserID: 2, ChatID: 600, Content: "and?", ParentMessageID: answer.ID})
	e.tickUntilQuiet(t, 4)
	waitFor(t, func() bool { return e.runner.promptCount() == 2 }, "the reply after the lookup recovered")
}

func TestPoll_ADirectMessageNeedsNoChatLookup(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.startAt(t)
	e.fake.AddFault(pachcafake.Fault{Route: "GET /chats", Status: 500, Times: 10})
	e.dm(t, 1, "hello")
	e.tickUntilQuiet(t, 3)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the direct message despite the chat lookup failing")
}

func TestStripMention_KeepsLines(t *testing.T) {
	in := "@coddy_bot look:\n  line2\n\nline3"
	if got := stripMention(in, "coddy_bot", 5); got != "look:\n  line2\n\nline3" {
		t.Fatalf("got %q", got)
	}
}

func TestBot_AStopFinishesTheQueuedMessages(t *testing.T) {
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, nil)
	e.bot.pollEvery = bddPollFor
	gate := make(chan struct{}, 2)
	e.runner.mu.Lock()
	e.runner.gate = gate
	e.runner.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.bot.Start(ctx) }()
	waitFor(t, func() bool { return e.bot.connectedClient() != nil && !e.bot.state.watermark().empty() }, "the bot to start")
	e.dm(t, 1, "first")
	e.dm(t, 1, "second")
	// Both are taken: the first turn runs, the second waits in the queue,
	// and their events are gone from the history.
	waitFor(t, func() bool { return e.runner.promptCount() == 1 && len(e.fake.Events()) == 0 }, "the first turn and both events taken")
	cancel()
	gate <- struct{}{}
	gate <- struct{}{}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return")
	}
	if n := e.runner.promptCount(); n != 2 {
		t.Fatalf("a message taken before the stop was dropped: %d turns", n)
	}
}

func TestGroup_SettingsAreAdminOnly(t *testing.T) {
	cfg := &config.PachcaGatewayConfig{Admins: []int64{9}}
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, cfg)
	e.startAt(t)
	e.groupPost(t, 1, "@coddy_bot hello", 0)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the first turn")
	key := "pachca:chat:600:user:1"
	before := e.bot.store.Peek(key)
	for _, cmd := range []string{"@coddy_bot /clear", "@coddy_bot /model", "@coddy_bot /model rpa/qwen3.6-35b-a3b", "@coddy_bot /think"} {
		e.groupPost(t, 1, cmd, 0)
	}
	waitFor(t, func() bool {
		n := 0
		for _, m := range e.fake.Messages(600) {
			if strings.Contains(m.Content, "Only the bot's admins") {
				n++
			}
		}
		return n == 4
	}, "four refusals")
	if e.bot.store.Peek(key) != before || e.runner.promptCount() != 1 {
		t.Fatal("a non-admin changed the group's session")
	}
	// The same in a direct chat is the person's own business.
	e.dm(t, 1, "/clear")
	e.tickUntilQuiet(t, 2)
	waitFor(t, func() bool {
		for _, m := range e.fake.Messages(e.fake.PersonalChat(1)) {
			if m.Content == "New session started." {
				return true
			}
		}
		return false
	}, "/clear in a direct chat")
}

func TestGroup_AModelClickFromANonAdminIsRefused(t *testing.T) {
	cfg := &config.PachcaGatewayConfig{Admins: []int64{9}}
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, cfg)
	e.startAt(t)
	e.fake.AddUser(9, "admin", "Admin")
	e.groupPost(t, 9, "@coddy_bot /model", 0)
	var menu pachcafake.Message
	waitFor(t, func() bool {
		for _, m := range e.fake.Messages(600) {
			if len(m.Buttons) > 0 {
				menu = m
				return true
			}
		}
		return false
	}, "the admin's model menu")
	e.fake.AddUser(1, "u1", "U1")
	if err := e.fake.UserClicks(1, menu.ID, buttonFor(menu, "rpa/qwen3.6-35b-a3b")); err != nil {
		t.Fatal(err)
	}
	e.tickUntilQuiet(t, 2)
	time.Sleep(50 * time.Millisecond)
	if e.bot.store.LastModel() != "" {
		t.Fatal("a non-admin's click switched the model")
	}
}

func TestModel_ANonAdminPickIsNotRemembered(t *testing.T) {
	cfg := &config.PachcaGatewayConfig{Admins: []int64{9}}
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, cfg)
	e.startAt(t)
	e.dm(t, 1, "/model")
	e.tickUntilQuiet(t, 2)
	chat := e.fake.PersonalChat(1)
	var menu pachcafake.Message
	waitFor(t, func() bool {
		for _, m := range e.fake.Messages(chat) {
			if len(m.Buttons) > 0 {
				menu = m
				return true
			}
		}
		return false
	}, "the model menu")
	if err := e.fake.UserClicks(1, menu.ID, buttonFor(menu, "rpa/qwen3.6-35b-a3b")); err != nil {
		t.Fatal(err)
	}
	e.tickUntilQuiet(t, 2)
	waitFor(t, func() bool {
		id := e.bot.store.Peek("pachca:user:1")
		e.runner.mu.Lock()
		defer e.runner.mu.Unlock()
		st := e.runner.live[id]
		return st != nil && st.GetSelectedModelID() == "rpa/qwen3.6-35b-a3b"
	}, "the pick in the person's own session")
	if got := e.bot.store.LastModel(); got != "" {
		t.Fatalf("a non-admin's pick became the bot's default: %q", got)
	}
	e.dm(t, 1, "/model openai/gpt-4o")
	e.tickUntilQuiet(t, 2)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the typed /model turn")
	if got := e.bot.store.LastModel(); got != "" {
		t.Fatalf("a non-admin's typed /model became the bot's default: %q", got)
	}
}

func TestNonAdminTurnIsRestricted(t *testing.T) {
	cfg := &config.PachcaGatewayConfig{Admins: []int64{9}}
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, cfg)
	e.startAt(t)
	e.dm(t, 1, "hello")
	e.tickUntilQuiet(t, 2)
	waitFor(t, func() bool { return e.runner.promptCount() == 1 }, "the user's turn")
	e.dm(t, 9, "hello")
	e.tickUntilQuiet(t, 2)
	waitFor(t, func() bool { return e.runner.promptCount() == 2 }, "the admin's turn")
	e.runner.mu.Lock()
	got := append([]bool(nil), e.runner.restricted...)
	e.runner.mu.Unlock()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("restricted turns: %v, want [true false] for a user and an admin", got)
	}
	s := e.bot.newSender(context.Background(), e.client, UserTarget(1), 0)
	s.refuseApprovals = true
	res, err := s.RequestPermission(context.Background(), acp.PermissionRequestParams{SessionID: "x"})
	if err != nil || res == nil || res.OptionID != "reject" {
		t.Fatalf("a non-admin's own agent was approved: %+v %v", res, err)
	}
}

// In a shared group session everybody shares the asking session: only the
// bot's admins may answer what the agent asks.
func TestPermissionClickFromANonAdminInASharedGroupIsIgnored(t *testing.T) {
	cfg := &config.PachcaGatewayConfig{Admins: []int64{9}, DefaultIsolation: config.IsolationShared}
	e := newTestEnv(t, pachcafake.Options{IgnoreSelfMessages: true}, cfg)
	key := "pachca:chat:600" // a shared group's key names no person
	sid := e.bot.store.Get(key)
	p := &chatPrompt{sessionID: sid, options: []acp.PermissionOption{{OptionID: "allow", Name: "Allow"}}, answer: make(chan *acp.PermissionResult, 1)}
	e.bot.asks.mu.Lock()
	e.bot.asks.pending["tok"] = p
	e.bot.asks.mu.Unlock()
	e.bot.answerPermissionClick(context.Background(), e.client, buttonPayload{UserID: 1, ChatID: 600}, "tok:0", key)
	select {
	case <-p.answer:
		t.Fatal("a non-admin answered a permission request in a shared group")
	default:
	}
	e.bot.answerPermissionClick(context.Background(), e.client, buttonPayload{UserID: 9, ChatID: 600}, "tok:0", key)
	select {
	case res := <-p.answer:
		if res.OptionID != "allow" {
			t.Fatalf("the admin's answer: %+v", res)
		}
	default:
		t.Fatal("the admin's click did not answer the request")
	}
}
