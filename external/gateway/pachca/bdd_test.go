//go:build gateway || gateway.pachca

package pachca

// Godog harness for the features/gateway_pachca_*.feature specs: runs
// Bot.Start end to end - token check, profile, the events history poll -
// against the fake Pachca of internal/pachcafake on httptest, with a scripted
// agent behind the session runner. No LLM and no network beyond the local
// server.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/pachcafake"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	bddToken   = "pachca-test-token"
	bddSettle  = 5 * time.Second
	bddPollFor = 20 * time.Millisecond
)

// permissionScript makes a turn ask for a subagent's permission before it
// answers.
type permissionScript struct {
	agent, command string
}

// scriptRunner answers every prompt with a scripted text.
type scriptRunner struct {
	mu      sync.Mutex
	cfg     *config.Config
	live    map[string]*session.State
	answer  string
	prompts []string
	// sessions records the session each prompt ran in.
	sessions []string
	// restricted records whether each turn came with a restriction.
	restricted []bool
	perm       *permissionScript
	answered   []string
	// gate, when set, holds every turn until a value arrives.
	gate chan struct{}
}

func newScriptRunner() *scriptRunner {
	return &scriptRunner{
		cfg: &config.Config{
			Models: []config.ModelEntry{{Model: "openai/gpt-4o"}, {Model: "rpa/qwen3.6-35b-a3b"}},
			Agent:  config.Agent{Model: "openai/gpt-4o"},
		},
		live:   map[string]*session.State{},
		answer: "plain answer",
	}
}

func (r *scriptRunner) EnsureHTTPSession(_ context.Context, sessionID, cwd string) (*session.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("empty session id")
	}
	if st, ok := r.live[sessionID]; ok {
		return st, nil
	}
	st := &session.State{ID: sessionID, CWD: cwd, Mode: session.ModeAgent}
	r.live[sessionID] = st
	return st, nil
}

func (r *scriptRunner) HandleSessionPromptWithSender(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	r.mu.Lock()
	var text strings.Builder
	for _, block := range params.Prompt {
		text.WriteString(block.Text)
	}
	r.prompts = append(r.prompts, text.String())
	r.sessions = append(r.sessions, params.SessionID)
	r.restricted = append(r.restricted, opts != nil && opts.Restriction != nil)
	answer := r.answer
	perm := r.perm
	gate := r.gate
	r.mu.Unlock()
	if gate != nil {
		<-gate
	}

	if opts != nil && opts.BackgroundWake != nil {
		_ = sender.SendSessionUpdate(params.SessionID, acp.BackgroundWakeUpdate{
			SessionUpdate: "background_wake",
			Tasks:         []acp.BackgroundWakeTask{{ID: "bg_1", Kind: "command", Label: "make build", Status: "succeeded"}},
		})
	}
	if perm != nil {
		res, err := sender.RequestPermission(ctx, permissionParams(params.SessionID, perm.agent, perm.command))
		r.mu.Lock()
		if err == nil && res != nil {
			r.answered = append(r.answered, res.OptionID)
		}
		r.mu.Unlock()
	}
	_ = sender.SendSessionUpdate(params.SessionID, acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: answer},
	})
	return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn}, nil
}

func permissionParams(sessionID, agentName, command string) acp.PermissionRequestParams {
	return acp.PermissionRequestParams{
		SessionID:               sessionID,
		EffectivePermissionMode: config.PermModeAsk,
		ToolCall: acp.PermissionToolCall{
			ToolCallID: "call_1",
			Title:      "Subagent " + agentName + " wants to run a command",
			Content:    []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: command}}},
		},
		Options: []acp.PermissionOption{{OptionID: "allow", Name: "Allow"}, {OptionID: "reject", Name: "Reject"}},
	}
}

func (r *scriptRunner) ForgetLiveSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, id)
}

func (r *scriptRunner) HandleSessionSetConfigOption(_ context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.live[params.SessionID]
	if st == nil {
		return nil, fmt.Errorf("session not found: %s", params.SessionID)
	}
	if params.ConfigID != "model" || r.cfg.FindModelEntry(params.Value) == nil {
		return nil, fmt.Errorf("unknown option %s=%s", params.ConfigID, params.Value)
	}
	st.SetSelectedModelID(params.Value)
	return &acp.SessionSetConfigOptionResult{}, nil
}

