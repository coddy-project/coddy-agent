//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_model_switch.feature: drives
// /model and the inline-keyboard tap through the real handlers against a stub
// Telegram API, and asserts on the session model plus the debug trail. No LLM
// and no network beyond the local httptest server.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	modelSwitchChatID = int64(4242)
	modelSwitchUserID = int64(309275343)
	// longModelID is longer than the 64 bytes Telegram allows in callback_data
	// once the "model:" prefix is added.
	longModelID = "neuraldeep/qwen3-235b-a22b-instruct-2507-fp8-extended-context-preview"
)

// stubRunner is the session side of the bot, modelled on session.Manager: only
// a live session can be configured, and a session on disk becomes live through
// EnsureHTTPSession. That is what a gateway restart looks like from here.
type stubRunner struct {
	mu     sync.Mutex
	cfg    *config.Config
	live   map[string]*session.State
	onDisk map[string]*session.State
}

func newStubRunner(cfg *config.Config) *stubRunner {
	return &stubRunner{
		cfg:    cfg,
		live:   map[string]*session.State{},
		onDisk: map[string]*session.State{},
	}
}

func (r *stubRunner) EnsureHTTPSession(_ context.Context, sessionID, cwd string) (*session.State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("empty session id")
	}
	if st, ok := r.live[sessionID]; ok {
		return st, nil
	}
	if st, ok := r.onDisk[sessionID]; ok {
		r.live[sessionID] = st
		return st, nil
	}
	st := &session.State{ID: sessionID, CWD: cwd, Mode: session.ModeAgent}
	r.live[sessionID] = st
	r.onDisk[sessionID] = st
	return st, nil
}

// restart drops every live session, leaving the persisted copies behind.
func (r *stubRunner) restart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = map[string]*session.State{}
}

func (r *stubRunner) HandleSessionPromptWithSender(context.Context, acp.SessionPromptParams, acp.UpdateSender, *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn}, nil
}

func (r *stubRunner) ForgetLiveSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, id)
}

func (r *stubRunner) HandleSessionSetMode(_ context.Context, params acp.SessionSetModeParams) error {
	r.mu.Lock()
	st := r.live[params.SessionID]
	r.mu.Unlock()
	if st == nil {
		return fmt.Errorf("session not found: %s", params.SessionID)
	}
	st.SetMode(params.ModeID)
	return nil
}

func (r *stubRunner) HandleSessionSetConfigOption(_ context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	r.mu.Lock()
	st := r.live[params.SessionID]
	models := r.cfg.Models
	r.mu.Unlock()
	if st == nil {
		return nil, fmt.Errorf("session not found: %s", params.SessionID)
	}
	if params.ConfigID != "model" {
		return nil, fmt.Errorf("unknown config option: %q", params.ConfigID)
	}
	for i := range models {
		if models[i].Model == params.Value {
			st.SetSelectedModelID(params.Value)
			return &acp.SessionSetConfigOptionResult{}, nil
		}
	}
	return nil, fmt.Errorf("unknown model value: %q", params.Value)
}

func (r *stubRunner) Cfg() *config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

// modelSwitchWorld holds the bot, the stub API and everything the steps assert on.
type modelSwitchWorld struct {
	mu sync.Mutex

	runner   *stubRunner
	bot      *Bot
	api      *tgbotapi.BotAPI
	srv      *httptest.Server
	logDir   string
	logPath  string
	logClose io.Closer
	logCfg   config.Logger
	messages []url.Values // every outgoing sendMessage / editMessageText form
	nextMsg  int
}

// logText returns everything the process has written to its log file.
func (w *modelSwitchWorld) logText() string {
	if w.logPath == "" {
		return ""
	}
	data, err := os.ReadFile(w.logPath) //nolint:gosec // path built by this test
	if err != nil {
		return ""
	}
	return string(data)
}

func (w *modelSwitchWorld) handler(rw http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	w.mu.Lock()
	w.messages = append(w.messages, r.PostForm)
	w.nextMsg++
	id := w.nextMsg
	w.mu.Unlock()
	rw.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(rw, `{"ok":true,"result":{"message_id":%d,"date":0,"chat":{"id":%d,"type":"private"}}}`,
		id, modelSwitchChatID)
}

