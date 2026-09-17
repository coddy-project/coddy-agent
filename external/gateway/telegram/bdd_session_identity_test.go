//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_session_identity.feature: drives a chat
// message through the real handler against the fake Bot API (internal/tgfake)
// and a scripted agent, and asserts on the session id, on the prompt the agent
// was handed and on the text that reached the chat. No LLM and no network
// beyond the local httptest server.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
	identityChatID = int64(9001)
	identityUserID = int64(4242)
)

// ordinarySessionIDPattern is the shape session.NewSessionID mints. A chat
// conversation carries it like any other session.
var ordinarySessionIDPattern = regexp.MustCompile(`^sess_[0-9a-f]{24}$`)

// scriptedRunner answers every prompt with the same text, pushed through the
// sender the gateway supplies, and records the prompt it was given.
type scriptedRunner struct {
	mu      sync.Mutex
	cfg     *config.Config
	live    map[string]*session.State
	answer  string
	prompts []string
	// surfaces records the system prompt block each turn was given, so the
	// spec can assert the gateway spoke for itself without a real model.
	surfaces []string
}

func newScriptedRunner() *scriptedRunner {
	return &scriptedRunner{
		cfg:    &config.Config{Models: []config.ModelEntry{{Model: "stub/model"}}, Agent: config.Agent{Model: "stub/model"}},
		live:   map[string]*session.State{},
		answer: "plain answer",
	}
}

func (r *scriptedRunner) EnsureHTTPSession(_ context.Context, sessionID, cwd string) (*session.State, error) {
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

func (r *scriptedRunner) HandleSessionPromptWithSender(_ context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error) {
	r.mu.Lock()
	var text strings.Builder
	for _, block := range params.Prompt {
		text.WriteString(block.Text)
	}
	r.prompts = append(r.prompts, text.String())
	surface := ""
	if opts != nil {
		surface = opts.SurfaceSystemPrompt
	}
	r.surfaces = append(r.surfaces, surface)
	answer := r.answer
	r.mu.Unlock()

	if sender != nil {
		_ = sender.SendSessionUpdate(params.SessionID, acp.MessageChunkUpdate{
			SessionUpdate: acp.UpdateTypeAgentMessageChunk,
			Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: answer},
		})
	}
	return &acp.SessionPromptResult{StopReason: acp.StopReasonEndTurn}, nil
}

func (r *scriptedRunner) ForgetLiveSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.live, id)
}

func (r *scriptedRunner) HandleSessionSetMode(context.Context, acp.SessionSetModeParams) error {
	return nil
}

func (r *scriptedRunner) HandleSessionSetConfigOption(context.Context, acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	return &acp.SessionSetConfigOptionResult{}, nil
}

func (r *scriptedRunner) Cfg() *config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

// identityWorld holds the bot and the fake Bot API it posts to.
type identityWorld struct {
	runner *scriptedRunner
	bot    *Bot
	f      *fakeAPI
}

func (w *identityWorld) gatewayOverScriptedAgent() error {
	w.runner = newScriptedRunner()
	w.f = openFakeAPI(tgfake.Options{})
	base, _, err := logger.New(config.Logger{Level: config.LogLevelError, Format: config.LogFormatText, Outputs: []string{config.LogOutputStderr}})
	if err != nil {
		return err
	}
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	}, w.runner, w.f.srv.URL, logger.Component(base, logger.ComponentGatewayTelegram), "", nil)
	return nil
}

func (w *identityWorld) close() {
	if w.f != nil {
		w.f.close()
		w.f = nil
	}
}

func (w *identityWorld) agentAnswersWith(answer string) error {
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	w.runner.answer = strings.ReplaceAll(answer, `\n`, "\n")
	return nil
}

func (w *identityWorld) agentAnswersWithCodeBlock(body string) error {
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	w.runner.answer = "Here:\n\n```go\n" + body + "\n```\n"
	return nil
}

func (w *identityWorld) userSends(text string) error {
	msg := w.f.userMessage(identityChatID, identityUserID, text)
	w.bot.processMessage(context.Background(), w.f.api, msg, w.sessionKey())
	return nil
}

func (w *identityWorld) sessionKey() string {
	return fmt.Sprintf("tg:user:%d", identityUserID)
}

func (w *identityWorld) sessionIDIsOrdinary() error {
	id := w.bot.store.Peek(w.sessionKey())
	if !ordinarySessionIDPattern.MatchString(id) {
		return fmt.Errorf("chat session id %q is not shaped like an ordinary session id", id)
	}
	return nil
}