func (r *scriptRunner) Cfg() *config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

func (r *scriptRunner) promptCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prompts)
}

// pachcaWorld holds the fake workspace, the bot and the run of Start.
type pachcaWorld struct {
	fake   *pachcafake.Server
	srv    *httptest.Server
	runner *scriptRunner
	bot    *Bot
	dir    string
	envSet bool

	cancel context.CancelFunc
	done   chan error

	people   map[string]int64
	groups   map[string]int64
	nextUser int64
	nextChat int64

	// lastPost is the person's latest message; lastThreadChat the chat of
	// the thread the latest message went to.
	lastPost       pachcafake.Message
	lastThreadChat int64
	menuID         int64
	permID         int64
	detached       chan *acp.PermissionResult
	pollsAtPost    int
	firstSessionID string
}

func newPachcaWorld() *pachcaWorld {
	return &pachcaWorld{people: map[string]int64{}, groups: map[string]int64{}, nextUser: 100, nextChat: 70000}
}

func (w *pachcaWorld) fakeWorkspace(nickname string) error {
	w.fake = pachcafake.New(pachcafake.Options{Token: bddToken, BotNickname: nickname})
	w.srv = httptest.NewServer(w.fake.Handler())
	return nil
}

func (w *pachcaWorld) gatewayPointedAtIt() error {
	dir, err := os.MkdirTemp("", "coddy-pachca-bdd-")
	if err != nil {
		return err
	}
	w.dir = dir
	w.runner = newScriptRunner()
	w.bot = w.newBot()
	return nil
}

func (w *pachcaWorld) newBot() *Bot {
	base, _, _ := logger.New(config.Logger{Level: config.LogLevelError, Format: config.LogFormatText, Outputs: []string{config.LogOutputStderr}})
	b := New(&config.PachcaGatewayConfig{
		Enabled: true, Token: bddToken, DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	}, w.runner, w.dir, logger.Component(base, logger.ComponentGatewayPachca),
		filepath.Join(w.dir, "sessions.json"), filepath.Join(w.dir, "state.json"), nil)
	b.apiBase = w.srv.URL
	b.pollEvery = bddPollFor
	return b
}

func (w *pachcaWorld) originFromEnvironment() error {
	w.bot.apiBase = ""
	w.envSet = true
	return os.Setenv(config.PachcaAPIBaseEnv, w.srv.URL)
}

func (w *pachcaWorld) start() error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.done = make(chan error, 1)
	bot := w.bot
	go func() { w.done <- bot.Start(ctx) }()
	// Started means connected and past the first read of the history.
	return waitUntil(func() bool {
		return bot.connectedClient() != nil && len(w.fake.Calls("GET /webhooks/events")) > 0
	}, "the bot to connect and read the events history")
}

func (w *pachcaWorld) stop() error {
	if w.cancel == nil {
		return nil
	}
	w.cancel()
	w.cancel = nil
	select {
	case err := <-w.done:
		return err
	case <-time.After(10 * time.Second):
		return fmt.Errorf("Start did not return after the stop")
	}
}

func (w *pachcaWorld) startAgain() error {
	w.bot = w.newBot()
	return w.start()
}

func waitUntil(cond func() bool, what string) error {
	deadline := time.Now().Add(bddSettle)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", what)
}

func (w *pachcaWorld) person(name string) int64 {
	if id, ok := w.people[name]; ok {
		return id
	}
	w.nextUser++
	w.people[name] = w.nextUser
	w.fake.AddUser(w.nextUser, name, strings.ToUpper(name[:1])+name[1:])
	return w.nextUser
}

func (w *pachcaWorld) group(name string) int64 {
	if id, ok := w.groups[name]; ok {
		return id
	}
	w.nextChat++
	w.groups[name] = w.nextChat
	w.fake.AddGroupChat(w.nextChat, name)
	return w.nextChat
}

