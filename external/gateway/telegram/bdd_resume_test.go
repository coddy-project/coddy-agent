//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_resume.feature: drives /resume,
// the keyboard tap and the message that follows through the real handlers
// against the fake Bot API (internal/tgfake) and a server that keeps a fixed
// set of sessions, and asserts on which session the next prompt reached and on
// what the chat shows. What the user types lands in the fake's chat first, so
// the bot replies to a message Telegram holds, and a tap presses the keyboard
// the bot really sent, on the message it sent it with. No LLM and no network
// beyond the local httptest server.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

const (
	resumeChatID = int64(7007)
	resumeUserID = int64(1234)
)

// resumePrompt is one prompt a session received, with the id it went to.
type resumePrompt struct {
	sessionID string
	text      string
}

// resumeRunner is the server side of the bot for the /resume spec, modelled on
// session.Manager: a set of sessions kept on disk, each with a title and a
// last-update stamp, the ones made live through EnsureHTTPSession, and the
// prompt each session received. An id nobody stored becomes a new session, as
// the manager creates an empty bundle for one.
type resumeRunner struct {
	mu      sync.Mutex
	cfg     *config.Config
	rows    []acp.SessionListInfo
	live    map[string]*session.State
	prompts []resumePrompt
	// ensured records every id EnsureHTTPSession was asked for, so a test can
	// tell a refusal that never touched the server from one that did.
	ensured []string
	// forgot records the ids ForgetLiveSession was given.
	forgot []string
	// stored are the settings a stored session was saved with, restored when
	// it is made live, the way the manager reads them from session.json.
	stored map[string]storedSettings
}

// storedSettings is the model and the reasoning level of a stored session.
type storedSettings struct {
	model, reasoning string
}

func newResumeRunner() *resumeRunner {
	return &resumeRunner{
		cfg: &config.Config{
			Models: []config.ModelEntry{
				{Model: "stub/model"},
				{Model: "stub/thinker", ReasoningLevels: &[]string{"low", "medium", "high"}},
			},
			Agent: config.Agent{Model: "stub/model"},
		},
		live:   map[string]*session.State{},
		stored: map[string]storedSettings{},
	}
}

// keep adds a session to the ones the server stores.
func (r *resumeRunner) keep(id, title, updatedAt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row := acp.SessionListInfo{SessionID: id, CWD: "/work"}
	if title != "" {
		t := title
		row.Title = &t
	}
	if updatedAt != "" {
		u := updatedAt
		row.UpdatedAt = &u
	}
	r.rows = append(r.rows, row)
}

func (r *resumeRunner) hasRow(id string) bool {
	for _, row := range r.rows {
		if row.SessionID == id {
			return true
		}
	}
	return false
}

func (r *resumeRunner) EnsureHTTPSession(_ context.Context, sessionID, cwd string) (*session.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("empty session id")
	}
	r.ensured = append(r.ensured, sessionID)
	if st, ok := r.live[sessionID]; ok {
		return st, nil
	}
	st := &session.State{ID: sessionID, CWD: cwd, Mode: session.ModeAgent}
	if saved, ok := r.stored[sessionID]; ok {
		st.SelectedModelID, st.SelectedReasoning = saved.model, saved.reasoning
	}
	r.live[sessionID] = st
	if !r.hasRow(sessionID) {
		u := time.Now().UTC().Format(time.RFC3339)
		r.rows = append(r.rows, acp.SessionListInfo{SessionID: sessionID, CWD: cwd, UpdatedAt: &u})
	}
	return st, nil
}

// restart drops every live session, leaving the stored ones behind.
func (r *resumeRunner) restart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = map[string]*session.State{}
}

func (r *resumeRunner) HandleSessionPromptWithSender(_ context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, _ *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	var text strings.Builder
	for _, block := range params.Prompt {
		text.WriteString(block.Text)
	}
	r.mu.Lock()
	r.prompts = append(r.prompts, resumePrompt{sessionID: params.SessionID, text: text.String()})
	r.mu.Unlock()
	if sender != nil {
		_ = sender.SendSessionUpdate(params.SessionID, acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "noted"},
		})
	}
	return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn}, nil
}

func (r *resumeRunner) ForgetLiveSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forgot = append(r.forgot, id)
	delete(r.live, id)
}

func (r *resumeRunner) HandleSessionSetMode(context.Context, acp.SessionSetModeParams) error {
	return nil
}

func (r *resumeRunner) HandleSessionSetConfigOption(context.Context, acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	return &acp.SessionSetConfigOptionResult{}, nil
}

// HandleSessionList answers like the manager: the stored sessions, the most
// recently updated first.
func (r *resumeRunner) HandleSessionList(context.Context, acp.SessionListParams) (*acp.SessionListResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]acp.SessionListInfo, len(r.rows))
	copy(out, r.rows)
	sort.SliceStable(out, func(i, j int) bool {
		var a, b string
		if out[i].UpdatedAt != nil {
			a = *out[i].UpdatedAt
		}
		if out[j].UpdatedAt != nil {
			b = *out[j].UpdatedAt
		}
		return a > b
	})
	return &acp.SessionListResult{Sessions: out}, nil
}

