//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_wake.feature: a chat turn starts
// a real background task through the real agent, the real task pool and the
// real waker, and the woken turn has to come back to the chat. The model is the
// scripted llmstub, Telegram is the fake Bot API; nothing leaves the machine.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake/llmstub"
)

const (
	wakeChatID = int64(7070)
	wakeUserID = int64(7070)
)

type wakeWorld struct {
	root  string
	fake  *fakeAPI
	model *httptest.Server
	mgr   *session.Manager
	bot   *Bot
	// failCommand is what the scripted model starts in the background; the
	// scenario fixes its exit code.
	failCommand string
}

func (w *wakeWorld) reset() error {
	w.close()
	root, err := os.MkdirTemp("", "coddy-tg-wake-*")
	if err != nil {
		return err
	}
	w.root = root
	return nil
}

func (w *wakeWorld) close() {
	// A draining pool starts no wake, so a task of this scenario cannot reach
	// the next one's bot.
	if w.mgr != nil {
		bgtask.Default().SetDraining(true)
		for _, id := range w.sessionIDs() {
			bgtask.Default().StopSession(id)
		}
		bgtask.Default().SetDraining(false)
		bgtask.Default().SubscribeKeyed(bgtask.WakeWatcherKey, nil)
	}
	if w.bot != nil {
		w.bot.asks.stop()
		w.bot = nil
	}
	if w.fake != nil {
		w.fake.close()
		w.fake = nil
	}
	if w.model != nil {
		w.model.Close()
		w.model = nil
	}
	w.mgr = nil
	if w.root != "" {
		_ = os.RemoveAll(w.root)
		w.root = ""
	}
}

func (w *wakeWorld) sessionIDs() []string {
	if w.bot == nil {
		return nil
	}
	return w.bot.store.KnownIDs()
}

func (w *wakeWorld) chatKey() string {
	return sessionstore.SessionKey(adapterName, wakeChatID, wakeUserID, config.IsolationIndividual, false)
}

// chatWithScriptedModel builds the whole path a chat turn takes in `coddy
// serve` with the gateway alone: the manager, the agent loop, the task pool,
// and a waker whose only surface is this bot.
func (w *wakeWorld) chatWithScriptedModel() error {
	w.failCommand = "echo 'tests failed' >&2; exit 2"
	start, _ := json.Marshal(map[string]any{
		"command":          w.failCommand,
		"background":       true,
		"notify_on_finish": true,
		"expected_seconds": 1,
	})
	stub := &llmstub.Server{Rules: []llmstub.Rule{
		{Match: "start the tests", Tool: &llmstub.ToolCall{Name: "run_command", Arguments: start}, Answer: "Started the tests in the background."},
		{Match: "background task you asked to be notified about", Answer: "The tests failed with exit 2."},
	}}
	w.model = httptest.NewServer(stub.Handler())

	home := filepath.Join(w.root, "home")
	cwd := filepath.Join(w.root, "work")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "stub", Type: "openai", APIBase: w.model.URL + "/v1", APIKey: "sk-stub"}},
		Models:    []config.ModelEntry{{Model: "stub/coddy-demo", MaxContextTokens: 131072}},
		Agent:     config.Agent{Model: "stub/coddy-demo"},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	log := slog.New(slog.DiscardHandler)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	store := &session.FileStore{Root: filepath.Join(w.root, "sessions")}
	w.mgr = session.NewManager(cfg, noopUpdateSender{}, runner, log, cwd, store)

	w.fake = openFakeAPI(tgfake.Options{})
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	}, w.mgr, cwd, log, "", nil)
	w.bot.setAPI(w.fake.api)
	agent.NewBackgroundWaker(log, func(ctx context.Context, wake agent.Wake) error {
		if handled, err := w.bot.RunBackgroundWake(ctx, wake); handled {
			return err
		}
		return fmt.Errorf("the bot declined the wake of session %s", wake.SessionID)
	}).Attach(bgtask.Default())
	return nil
}

func (w *wakeWorld) userAsks(text string) error {
	msg := w.fake.userMessage(wakeChatID, wakeUserID, text)
	w.bot.processMessage(context.Background(), w.fake.api, msg, w.chatKey())
	return nil
}

func (w *wakeWorld) chatShows(text string) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.fake.fake.Chat(wakeChatID).Text(), text) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("the chat never showed %q:\n%s", text, w.fake.fake.Chat(wakeChatID).Text())
}

// taskEnds waits for the task the model started to reach its terminal status
// with the exit code the scenario names.
func (w *wakeWorld) taskEnds(code int) error {
	sessionID := w.bot.store.Peek(w.chatKey())
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, task := range bgtask.Default().List(sessionID) {
			if task.Status.Finished() {
				if task.ExitCode == nil || *task.ExitCode != code {
					return fmt.Errorf("task %s ended %s with exit %v, want %d", task.ID, task.Status, task.ExitCode, code)
				}
				if !task.NotifyOnFinish {
					return fmt.Errorf("task %s was not recorded as waking the agent", task.ID)
				}
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("the background task of session %s never ended", sessionID)
}

func (w *wakeWorld) sessionKeepsWake() error {
	sessionID := w.bot.store.Peek(w.chatKey())
	st := w.mgr.SessionByID(sessionID)
	if st == nil {
		return fmt.Errorf("session %s is not live", sessionID)
	}
	for _, m := range st.GetMessages() {
		if m.BackgroundWake != nil {
			if len(m.BackgroundWake.Tasks) != 1 || m.BackgroundWake.Tasks[0].ID != "bg_1" || m.BackgroundWake.Tasks[0].Status != "failed" {
				return fmt.Errorf("wake marker = %+v", m.BackgroundWake)
			}
			return nil
		}
	}
	return fmt.Errorf("no message of session %s is marked as a background wake", sessionID)
}

// noopUpdateSender is the manager's default surface in this harness: every
// turn here is run with the chat's own sender.
type noopUpdateSender struct{}

func (noopUpdateSender) SendSessionUpdate(string, interface{}) error { return nil }
func (noopUpdateSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}
func (noopUpdateSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func initializeWakeScenario(sc *godog.ScenarioContext) {
	w := &wakeWorld{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, w.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.close()
		return ctx, nil
	})
	sc.Step(`^a telegram chat whose agent is a scripted model$`, w.chatWithScriptedModel)
	sc.Step(`^the user asks the agent to "([^"]*)" in the background$`, w.userAsks)
	sc.Step(`^the chat shows "([^"]*)"$`, w.chatShows)
	sc.Step(`^the background task ends with exit code (\d+)$`, w.taskEnds)
	sc.Step(`^the session keeps the woken turn's first message as a background wake$`, w.sessionKeepsWake)
}

func TestGatewayTelegramWakeFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway-telegram-wake",
		ScenarioInitializer: initializeWakeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_telegram_wake.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("telegram wake feature suite failed")
	}
}