func (w *pachcaWorld) post(p pachcafake.Post) {
	w.pollsAtPost = len(w.fake.Calls("GET /webhooks/events"))
	w.lastPost = w.fake.UserPosts(p)
}

func (w *pachcaWorld) writesDirect(name, text string) error {
	uid := w.person(name)
	w.post(pachcafake.Post{UserID: uid, ChatID: w.fake.PersonalChat(uid), Content: text})
	return nil
}

func (w *pachcaWorld) writesInGroup(name, text, group string) error {
	uid := w.person(name)
	w.post(pachcafake.Post{UserID: uid, ChatID: w.group(group), Content: text})
	return nil
}

// botMessages are the bot's messages in chatID, oldest first.
func (w *pachcaWorld) botMessages(chatID int64) []pachcafake.Message {
	var out []pachcafake.Message
	for _, m := range w.fake.Messages(chatID) {
		if m.UserID == w.fake.BotUserID() {
			out = append(out, m)
		}
	}
	return out
}

func (w *pachcaWorld) lastBotMessage(chatID int64) (pachcafake.Message, bool) {
	ms := w.botMessages(chatID)
	if len(ms) == 0 {
		return pachcafake.Message{}, false
	}
	return ms[len(ms)-1], true
}

func (w *pachcaWorld) waitBotMessage(chatID int64, match func(pachcafake.Message) bool, what string) (pachcafake.Message, error) {
	var found pachcafake.Message
	err := waitUntil(func() bool {
		for _, m := range w.botMessages(chatID) {
			if match(m) {
				found = m
				return true
			}
		}
		return false
	}, what)
	if err != nil {
		var got []string
		for _, m := range w.botMessages(chatID) {
			got = append(got, fmt.Sprintf("%q", m.Content))
		}
		return found, fmt.Errorf("%w; the bot's messages: %s", err, strings.Join(got, ", "))
	}
	return found, nil
}

func (w *pachcaWorld) directShows(name, text string) error {
	chat := w.fake.PersonalChat(w.person(name))
	_, err := w.waitBotMessage(chat, func(m pachcafake.Message) bool { return m.Content == text }, "bot message "+text)
	return err
}

func (w *pachcaWorld) directShowsContaining(name, text string) error {
	chat := w.fake.PersonalChat(w.person(name))
	_, err := w.waitBotMessage(chat, func(m pachcafake.Message) bool { return strings.Contains(m.Content, text) }, "bot message containing "+text)
	return err
}

func (w *pachcaWorld) groupShowsReply(group, text string) error {
	parent := w.lastPost.ID
	_, err := w.waitBotMessage(w.group(group), func(m pachcafake.Message) bool {
		return m.Content == text && m.ParentMessageID != nil && *m.ParentMessageID == parent
	}, "bot reply "+text)
	return err
}

func (w *pachcaWorld) groupShows(group, text string) error {
	_, err := w.waitBotMessage(w.group(group), func(m pachcafake.Message) bool { return m.Content == text }, "bot message "+text)
	return err
}

func (w *pachcaWorld) groupShowsContaining(group, text string) error {
	_, err := w.waitBotMessage(w.group(group), func(m pachcafake.Message) bool { return strings.Contains(m.Content, text) }, "bot message containing "+text)
	return err
}

// historyRead waits for two polls after the latest post: the event was read
// and whatever the bot does with it has been done.
func (w *pachcaWorld) historyRead() error {
	want := w.pollsAtPost + 3
	if err := waitUntil(func() bool { return len(w.fake.Calls("GET /webhooks/events")) >= want }, "the bot to read the history"); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return nil
}

func (w *pachcaWorld) groupShowsNothing(group string) error {
	if ms := w.botMessages(w.group(group)); len(ms) > 0 {
		return fmt.Errorf("the bot posted %d message(s), first %q", len(ms), ms[0].Content)
	}
	return nil
}

func (w *pachcaWorld) agentAnswers(text string) error {
	w.runner.mu.Lock()
	w.runner.answer = text
	w.runner.mu.Unlock()
	return nil
}