func (r *resumeRunner) Cfg() *config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

func (r *resumeRunner) lastPrompt() (resumePrompt, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.prompts) == 0 {
		return resumePrompt{}, false
	}
	return r.prompts[len(r.prompts)-1], true
}

// resumeWorld holds the bot, the fake Bot API in front of it and the store the
// bot persists its chats in.
type resumeWorld struct {
	runner    *resumeRunner
	bot       *Bot
	f         *fakeAPI
	storeDir  string
	storePath string
}

func (w *resumeWorld) gatewayKeepingSessions(table *godog.Table) error {
	w.runner = newResumeRunner()
	for i, row := range table.Rows {
		if i == 0 {
			continue // header
		}
		if len(row.Cells) != 3 {
			return fmt.Errorf("row %d: want id, title and updated, got %d cells", i, len(row.Cells))
		}
		w.runner.keep(row.Cells[0].Value, row.Cells[1].Value, row.Cells[2].Value)
	}
	w.f = openFakeAPI(tgfake.Options{})
	dir, err := os.MkdirTemp("", "coddy-tg-resume-")
	if err != nil {
		return err
	}
	w.storeDir = dir
	w.storePath = filepath.Join(dir, "gateway_sessions.json")
	return w.buildBot()
}

// buildBot materialises a bot over the persisted store, as a process start does.
func (w *resumeWorld) buildBot() error {
	base, _, err := logger.New(config.Logger{Level: config.LogLevelError, Format: config.LogFormatText, Outputs: []string{config.LogOutputStderr}})
	if err != nil {
		return err
	}
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
		// /resume reaches every session the server keeps: an admin's command.
		Admins: []int64{resumeUserID},
	}, w.runner, "/work", logger.Component(base, logger.ComponentGatewayTelegram), w.storePath, nil)
	return nil
}

func (w *resumeWorld) close() {
	if w.f != nil {
		w.f.close()
		w.f = nil
	}
	if w.storeDir != "" {
		_ = os.RemoveAll(w.storeDir)
		w.storeDir = ""
	}
}

func (w *resumeWorld) sessionKey() string {
	return fmt.Sprintf("tg:user:%d", resumeUserID)
}

// userSends types text into the chat. The message lands in the fake's chat
// before the bot sees it, so the answer, which quotes it, replies to a message
// Telegram holds.
func (w *resumeWorld) userSends(text string) error {
	msg := w.f.userMessage(resumeChatID, resumeUserID, text)
	w.bot.processMessage(context.Background(), w.f.api, msg, w.sessionKey())
	return nil
}

// menu returns the newest message the chat shows with a keyboard under it:
// the one a person looks at and taps.
func (w *resumeWorld) menu() (tgfake.MessageView, error) {
	view := w.f.fake.Chat(resumeChatID)
	for i := len(view.Messages) - 1; i >= 0; i-- {
		if m := view.Messages[i]; m.From == "bot" && !m.Deleted && len(m.Keyboard) > 0 {
			return m, nil
		}
	}
	return tgfake.MessageView{}, fmt.Errorf("no message in the chat shows a keyboard:\n%s", view.Text())
}

// buttonTitle strips what the label adds around the title: the mark on the
// current session in front, the age of the session behind.
func buttonTitle(label string) string {
	label = strings.TrimPrefix(label, "✓ ")
	if i := strings.LastIndex(label, " · "); i >= 0 {
		label = label[:i]
	}
	return label
}

// menuButtons splits the keyboard of a message into the buttons that name a
// session, in keyboard order, and the labels of the navigation row.
func menuButtons(m tgfake.MessageView) (sessions []tgfake.InlineKeyboardButton, nav []string) {
	for _, row := range m.Keyboard {
		for _, btn := range row {
			if strings.HasPrefix(btn.CallbackData, "resume:s:") {
				sessions = append(sessions, btn)
			} else {
				nav = append(nav, btn.Text)
			}
		}
	}
	return sessions, nav
}

// sessionButtons returns the buttons of the menu that name a session, in
// keyboard order, leaving the navigation row out.
func (w *resumeWorld) sessionButtons() ([]tgfake.InlineKeyboardButton, error) {
	m, err := w.menu()
	if err != nil {
		return nil, err
	}
	sessions, _ := menuButtons(m)
	return sessions, nil
}

func (w *resumeWorld) offeredKeyboardWith(table *godog.Table) error {
	buttons, err := w.sessionButtons()
	if err != nil {
		return err
	}
	var got []string
	for _, btn := range buttons {
		got = append(got, buttonTitle(btn.Text))
	}
	var want []string
	for _, row := range table.Rows {
		want = append(want, row.Cells[0].Value)
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		return fmt.Errorf("keyboard buttons = %q, want %q", got, want)
	}
	return nil
}