func (w *modelSwitchWorld) gatewayWithModels(a, b string) error {
	w.logCfg = config.Logger{Level: config.LogLevelInfo, Format: config.LogFormatJSON}
	w.runner = newStubRunner(&config.Config{
		Models: []config.ModelEntry{{Model: a}, {Model: b}},
		Agent:  config.Agent{Model: a},
	})
	w.srv = httptest.NewServer(http.HandlerFunc(w.handler))
	w.api = &tgbotapi.BotAPI{Token: "TESTTOKEN", Client: &http.Client{}, Buffer: 100}
	w.api.SetAPIEndpoint(w.srv.URL + "/bot%s/%s")
	return nil
}

// buildBot materialises the bot with the log configuration accumulated so far,
// through the same logger.New every entrypoint uses.
func (w *modelSwitchWorld) buildBot() error {
	if w.bot != nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "coddy-tg-bdd-")
	if err != nil {
		return err
	}
	w.logDir = dir
	w.logPath = filepath.Join(dir, "coddy.log")

	cfg := w.logCfg
	cfg.Outputs = []string{config.LogOutputFile}
	cfg.File = w.logPath
	base, closer, err := logger.New(cfg)
	if err != nil {
		return err
	}
	w.logClose = closer
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	},
		w.runner, dir, logger.Component(base, logger.ComponentGatewayTelegram), "", nil)
	return nil
}

func (w *modelSwitchWorld) componentConfiguredAt(name, level string) error {
	w.logCfg.Levels = append(w.logCfg.Levels, config.LoggerComponentLevel{Component: name, Level: level})
	return nil
}

func (w *modelSwitchWorld) longModelIsConfigured() error {
	w.runner.cfg.Models = append(w.runner.cfg.Models, config.ModelEntry{Model: longModelID})
	return nil
}

func (w *modelSwitchWorld) sendCommand(text string) error {
	if err := w.buildBot(); err != nil {
		return err
	}
	msg := &tgbotapi.Message{
		MessageID: 1,
		From:      &tgbotapi.User{ID: modelSwitchUserID},
		Chat:      &tgbotapi.Chat{ID: modelSwitchChatID, Type: "private"},
		Text:      text,
		Entities:  []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(text)}},
	}
	w.bot.processMessage(context.Background(), w.api, msg, w.sessionKey())
	return nil
}

func (w *modelSwitchWorld) sessionKey() string {
	return fmt.Sprintf("tg:user:%d", modelSwitchUserID)
}

// keyboardData returns the callback_data of the button labelled for model.
func (w *modelSwitchWorld) keyboardData(model string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := len(w.messages) - 1; i >= 0; i-- {
		raw := w.messages[i].Get("reply_markup")
		if raw == "" {
			continue
		}
		var kb tgbotapi.InlineKeyboardMarkup
		if err := json.Unmarshal([]byte(raw), &kb); err != nil {
			continue
		}
		for _, row := range kb.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData == nil {
					continue
				}
				if strings.TrimPrefix(btn.Text, "✓ ") != model {
					continue
				}
				if len(*btn.CallbackData) > telegramCallbackDataMax {
					return "", fmt.Errorf("callback_data for %q is %d bytes, over the telegram limit of %d",
						model, len(*btn.CallbackData), telegramCallbackDataMax)
				}
				return *btn.CallbackData, nil
			}
		}
	}
	return "", fmt.Errorf("no button for model %q in %d outgoing messages", model, len(w.messages))
}

func (w *modelSwitchWorld) tapButton(model string) error {
	data, err := w.keyboardData(model)
	if err != nil {
		return err
	}
	w.bot.handleCallback(context.Background(), w.api, &tgbotapi.CallbackQuery{
		ID:   "cb1",
		From: &tgbotapi.User{ID: modelSwitchUserID},
		Message: &tgbotapi.Message{
			MessageID: 2,
			Chat:      &tgbotapi.Chat{ID: modelSwitchChatID, Type: "private"},
		},
		Data: data,
	})
	return nil
}

func (w *modelSwitchWorld) tapLongModelButton() error { return w.tapButton(longModelID) }

func (w *modelSwitchWorld) offeredTheKeyboard() error { return w.sendCommand("/model") }

func (w *modelSwitchWorld) restartWithSessionOnDisk() error {
	w.runner.restart()
	return nil
}