func (w *pachcaWorld) agentAsked(text string) error {
	return waitUntil(func() bool {
		w.runner.mu.Lock()
		defer w.runner.mu.Unlock()
		for _, p := range w.runner.prompts {
			if p == text {
				return true
			}
		}
		return false
	}, "the agent to be asked "+text)
}

func (w *pachcaWorld) agentAskedNothing() error {
	if n := w.runner.promptCount(); n != 0 {
		return fmt.Errorf("the agent was asked %d time(s)", n)
	}
	return nil
}

func (w *pachcaWorld) agentAskedTimes(n int) error {
	if got := w.runner.promptCount(); got != n {
		return fmt.Errorf("the agent was asked %d time(s), want %d", got, n)
	}
	return nil
}

func (w *pachcaWorld) historyLacksMessage() error {
	id := w.lastPost.ID
	return waitUntil(func() bool {
		for _, e := range w.fake.Events() {
			var p struct {
				Type string `json:"type"`
				ID   int64  `json:"id"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			if p.Type == "message" && p.ID == id {
				return false
			}
		}
		return true
	}, "the handled event to be deleted")
}

func (w *pachcaWorld) pachcaReceived(route string) error {
	if len(w.fake.Calls(route)) == 0 {
		return fmt.Errorf("Pachca never received %s", route)
	}
	return nil
}

func (w *pachcaWorld) botKnowsItself(nickname string) error {
	if w.bot.nickname != nickname {
		return fmt.Errorf("the bot knows itself as %q", w.bot.nickname)
	}
	return nil
}

func (w *pachcaWorld) repliesToBot(name, text, group string) error {
	chat := w.group(group)
	last, ok := w.lastBotMessage(chat)
	if !ok {
		return fmt.Errorf("no bot message in %s", group)
	}
	w.post(pachcafake.Post{UserID: w.person(name), ChatID: chat, Content: text, ParentMessageID: last.ID})
	return nil
}

func (w *pachcaWorld) repliesToBotDirect(name, text string) error {
	uid := w.person(name)
	chat := w.fake.PersonalChat(uid)
	last, ok := w.lastBotMessage(chat)
	if !ok {
		return fmt.Errorf("no bot message in the direct chat with %s", name)
	}
	w.post(pachcafake.Post{UserID: uid, ChatID: chat, Content: text, ParentMessageID: last.ID})
	return nil
}

func (w *pachcaWorld) repliesToThatMessage(name, text, group string) error {
	w.post(pachcafake.Post{UserID: w.person(name), ChatID: w.group(group), Content: text, ParentMessageID: w.lastPost.ID})
	return nil
}

func (w *pachcaWorld) agentAskedDoc(doc *godog.DocString) error {
	return w.agentAsked(strings.TrimSpace(doc.Content))
}

func (w *pachcaWorld) writesInThread(name, text, group string) error {
	last, ok := w.lastBotMessage(w.group(group))
	if !ok {
		return fmt.Errorf("no bot message in %s", group)
	}
	_, threadChat := w.fake.StartThread(last.ID)
	w.lastThreadChat = threadChat
	w.post(pachcafake.Post{UserID: w.person(name), ChatID: threadChat, Content: text})
	return nil
}

func (w *pachcaWorld) threadShows(text string) error {
	_, err := w.waitBotMessage(w.lastThreadChat, func(m pachcafake.Message) bool { return m.Content == text }, "bot message in the thread")
	return err
}

// wroteAndGotAnswer is a conversation that already exists.
func (w *pachcaWorld) wroteDirectAndGotAnswer(name, text string) error {
	before := w.runner.promptCount()
	if err := w.writesDirect(name, text); err != nil {
		return err
	}
	if err := waitUntil(func() bool { return w.runner.promptCount() > before }, "the first turn"); err != nil {
		return err
	}
	w.firstSessionID = w.store().Peek(w.directKey(name))
	return w.directShowsContaining(name, w.runner.answer)
}

func (w *pachcaWorld) wroteGroupAndGotAnswer(name, text, group string) error {
	before := w.runner.promptCount()
	if err := w.writesInGroup(name, text, group); err != nil {
		return err
	}
	if err := waitUntil(func() bool { return w.runner.promptCount() > before }, "the first turn"); err != nil {
		return err
	}
	return w.groupShowsContaining(group, w.runner.answer)
}

func (w *pachcaWorld) store() interface{ Peek(string) string } { return w.bot.store }

func (w *pachcaWorld) directKey(name string) string {
	return fmt.Sprintf("pachca:user:%d", w.person(name))
}

func (w *pachcaWorld) groupKey(name, group string) string {
	return fmt.Sprintf("pachca:chat:%d:user:%d", w.group(group), w.person(name))
}

func (w *pachcaWorld) nextMessageNewSession(name string) error {
	before := w.runner.promptCount()
	if err := w.writesDirect(name, "again"); err != nil {
		return err
	}
	if err := waitUntil(func() bool { return w.runner.promptCount() > before }, "the next turn"); err != nil {
		return err
	}
	w.runner.mu.Lock()
	last := w.runner.sessions[len(w.runner.sessions)-1]
	w.runner.mu.Unlock()
	if last == "" || last == w.firstSessionID {
		return fmt.Errorf("the next message ran in session %q, the one before /clear", last)
	}
	return nil
}

func (w *pachcaWorld) modelMenuWithButton(name, model string) error {
	chat := w.fake.PersonalChat(w.person(name))
	m, err := w.waitBotMessage(chat, func(m pachcafake.Message) bool { return buttonFor(m, model) != "" }, "a model menu")
	if err != nil {
		return err
	}
	w.menuID = m.ID
	return nil
}

func buttonFor(m pachcafake.Message, label string) string {
	for _, row := range m.Buttons {
		for _, b := range row {
			if strings.TrimPrefix(b.Text, "• ") == label {
				return b.Data
			}
		}
	}
	return ""
}

func (w *pachcaWorld) clicksModel(name, model string) error {
	m, ok := w.fake.Message(w.menuID)
	if !ok {
		return fmt.Errorf("no model menu")
	}
	return w.fake.UserClicks(w.person(name), m.ID, buttonFor(m, model))
}

func (w *pachcaWorld) sessionModelIs(name, model string) error {
	return waitUntil(func() bool {
		id := w.bot.store.Peek(w.directKey(name))
		w.runner.mu.Lock()
		defer w.runner.mu.Unlock()
		st := w.runner.live[id]
		return st != nil && st.GetSelectedModelID() == model
	}, "the session model "+model)
}

func (w *pachcaWorld) menuMarksCurrent(model string) error {
	return waitUntil(func() bool {
		m, ok := w.fake.Message(w.menuID)
		if !ok || !strings.Contains(m.Content, "`"+model+"`") {
			return false
		}
		for _, row := range m.Buttons {
			for _, b := range row {
				if b.Text == "• "+model {
					return true
				}
			}
		}
		return false
	}, "the menu to mark "+model)
}

func (w *pachcaWorld) turnAsksPermission(agentName, command string) error {
	w.runner.mu.Lock()
	w.runner.perm = &permissionScript{agent: agentName, command: command}
	w.runner.mu.Unlock()
	return nil
}

func (w *pachcaWorld) permissionRequestShown(name, agentName, allow, reject string) error {
	chat := w.fake.PersonalChat(w.person(name))
	m, err := w.waitBotMessage(chat, func(m pachcafake.Message) bool {
		return strings.Contains(m.Content, agentName) && buttonFor(m, allow) != "" && buttonFor(m, reject) != ""
	}, "a permission request")
	if err != nil {
		return err
	}
	w.permID = m.ID
	return nil
}

func (w *pachcaWorld) clicksPermission(name, label string) error {
	m, ok := w.fake.Message(w.permID)
	if !ok {
		return fmt.Errorf("no permission request")
	}
	return w.fake.UserClicks(w.person(name), m.ID, buttonFor(m, label))
}

func (w *pachcaWorld) subagentAnswered(option string) error {
	if w.detached != nil {
		select {
		case res := <-w.detached:
			if res == nil || res.OptionID != option {
				return fmt.Errorf("the subagent was answered %+v", res)
			}
			return nil
		case <-time.After(bddSettle):
			return fmt.Errorf("the background subagent was never answered")
		}
	}
	return waitUntil(func() bool {
		w.runner.mu.Lock()
		defer w.runner.mu.Unlock()
		return len(w.runner.answered) > 0 && w.runner.answered[len(w.runner.answered)-1] == option
	}, "the subagent to be answered "+option)
}

func (w *pachcaWorld) permissionReads(note string) error {
	return waitUntil(func() bool {
		m, ok := w.fake.Message(w.permID)
		return ok && strings.HasSuffix(m.Content, note) && len(m.Buttons) == 0
	}, "the request to read "+note)
}

func (w *pachcaWorld) backgroundSubagentAsks(agentName, name, command string) error {
	parent := w.bot.store.Peek(w.directKey(name))
	if parent == "" {
		return fmt.Errorf("%s has no session", name)
	}
	w.detached = make(chan *acp.PermissionResult, 1)
	go func() {
		res, _ := w.bot.RequestDetachedPermission(context.Background(), agent.DetachedPermissionRequest{
			ParentSessionID: parent, ChildSessionID: "sess_child_" + agentName, AgentName: agentName,
			Params: permissionParams(parent, agentName, command),
		})
		w.detached <- res
	}()
	return nil
}

func (w *pachcaWorld) wake(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("no session to wake")
	}
	took, err := w.bot.RunBackgroundWake(context.Background(), agent.Wake{
		SessionID: sessionID,
		Tasks:     []bgtask.Snapshot{{ID: "bg_1", SessionID: sessionID, Kind: bgtask.KindCommand, Label: "make build"}},
	})
	if err != nil {
		return err
	}
	if !took {
		return fmt.Errorf("the bot did not take the wake of %s", sessionID)
	}
	return nil
}

func (w *pachcaWorld) wakeDirect(name string) error {
	return w.wake(w.bot.store.Peek(w.directKey(name)))
}

func (w *pachcaWorld) wakeGroup(name, group string) error {
	return w.wake(w.bot.store.Peek(w.groupKey(name, group)))
}

func (w *pachcaWorld) adminsAre(notAdmin, admin string) error {
	w.person(notAdmin)
	w.bot.cfg.Admins = []int64{w.person(admin)}
	return nil
}

func (w *pachcaWorld) cleanup() {
	_ = w.stop()
	if w.envSet {
		_ = os.Unsetenv(config.PachcaAPIBaseEnv)
	}
	if w.srv != nil {
		w.srv.Close()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
}

func initializePachcaScenario(sc *godog.ScenarioContext) {
	w := newPachcaWorld()
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return ctx, nil
	})
	sc.Given(`^a fake Pachca workspace whose bot is "([^"]*)"$`, w.fakeWorkspace)
	sc.Given(`^a pachca gateway over a scripted agent pointed at it$`, w.gatewayPointedAtIt)
	sc.Given(`^the environment names the fake as the Pachca API base$`, w.originFromEnvironment)
	sc.Step(`^the agent answers with "([^"]*)"$`, w.agentAnswers)
	sc.Step(`^the bot is started$`, w.start)
	sc.Step(`^the bot is stopped$`, w.stop)
	sc.Step(`^the bot is started again$`, w.startAgain)
	sc.Step(`^the person "([^"]*)" writes "([^"]*)" in a direct chat$`, w.writesDirect)
	sc.Step(`^the person "([^"]*)" writes "([^"]*)" in the group "([^"]*)"$`, w.writesInGroup)
	sc.Step(`^the person "([^"]*)" replies "([^"]*)" to the bot's last message in the group "([^"]*)"$`, w.repliesToBot)
	sc.Step(`^the person "([^"]*)" writes "([^"]*)" in a thread under the bot's last message in the group "([^"]*)"$`, w.writesInThread)
	sc.Step(`^the person "([^"]*)" wrote "([^"]*)" in a direct chat and got an answer$`, w.wroteDirectAndGotAnswer)
	sc.Step(`^the person "([^"]*)" wrote "([^"]*)" in the group "([^"]*)" and got an answer$`, w.wroteGroupAndGotAnswer)
	sc.Step(`^the bot has read the events history$`, w.historyRead)
	sc.Step(`^the direct chat with "([^"]*)" shows a bot message "([^"]*)"$`, w.directShows)
	sc.Step(`^the direct chat with "([^"]*)" shows a bot message containing "([^"]*)"$`, w.directShowsContaining)
	sc.Step(`^the group "([^"]*)" shows a bot message "([^"]*)" replying to that message$`, w.groupShowsReply)
	sc.Step(`^the group "([^"]*)" shows a bot message "([^"]*)"$`, w.groupShows)
	sc.Step(`^the group "([^"]*)" shows a bot message containing "([^"]*)"$`, w.groupShowsContaining)
	sc.Step(`^the group "([^"]*)" shows no bot message$`, w.groupShowsNothing)
	sc.Step(`^the thread shows a bot message "([^"]*)"$`, w.threadShows)
	sc.Step(`^the agent was asked "([^"]*)"$`, w.agentAsked)
	sc.Step(`^the agent was asked:$`, w.agentAskedDoc)
	sc.Step(`^the person "([^"]*)" replies "([^"]*)" to the bot's last message in the direct chat$`, w.repliesToBotDirect)
	sc.Step(`^the person "([^"]*)" replies "([^"]*)" to that message in the group "([^"]*)"$`, w.repliesToThatMessage)
	sc.Step(`^the agent was asked nothing$`, w.agentAskedNothing)
	sc.Step(`^"([^"]*)" is not an admin of the Pachca bot and "([^"]*)" is$`, w.adminsAre)
	sc.Step(`^"([^"]*)" is an admin of the Pachca bot$`, func(name string) error {
		w.bot.cfg.Admins = append(w.bot.cfg.Admins, w.person(name))
		return nil
	})
	sc.Step(`^the agent was asked (\d+) times?$`, w.agentAskedTimes)
	sc.Step(`^the events history no longer holds that message$`, w.historyLacksMessage)
	sc.Step(`^Pachca received "([^"]*)"$`, w.pachcaReceived)
	sc.Step(`^the bot knows itself as "([^"]*)"$`, w.botKnowsItself)
	sc.Step(`^the next message of "([^"]*)" runs in a new session$`, w.nextMessageNewSession)
	sc.Step(`^the direct chat with "([^"]*)" shows a model menu with a button for "([^"]*)"$`, w.modelMenuWithButton)
	sc.Step(`^"([^"]*)" clicks the button for "([^"]*)"$`, w.clicksModel)
	sc.Step(`^the session model of "([^"]*)" is "([^"]*)"$`, w.sessionModelIs)
	sc.Step(`^the model menu marks "([^"]*)" as current$`, w.menuMarksCurrent)
	sc.Step(`^the agent's turn has a subagent "([^"]*)" that asks to run "([^"]*)"$`, w.turnAsksPermission)
	sc.Step(`^the direct chat with "([^"]*)" shows a permission request naming "([^"]*)" with the buttons "([^"]*)" and "([^"]*)"$`, w.permissionRequestShown)
	sc.Step(`^"([^"]*)" clicks "([^"]*)"$`, w.clicksPermission)
	sc.Step(`^the subagent is answered "([^"]*)"$`, w.subagentAnswered)
	sc.Step(`^the permission request reads "([^"]*)"$`, w.permissionReads)
	sc.Step(`^the background subagent "([^"]*)" of the session of "([^"]*)" asks to run "([^"]*)"$`, w.backgroundSubagentAsks)
	sc.Step(`^a background task of the session of "([^"]*)" finishes and wakes it$`, w.wakeDirect)
	sc.Step(`^a background task of the session of "([^"]*)" in the group "([^"]*)" finishes and wakes it$`, w.wakeGroup)
}

func TestGatewayPachcaFeatures(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway pachca",
		ScenarioInitializer: initializePachcaScenario,
		Options: &godog.Options{
			Format: "pretty",
			Paths: []string{
				"../../../features/gateway_pachca_polling.feature",
				"../../../features/gateway_pachca_commands.feature",
				"../../../features/gateway_pachca_subagent_permission.feature",
				"../../../features/gateway_pachca_wake.feature",
			},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("gateway pachca feature suites failed")
	}
}
