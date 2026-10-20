//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_acp_provider.feature: a chat
// turn of the bot on an acp row, through the real manager, agent loop and
// provider, with the scripted ACP agent of internal/llm/acptest (this test
// binary started in its place) and the fake Bot API.

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tgfake "github.com/EvilFreelancer/tgfake/pkg/server"
	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/acptest"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// TestHelperACPAgent turns the test binary into the scripted ACP agent when
// acptest.Command started it; the ordinary run skips it.
func TestHelperACPAgent(t *testing.T) {
	if !acptest.Main(flag.Args()) {
		t.Skip("helper process")
	}
}

const acpChatID = int64(6060)

type acpChatWorld struct {
	t    *testing.T
	root string
	fake *fakeAPI
	bot  *Bot
	// turn closes when the turn adminWrites started is over.
	turn chan struct{}
}

func (w *acpChatWorld) reset() error {
	w.close()
	root, err := os.MkdirTemp("", "coddy-tg-acp-*")
	if err != nil {
		return err
	}
	w.root = root
	return nil
}

func (w *acpChatWorld) close() {
	if w.turn != nil {
		if w.bot != nil {
			w.bot.asks.stop()
		}
		select {
		case <-w.turn:
		case <-time.After(30 * time.Second):
		}
		w.turn = nil
	}
	if w.bot != nil {
		w.bot.asks.stop()
		w.bot = nil
	}
	if w.fake != nil {
		w.fake.close()
		w.fake = nil
	}
	llm.StopACPAgents()
	if w.root != "" {
		_ = os.RemoveAll(w.root)
		w.root = ""
	}
}

// chatOn builds the bot of an admin's chat whose only model is an acp row
// running turn.
func (w *acpChatWorld) chatOn(turn acptest.Turn) error {
	w.t.Setenv(llm.ACPDepthEnv, "")
	cmd, args := acptest.Command(w.t, acptest.Script{Log: filepath.Join(w.root, "agent.log"), Close: true, Turns: []acptest.Turn{turn}})
	home := filepath.Join(w.root, "home")
	cwd := filepath.Join(w.root, "work")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "agent", Type: "acp", ACP: &config.ACPProviderConfig{Command: cmd, Args: args}}},
		Models:    []config.ModelEntry{{Model: "agent/default", MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "agent/default"},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	log := slog.New(slog.DiscardHandler)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	mgr := session.NewManager(cfg, noopUpdateSender{}, runner, log, cwd, &session.FileStore{Root: filepath.Join(w.root, "sessions")})
	w.fake = openFakeAPI(tgfake.Options{})
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
		Admins: []int64{acpChatID},
	}, mgr, cwd, log, "", nil)
	w.bot.setAPI(w.fake.api)
	return nil
}

func (w *acpChatWorld) chatOnEchoingAgent() error {
	return w.chatOn(acptest.Turn{Tools: []acptest.Tool{{ID: "t1", Title: "Read README.md"}}, Echo: true})
}

func (w *acpChatWorld) chatOnAskingAgent(title string) error {
	return w.chatOn(acptest.Turn{Permission: &acptest.Permission{Title: title, Kind: "edit", Options: []acptest.Option{
		{ID: "ok", Name: "Allow", Kind: "allow_once"}, {ID: "no", Name: "Reject", Kind: "reject_once"},
	}}, Echo: true})
}

// adminWrites hands the bot the admin's message; the turn runs on, since a
// permission request in it waits for a tap.
func (w *acpChatWorld) adminWrites(text string) error {
	msg := w.fake.userMessage(acpChatID, acpChatID, text)
	key := sessionstore.SessionKey(adapterName, acpChatID, acpChatID, config.IsolationIndividual, false)
	w.turn = make(chan struct{})
	go func() {
		defer close(w.turn)
		w.bot.processMessage(context.Background(), w.fake.api, msg, key)
	}()
	return nil
}

// permissionRequest waits for the bot's message that carries buttons.
func (w *acpChatWorld) permissionRequest() (tgfake.MessageView, error) {
	deadline := time.Now().Add(20 * time.Second)
	for {
		for _, m := range w.fake.fake.Chat(acpChatID).Messages {
			if m.From == "bot" && !m.Deleted && len(m.Keyboard) > 0 {
				return m, nil
			}
		}
		if time.Now().After(deadline) {
			return tgfake.MessageView{}, fmt.Errorf("the chat never showed a permission request:\n%s", w.fake.fake.Chat(acpChatID).Text())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (w *acpChatWorld) chatShowsRequest(title, first, second string) error {
	m, err := w.permissionRequest()
	if err != nil {
		return err
	}
	if !strings.Contains(m.Text, title) {
		return fmt.Errorf("the request reads %q, want it to name %q", m.Text, title)
	}
	var labels []string
	for _, row := range m.Keyboard {
		for _, btn := range row {
			labels = append(labels, btn.Text)
		}
	}
	if strings.Join(labels, ",") != first+","+second {
		return fmt.Errorf("buttons %v, want %q and %q", labels, first, second)
	}
	return nil
}

func (w *acpChatWorld) adminTaps(label string) error {
	cbq, err := w.fake.tap(acpChatID, acpChatID, label)
	if err != nil {
		return err
	}
	w.bot.handleCallback(context.Background(), w.fake.api, cbq)
	return nil
}

func (w *acpChatWorld) chatShows(text string) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.fake.fake.Chat(acpChatID).Text(), text) {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("the chat never showed %q:\n%s", text, w.fake.fake.Chat(acpChatID).Text())
}

func TestGatewayTelegramACPProviderFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "gateway-telegram-acp-provider",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &acpChatWorld{t: t}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				return ctx, w.reset()
			})
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				w.close()
				return ctx, nil
			})
			sc.Step(`^a telegram chat whose model is an acp row with an agent that echoes$`, w.chatOnEchoingAgent)
			sc.Step(`^a telegram chat whose model is an acp row with an agent that asks to "([^"]*)"$`, w.chatOnAskingAgent)
			sc.Step(`^the admin writes "([^"]*)"$`, w.adminWrites)
			sc.Step(`^the chat shows "([^"]*)"$`, w.chatShows)
			sc.Step(`^the chat shows a permission request for "([^"]*)" with the buttons "([^"]*)" and "([^"]*)"$`, w.chatShowsRequest)
			sc.Step(`^the admin taps "([^"]*)"$`, w.adminTaps)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../../features/gateway_telegram_acp_provider.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("telegram acp provider feature suite failed")
	}
}