func (w *modelSwitchWorld) sessionModelIs(model string) error {
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	for _, st := range w.runner.onDisk {
		if got := st.GetSelectedModelID(); got == model {
			return nil
		} else if got != "" {
			return fmt.Errorf("session model is %q, want %q", got, model)
		}
	}
	return fmt.Errorf("no session carries model %q; log:\n%s", model, w.logText())
}

func (w *modelSwitchWorld) sessionModelIsTheLongOne() error { return w.sessionModelIs(longModelID) }

// logRecords returns the parsed JSON log lines emitted so far.
func (w *modelSwitchWorld) logRecords() []map[string]any {
	out := []map[string]any{}
	for _, line := range strings.Split(w.logText(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			out = append(out, rec)
		}
	}
	return out
}

// findRecord returns the first record whose msg matches and whose named
// attributes all carry the wanted values.
func (w *modelSwitchWorld) findRecord(msg string, attrs map[string]string) error {
	for _, rec := range w.logRecords() {
		if fmt.Sprint(rec["msg"]) != msg {
			continue
		}
		ok := true
		for k, want := range attrs {
			if got, present := rec[k]; !present || fmt.Sprint(got) != want {
				ok = false
				break
			}
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("no %q record with %v; log:\n%s", msg, attrs, w.logText())
}

func (w *modelSwitchWorld) logRecordsCommand(command string) error {
	return w.findRecord("telegram: command", map[string]string{"command": command})
}

// The menu is where the session id first appears: the command line runs before
// anything has created one, and reporting it must not mint one either.
func (w *modelSwitchWorld) logRecordsMenuSession() error {
	for _, rec := range w.logRecords() {
		if fmt.Sprint(rec["msg"]) != "telegram: model menu" {
			continue
		}
		if id := fmt.Sprint(rec["session"]); strings.HasPrefix(id, "gw_") {
			return nil
		}
	}
	return fmt.Errorf("no model menu record carrying a session id; log:\n%s", w.logText())
}

func (w *modelSwitchWorld) logRecordsCallback(model string) error {
	return w.findRecord("telegram: callback", map[string]string{"action": "model", "value": model})
}

func (w *modelSwitchWorld) logRecordsModelApplied() error {
	return w.findRecord("telegram: model applied", nil)
}

func initializeModelSwitchScenario(sc *godog.ScenarioContext) {
	w := &modelSwitchWorld{}

	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.srv != nil {
			w.srv.Close()
		}
		if w.logClose != nil {
			_ = w.logClose.Close()
		}
		if w.logDir != "" {
			_ = os.RemoveAll(w.logDir)
		}
		return ctx, err
	})

	sc.Given(`^a telegram gateway with the models "([^"]*)" and "([^"]*)"$`, w.gatewayWithModels)
	sc.Given(`^the component "([^"]*)" is configured at "([^"]*)"$`, w.componentConfiguredAt)
	sc.Given(`^the user has been offered the model keyboard$`, w.offeredTheKeyboard)
	sc.Given(`^the gateway is restarted with the session left on disk$`, w.restartWithSessionOnDisk)
	sc.Given(`^a model whose id is longer than the telegram callback limit$`, w.longModelIsConfigured)

	sc.When(`^the user sends "([^"]*)"$`, w.sendCommand)
	sc.When(`^the user taps the button for "([^"]*)"$`, w.tapButton)
	sc.When(`^the user taps the button for that long model$`, w.tapLongModelButton)

	sc.Then(`^the session model is "([^"]*)"$`, w.sessionModelIs)
	sc.Then(`^the session model is that long model$`, w.sessionModelIsTheLongOne)
	sc.Then(`^the log records the command "([^"]*)"$`, w.logRecordsCommand)
	sc.Then(`^the log records the model menu with the session id$`, w.logRecordsMenuSession)
	sc.Then(`^the log records the callback with the resolved model "([^"]*)"$`, w.logRecordsCallback)
	sc.Then(`^the log records that the model was applied$`, w.logRecordsModelApplied)
}

func TestModelSwitchFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway-telegram-model-switch",
		ScenarioInitializer: initializeModelSwitchScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_telegram_model_switch.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("telegram model switch feature suite failed")
	}
}
