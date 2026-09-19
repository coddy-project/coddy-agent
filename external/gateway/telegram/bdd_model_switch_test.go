//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_model_switch.feature: drives
// /model and the inline-keyboard tap through the real handlers against the
// fake Bot API (internal/tgfake), and asserts on the session model plus the
// debug trail. The keyboard a tap presses is the one the bot really sent, on
// the message it really sent it with. No LLM and no network beyond the local
// httptest server.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
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
	// prompts are the texts handed to the session, in order.
	prompts []string
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

func (r *stubRunner) HandleSessionPromptWithSender(_ context.Context, params acp.SessionPromptParams, _ acp.UpdateSender, _ *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	r.mu.Lock()
	for _, b := range params.Prompt {
		if b.Type == acp.ContentTypeText {
			r.prompts = append(r.prompts, b.Text)
		}
	}
	r.mu.Unlock()
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

// HandleSessionList answers with the sessions on disk; the order is not
// something this spec asserts on.
func (r *stubRunner) HandleSessionList(context.Context, acp.SessionListParams) (*acp.SessionListResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]acp.SessionListInfo, 0, len(r.onDisk))
	for id, st := range r.onDisk {
		out = append(out, acp.SessionListInfo{SessionID: id, CWD: st.CWD})
	}
	return &acp.SessionListResult{Sessions: out}, nil
}

func (r *stubRunner) Cfg() *config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

// modelSwitchWorld holds the bot, the fake Bot API and everything the steps assert on.
type modelSwitchWorld struct {
	runner   *stubRunner
	bot      *Bot
	f        *fakeAPI
	logDir   string
	logPath  string
	logClose io.Closer
	logCfg   config.Logger
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

func (w *modelSwitchWorld) gatewayWithModels(a, b string) error {
	w.logCfg = config.Logger{Level: config.LogLevelInfo, Format: config.LogFormatJSON}
	w.runner = newStubRunner(&config.Config{
		Models: []config.ModelEntry{{Model: a}, {Model: b}},
		Agent:  config.Agent{Model: a},
	})
	w.f = openFakeAPI(tgfake.Options{})
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
	msg := w.f.userMessage(modelSwitchChatID, modelSwitchUserID, text)
	w.bot.processMessage(context.Background(), w.f.api, msg, w.sessionKey())
	return nil
}

func (w *modelSwitchWorld) sessionKey() string {
	return fmt.Sprintf("tg:user:%d", modelSwitchUserID)
}

// tapButton presses the button labelled for model on the keyboard the bot
// sent. The fake refuses a keyboard whose callback_data is over Telegram's
// limit, so a button that exists already fits; the length is asserted anyway,
// because it is what the feature states.
func (w *modelSwitchWorld) tapButton(model string) error {
	cbq, err := w.f.tap(modelSwitchChatID, modelSwitchUserID, model)
	if err != nil {
		return err
	}
	if len(cbq.Data) > telegramCallbackDataMax {
		return fmt.Errorf("callback_data for %q is %d bytes, over the telegram limit of %d",
			model, len(cbq.Data), telegramCallbackDataMax)
	}
	w.bot.handleCallback(context.Background(), w.f.api, cbq)
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

// sessionReceived asserts the text reached the session as a message: the
// manager takes a settings command off its start there.
func (w *modelSwitchWorld) sessionReceived(text string) error {
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	for _, p := range w.runner.prompts {
		if p == text {
			return nil
		}
	}
	return fmt.Errorf("the session received %q, not %q", w.runner.prompts, text)
}

// noKeyboardSent asserts the bot answered without an inline keyboard.
func (w *modelSwitchWorld) noKeyboardSent() error {
	if _, err := w.f.tap(modelSwitchChatID, modelSwitchUserID, "rpa/qwen3.6-35b-a3b"); err == nil {
		return fmt.Errorf("a model keyboard was sent")
	}
	return nil
}

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
		if id := fmt.Sprint(rec["session"]); ordinarySessionIDPattern.MatchString(id) {
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
		if w.f != nil {
			w.f.close()
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
	sc.Then(`^the session received "([^"]*)"$`, w.sessionReceived)
	sc.Then(`^no model keyboard was sent$`, w.noKeyboardSent)
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
