//go:build browser

// An external test package: the scripted chat runs the real agent, whose tool
// registry imports this module's tool, which imports this package.
package browser_test

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

	"github.com/EvilFreelancer/tgfake/pkg/llmstub"
	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/browser"
	"github.com/EvilFreelancer/coddy-agent/external/browser/browsertest"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const chatKey = "0123456789abcdef0123456789abcdef"

type noopSender struct{}

func (noopSender) SendSessionUpdate(string, interface{}) error { return nil }
func (noopSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled"}, nil
}
func (noopSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// panelWorld is one scenario: a real manager and agent on a scripted model,
// the real listener with its chat, and the fake extension playing the panel.
type panelWorld struct {
	t         *testing.T
	model     *httptest.Server
	listener  *httptest.Server
	ext       *browsertest.Extension
	sessionID string
	tab       browsertest.Tab
	asked     chan string
	sent      chan sendOutcome
}

type sendOutcome struct {
	raw json.RawMessage
	err error
}

// scriptedCoddy is the server side of a panel chat with a scripted model: the
// manager and the agent, and the real listener with its chat on the process
// hub the browser tool calls through.
func scriptedCoddy(t *testing.T, rules []llmstub.Rule) (listener, model *httptest.Server) {
	t.Helper()
	stub := &llmstub.Server{Model: "coddy-demo", StripTags: []string{"turn_context"}, Rules: rules}
	model = httptest.NewServer(stub.Handler())
	root := t.TempDir()
	home, cwd := filepath.Join(root, "home"), filepath.Join(root, "work")
	for _, dir := range []string{home, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: cwd},
		Providers: []config.ProviderConfig{{Name: "stub", Type: "openai", APIBase: model.URL + "/v1", APIKey: "sk-stub"}},
		Models:    []config.ModelEntry{{Model: "stub/coddy-demo", MaxContextTokens: 131072}},
		Agent:     config.Agent{Model: "stub/coddy-demo"},
		Browser:   config.BrowserConfig{Enable: true, Token: chatKey},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	log := slog.New(slog.DiscardHandler)
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, cwd, &session.FileStore{Root: filepath.Join(root, "sessions")})
	srv := browser.NewServer(browser.Default(), chatKey, log)
	srv.Chat = &browser.Chat{Mgr: mgr, CWD: cwd, Log: log}
	return httptest.NewServer(srv), model
}

func (w *panelWorld) connected() error {
	snapshot, _ := json.Marshal(map[string]any{"action": "snapshot", "tab_id": 41})
	w.listener, w.model = scriptedCoddy(w.t, []llmstub.Rule{
		{Match: "read this page", Tool: &llmstub.ToolCall{Name: "browser", Arguments: snapshot}, Answer: "The page is Example Domain."},
	})
	ext, err := browsertest.Connect(w.listener.URL, browsertest.Options{Key: chatKey, ID: "browser-chat-0001", Name: "Chrome - panel"})
	if err != nil {
		return err
	}
	w.ext = ext
	w.asked = make(chan string, 4)
	ext.Ask = func(method string, params json.RawMessage) browsertest.Answer {
		if method != "chat/permission" {
			return browsertest.Answer{Error: "unexpected " + method}
		}
		var p struct {
			Title   string `json:"title"`
			Options []struct {
				OptionID string `json:"optionId"`
				Kind     string `json:"kind"`
			} `json:"options"`
		}
		_ = json.Unmarshal(params, &p)
		w.asked <- p.Title
		for _, o := range p.Options {
			if o.Kind == "allow_once" {
				return browsertest.Answer{Result: map[string]string{"optionId": o.OptionID}}
			}
		}
		return browsertest.Answer{Error: "no option to allow"}
	}
	ext.Handle = func(c browsertest.Command) browsertest.Answer {
		return browsertest.Answer{Result: map[string]any{"title": "Example Domain", "text": "This domain is for use in examples."}}
	}
	return ext.Share()
}

