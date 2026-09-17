//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_resume.feature: drives /resume,
// the keyboard tap and the message that follows through the real handlers
// against a stub Telegram API and a server that keeps a fixed set of sessions,
// and asserts on which session the next prompt reached. No LLM and no network
// beyond the local httptest server.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
}

func newResumeRunner() *resumeRunner {
	return &resumeRunner{
		cfg:  &config.Config{Models: []config.ModelEntry{{Model: "stub/model"}}, Agent: config.Agent{Model: "stub/model"}},
		live: map[string]*session.State{},
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

// apiCall is one request the bot made to the stub Telegram API.
type apiCall struct {
	method string
	form   url.Values
}

// resumeWorld holds the bot, the stub API and what the bot posted.
type resumeWorld struct {
	mu sync.Mutex

	runner    *resumeRunner
	bot       *Bot
	api       *tgbotapi.BotAPI
	srv       *httptest.Server
	storeDir  string
	storePath string
	calls     []apiCall
	nextMsg   int
}

func (w *resumeWorld) handler(rw http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	w.mu.Lock()
	w.calls = append(w.calls, apiCall{method: r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], form: r.PostForm})
	w.nextMsg++
	id := w.nextMsg
	w.mu.Unlock()
	rw.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(rw, `{"ok":true,"result":{"message_id":%d,"date":0,"chat":{"id":%d,"type":"private"}}}`,
		id, resumeChatID)
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
	w.srv = httptest.NewServer(http.HandlerFunc(w.handler))
	w.api = &tgbotapi.BotAPI{Token: "TESTTOKEN", Client: &http.Client{}, Buffer: 100}
	w.api.SetAPIEndpoint(w.srv.URL + "/bot%s/%s")
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
	}, w.runner, "/work", logger.Component(base, logger.ComponentGatewayTelegram), w.storePath, nil)
	return nil
}

func (w *resumeWorld) close() {
	if w.srv != nil {
		w.srv.Close()
		w.srv = nil
	}
	if w.storeDir != "" {
		_ = os.RemoveAll(w.storeDir)
		w.storeDir = ""
	}
}

func (w *resumeWorld) sessionKey() string {
	return fmt.Sprintf("tg:user:%d", resumeUserID)
}

// commandMessage shapes text the way Telegram delivers it: a message that
// starts with a slash carries a bot_command entity over its first word, so
// Command and CommandArguments split it as the real client would.
func commandMessage(text string) *tgbotapi.Message {
	msg := &tgbotapi.Message{
		MessageID: 1,
		From:      &tgbotapi.User{ID: resumeUserID},
		Chat:      &tgbotapi.Chat{ID: resumeChatID, Type: "private"},
		Text:      text,
	}
	if strings.HasPrefix(text, "/") {
		length := len(text)
		if i := strings.IndexByte(text, ' '); i > 0 {
			length = i
		}
		msg.Entities = []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: length}}
	}
	return msg
}

func (w *resumeWorld) userSends(text string) error {
	w.bot.processMessage(context.Background(), w.api, commandMessage(text), w.sessionKey())
	return nil
}

// lastKeyboard returns the inline keyboard of the most recent message that
// carried one.
func (w *resumeWorld) lastKeyboard() (tgbotapi.InlineKeyboardMarkup, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := len(w.calls) - 1; i >= 0; i-- {
		raw := w.calls[i].form.Get("reply_markup")
		if raw == "" {
			continue
		}
		var kb tgbotapi.InlineKeyboardMarkup
		if err := json.Unmarshal([]byte(raw), &kb); err != nil {
			return kb, fmt.Errorf("reply_markup is not an inline keyboard: %v", err)
		}
		return kb, nil
	}
	return tgbotapi.InlineKeyboardMarkup{}, fmt.Errorf("no keyboard in %d api calls", len(w.calls))
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

// sessionButtons returns the buttons that name a session, in keyboard order,
// leaving the navigation row out.
func (w *resumeWorld) sessionButtons() ([]tgbotapi.InlineKeyboardButton, error) {
	kb, err := w.lastKeyboard()
	if err != nil {
		return nil, err
	}
	var out []tgbotapi.InlineKeyboardButton
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData == nil || !strings.HasPrefix(*btn.CallbackData, "resume:s:") {
				continue
			}
			out = append(out, btn)
		}
	}
	return out, nil
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

func (w *resumeWorld) tapButton(title string) error {
	buttons, err := w.sessionButtons()
	if err != nil {
		return err
	}
	for _, btn := range buttons {
		if buttonTitle(btn.Text) != title {
			continue
		}
		if len(*btn.CallbackData) > telegramCallbackDataMax {
			return fmt.Errorf("callback_data for %q is %d bytes, over the telegram limit of %d",
				title, len(*btn.CallbackData), telegramCallbackDataMax)
		}
		w.bot.handleCallback(context.Background(), w.api, &tgbotapi.CallbackQuery{
			ID:   "cb1",
			From: &tgbotapi.User{ID: resumeUserID},
			Message: &tgbotapi.Message{
				MessageID: 2,
				Chat:      &tgbotapi.Chat{ID: resumeChatID, Type: "private"},
			},
			Data: *btn.CallbackData,
		})
		return nil
	}
	return fmt.Errorf("no button titled %q on the keyboard", title)
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

// sentTexts returns the text of every message the bot posted or edited.
func (w *resumeWorld) sentTexts() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, call := range w.calls {
		if t := call.form.Get("text"); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (w *resumeWorld) chatReceived(want string) error {
	for _, t := range w.sentTexts() {
		if strings.Contains(t, want) {
			return nil
		}
	}
	return fmt.Errorf("no message sent to the chat contains %q; sent %q", want, w.sentTexts())
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
		names := *btn.CallbackData == "resume:s:"+current
		switch {
		case names && !marked:
			return fmt.Errorf("the button for the chat's session %q is not marked: %q", current, btn.Text)
		case marked && !names:
			return fmt.Errorf("a button for another session is marked as current: %q", btn.Text)
		}
	}
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
	sc.Step(`^the chat is offered a keyboard with the buttons:$`, w.offeredKeyboardWith)
	sc.Step(`^the user taps the button for "([^"]*)"$`, w.tapButton)
	sc.Step(`^the agent was prompted in the session "([^"]*)"$`, w.agentPromptedInSession)
	sc.Step(`^the chat received "([^"]*)"$`, w.chatReceived)
	sc.Step(`^the keyboard marks the session behind the chat as the current one$`, w.keyboardMarksTheChatSession)
	sc.Step(`^the gateway is restarted over the same session store$`, w.restartOverTheSameStore)
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