func (w *identityWorld) agentPromptedWith(want string) error {
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	if len(w.runner.prompts) == 0 {
		return fmt.Errorf("the agent was never prompted")
	}
	got := w.runner.prompts[len(w.runner.prompts)-1]
	if got != want {
		return fmt.Errorf("prompt = %q, want %q", got, want)
	}
	return nil
}

// lastSurface is the system prompt block the most recent turn carried.
func (w *identityWorld) lastSurface() string {
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	if len(w.runner.surfaces) == 0 {
		return ""
	}
	return w.runner.surfaces[len(w.runner.surfaces)-1]
}

func (w *identityWorld) surfaceNamesTelegram() error {
	block := w.lastSurface()
	if block == "" {
		return fmt.Errorf("the turn carried no system prompt block")
	}
	if !strings.Contains(block, "Telegram") {
		return fmt.Errorf("the block does not name Telegram: %q", block)
	}
	return nil
}

func (w *identityWorld) surfaceDescribesTheFormat() error {
	block := w.lastSurface()
	// The subset a Telegram chat renders is what the block has to be about:
	// the emphasis it understands, the headings it does not.
	for _, want := range []string{"*bold*", "`inline code`", "no `#` headings", "no tables"} {
		if !strings.Contains(block, want) {
			return fmt.Errorf("the block does not mention %q: %q", want, block)
		}
	}
	return nil
}

func (w *identityWorld) surfaceStayedOutOfThePrompt() error {
	block := w.lastSurface()
	w.runner.mu.Lock()
	defer w.runner.mu.Unlock()
	for _, p := range w.runner.prompts {
		if strings.Contains(p, "Telegram") || (block != "" && strings.Contains(p, block)) {
			return fmt.Errorf("the message the agent was prompted with carries the block: %q", p)
		}
	}
	return nil
}

// sentTexts returns every text the bot posted to the chat, the live previews
// and the edits included: what must never reach a chat must not pass through
// it either.
func (w *identityWorld) sentTexts() []string {
	var out []string
	for _, call := range w.f.fake.Calls("") {
		if t := call.Params["text"]; t != "" {
			out = append(out, t)
		}
		var rich struct {
			Markdown string `json:"markdown"`
		}
		if json.Unmarshal([]byte(call.Params["rich_message"]), &rich) == nil && rich.Markdown != "" {
			out = append(out, rich.Markdown)
		}
	}
	return out
}

func (w *identityWorld) chatReceived(want string) error {
	want = strings.ReplaceAll(want, `\n`, "\n")
	for _, t := range w.sentTexts() {
		if strings.Contains(t, want) {
			return nil
		}
	}
	return fmt.Errorf("no message sent to the chat contains %q; sent %q", want, w.sentTexts())
}

func (w *identityWorld) chatReceivedNothingContaining(unwanted string) error {
	for _, t := range w.sentTexts() {
		if strings.Contains(t, unwanted) {
			return fmt.Errorf("a message sent to the chat contains %q: %q", unwanted, t)
		}
	}
	return nil
}

func initializeSessionIdentityScenario(sc *godog.ScenarioContext) {
	w := &identityWorld{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.close()
		return ctx, nil
	})

	sc.Step(`^a telegram gateway over a scripted agent$`, w.gatewayOverScriptedAgent)
	sc.Step(`^the agent answers with "([^"]*)"$`, w.agentAnswersWith)
	sc.Step(`^the agent answers with a fenced code block holding "([^"]*)"$`, w.agentAnswersWithCodeBlock)
	sc.Step(`^the user sends "([^"]*)"$`, w.userSends)
	sc.Step(`^the session behind the chat has an ordinary session id$`, w.sessionIDIsOrdinary)
	sc.Step(`^the agent was prompted with exactly "([^"]*)"$`, w.agentPromptedWith)
	sc.Step(`^the turn carried a system prompt block naming Telegram$`, w.surfaceNamesTelegram)
	sc.Step(`^that block describes the answer format$`, w.surfaceDescribesTheFormat)
	sc.Step(`^nothing of it reached the message the agent was prompted with$`, w.surfaceStayedOutOfThePrompt)
	sc.Step(`^the chat received "([^"]*)"$`, w.chatReceived)
	sc.Step(`^the chat received no text containing "([^"]*)"$`, w.chatReceivedNothingContaining)
}

func TestGatewaySessionIdentityFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway session identity",
		ScenarioInitializer: initializeSessionIdentityScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_session_identity.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("gateway session identity feature suite failed")
	}
}