func (w *panelWorld) looksAt(url, title string) error {
	raw, err := w.ext.Request("chat/new", map[string]any{}, 10*time.Second)
	if err != nil {
		return err
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || !strings.HasPrefix(created.SessionID, "sess_") {
		return fmt.Errorf("chat/new answered %s", raw)
	}
	w.sessionID = created.SessionID
	// The panel puts the tab it writes from into the chat's group first.
	w.tab = browsertest.Tab{ID: 41, URL: url, Title: title, SessionID: w.sessionID}
	if err := w.ext.Publish(w.tab); err != nil {
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, b := range browser.Default().List(w.sessionID) {
			if len(b.Tabs) == 1 && b.Tabs[0].Yours {
				return nil
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("the tab never reached the hub")
}

func (w *panelWorld) asks(text string) error {
	w.sent = make(chan sendOutcome, 1)
	go func() {
		raw, err := w.ext.Request("chat/send", map[string]any{
			"sessionId": w.sessionID, "text": text,
			"tab": map[string]any{"id": w.tab.ID, "url": w.tab.URL, "title": w.tab.Title},
		}, 30*time.Second)
		w.sent <- sendOutcome{raw, err}
	}()
	return nil
}

func (w *panelWorld) askedToAllow() error {
	select {
	case title := <-w.asked:
		if !strings.Contains(strings.ToLower(title), "browser") {
			return fmt.Errorf("the panel was asked about %q", title)
		}
		return nil
	case <-time.After(20 * time.Second):
		return fmt.Errorf("the panel was never asked")
	}
}

func (w *panelWorld) commandFromChat(action string) error {
	cmd, err := w.ext.Next(20 * time.Second)
	if err != nil {
		return err
	}
	if cmd.Action != action || cmd.TabID != w.tab.ID || cmd.Session.ID != w.sessionID {
		return fmt.Errorf("the extension received %+v", cmd)
	}
	return nil
}

func (w *panelWorld) showsAnswer(answer string) error {
	var o sendOutcome
	select {
	case o = <-w.sent:
	case <-time.After(30 * time.Second):
		return fmt.Errorf("chat/send never answered")
	}
	if o.err != nil {
		return o.err
	}
	if !strings.Contains(string(o.raw), `"stopReason":"end_turn"`) {
		return fmt.Errorf("chat/send answered %s", o.raw)
	}
	var streamed, tools strings.Builder
	for {
		select {
		case note := <-w.ext.Notes:
			method, params, _ := strings.Cut(note, " ")
			if method != "chat/update" {
				continue
			}
			var u struct {
				SessionID string `json:"sessionId"`
				Kind      string `json:"kind"`
				Text      string `json:"text"`
				Title     string `json:"title"`
			}
			_ = json.Unmarshal([]byte(params), &u)
			if u.SessionID != w.sessionID {
				return fmt.Errorf("an update of %q reached the panel", u.SessionID)
			}
			if u.Kind == "text" {
				streamed.WriteString(u.Text)
			}
			if u.Kind == "tool" {
				tools.WriteString(u.Title)
			}
			continue
		default:
		}
		break
	}
	if !strings.Contains(streamed.String(), answer) {
		return fmt.Errorf("the panel streamed %q", streamed.String())
	}
	if tools.Len() == 0 {
		return fmt.Errorf("the panel saw no tool call")
	}
	return nil
}

func (w *panelWorld) listed() error {
	raw, err := w.ext.Request("chat/list", map[string]any{}, 10*time.Second)
	if err != nil {
		return err
	}
	if !strings.Contains(string(raw), w.sessionID) {
		return fmt.Errorf("chat/list: %s", raw)
	}
	raw, err = w.ext.Request("chat/messages", map[string]any{"sessionId": w.sessionID}, 10*time.Second)
	if err != nil {
		return err
	}
	var got struct {
		Messages []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return err
	}
	if len(got.Messages) != 2 || got.Messages[0].Role != "user" || got.Messages[0].Text != "read this page" ||
		got.Messages[1].Role != "assistant" || got.Messages[1].Text != "The page is Example Domain." {
		return fmt.Errorf("chat/messages: %s", raw)
	}
	return nil
}

func (w *panelWorld) close() {
	if w.ext != nil {
		_ = w.ext.Close()
	}
	if w.listener != nil {
		w.listener.Close()
	}
	if w.model != nil {
		w.model.Close()
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && browser.Default().Connected("browser-chat-0001") {
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBrowserChatFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "browser-chat",
		Options: &godog.Options{
			Format: "progress", Paths: []string{"../../features/browser_chat.feature"}, TestingT: t, Strict: true,
		},
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &panelWorld{t: t}
			sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
				w.close()
				return ctx, err
			})
			sc.Step(`^Coddy for Browser connected to a Coddy with a scripted model$`, w.connected)
			sc.Step(`^the user looks at the tab "([^"]*)" titled "([^"]*)" in the chat's tab group$`, w.looksAt)
			sc.Step(`^the user asks in the side panel "([^"]*)"$`, w.asks)
			sc.Step(`^the side panel is asked to allow the browser tool and allows it$`, w.askedToAllow)
			sc.Step(`^the extension receives a "([^"]*)" command for that tab from the panel's chat$`, w.commandFromChat)
			sc.Step(`^the side panel shows the answer "([^"]*)"$`, w.showsAnswer)
			sc.Step(`^the panel lists the chat with the question and the answer$`, w.listed)
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the browser chat feature failed")
	}
}