// tap presses the session button titled title on the menu the chat shows and
// hands the tap to the bot. The label carries the age of the session as well,
// so the button is found by its title and pressed by its whole label: the
// query is one the fake issued, on the message that carries the keyboard.
func (w *resumeWorld) tap(title string) (*tgbotapi.CallbackQuery, error) {
	buttons, err := w.sessionButtons()
	if err != nil {
		return nil, err
	}
	for _, btn := range buttons {
		if buttonTitle(btn.Text) != title {
			continue
		}
		// The fake refuses a keyboard whose callback_data is over Telegram's
		// limit, so a button that exists already fits; the length is asserted
		// anyway, because an id an operator chose can be far longer than what
		// fits next to the prefix, and the button has to carry a digest then.
		if len(btn.CallbackData) > telegramCallbackDataMax {
			return nil, fmt.Errorf("callback_data for %q is %d bytes, over the telegram limit of %d",
				title, len(btn.CallbackData), telegramCallbackDataMax)
		}
		cbq, err := w.f.tap(resumeChatID, resumeUserID, btn.Text)
		if err != nil {
			return nil, err
		}
		w.bot.handleCallback(context.Background(), w.f.api, cbq)
		return cbq, nil
	}
	return nil, fmt.Errorf("no button titled %q on the keyboard:\n%s", title, w.f.fake.Chat(resumeChatID).Text())
}

func (w *resumeWorld) tapButton(title string) error {
	_, err := w.tap(title)
	return err
}

func (w *resumeWorld) agentPromptedInSession(id string) error {
	p, ok := w.runner.lastPrompt()
	if !ok {
		return fmt.Errorf("the agent was never prompted")
	}
	if p.sessionID != id {
		return fmt.Errorf("the last prompt %q went to session %q, want %q", p.text, p.sessionID, id)
	}
	return nil
}

func (w *resumeWorld) agentNotPromptedInSession(id string) error {
	if p, ok := w.runner.lastPrompt(); ok && p.sessionID == id {
		return fmt.Errorf("the prompt %q went to session %q", p.text, id)
	}
	return nil
}

// chatReceived looks for want in what the chat shows: the bot's messages as
// they read now, an edited one with its new text. A message Telegram refused
// never got there.
func (w *resumeWorld) chatReceived(want string) error {
	view := w.f.fake.Chat(resumeChatID)
	for _, m := range view.Messages {
		if m.From == "bot" && !m.Deleted && strings.Contains(m.Text, want) {
			return nil
		}
	}
	return fmt.Errorf("no message in the chat contains %q:\n%s", want, view.Text())
}

func (w *resumeWorld) keyboardMarksTheChatSession() error {
	current := w.bot.store.Peek(w.sessionKey())
	if current == "" {
		return fmt.Errorf("the chat has no session to mark")
	}
	buttons, err := w.sessionButtons()
	if err != nil {
		return err
	}
	for _, btn := range buttons {
		marked := strings.HasPrefix(btn.Text, "✓ ")
		names := btn.CallbackData == "resume:s:"+current
		switch {
		case names && !marked:
			return fmt.Errorf("the button for the chat's session %q is not marked: %q", current, btn.Text)
		case marked && !names:
			return fmt.Errorf("a button for another session is marked as current: %q", btn.Text)
		}
	}
	return nil
}

func (w *resumeWorld) sessionRunsOn(id, model, reasoning string) error {
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	w.runner.stored[id] = storedSettings{model: model, reasoning: reasoning}
	return nil
}

func (w *resumeWorld) restartOverTheSameStore() error {
	w.runner.restart()
	return w.buildBot()
}

func initializeResumeScenario(sc *godog.ScenarioContext) {
	w := &resumeWorld{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.close()
		return ctx, nil
	})

	sc.Step(`^a telegram gateway over a server keeping these sessions:$`, w.gatewayKeepingSessions)
	sc.Step(`^the user sends "([^"]*)"$`, w.userSends)
	sc.Step(`^the user is not an admin of the bot$`, func() error { w.bot.cfg.Admins = nil; return nil })
	sc.Step(`^the chat is offered a keyboard with the buttons:$`, w.offeredKeyboardWith)
	sc.Step(`^the user taps the button for "([^"]*)"$`, w.tapButton)
	sc.Step(`^the agent was prompted in the session "([^"]*)"$`, w.agentPromptedInSession)
	sc.Step(`^the agent was not prompted in the session "([^"]*)"$`, w.agentNotPromptedInSession)
	sc.Step(`^the chat received "([^"]*)"$`, w.chatReceived)
	sc.Step(`^the keyboard marks the session behind the chat as the current one$`, w.keyboardMarksTheChatSession)
	sc.Step(`^the gateway is restarted over the same session store$`, w.restartOverTheSameStore)
	sc.Step(`^the session "([^"]*)" runs on the model "([^"]*)" with reasoning "([^"]*)"$`, w.sessionRunsOn)
}

func TestResumeFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway-telegram-resume",
		ScenarioInitializer: initializeResumeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_telegram_resume.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("telegram resume feature suite failed")
	}
}
