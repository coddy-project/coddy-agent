package agent

// Godog harness for features/decisions_check.feature: drives the real
// Agent.Run with a scripted model and a local stand of the decisions
// endpoint, so the spec covers the whole path - the permission gate deciding
// a call needs no prompt, the decisions check, the refusal as the tool
// result, and the model re-prompted on it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	bddDecisionsMarker = "coddy-decisions-ran"
	bddDecisionsCallID = "call_decisions_bdd"
	bddDecisionsAnswer = "The command was handled."
)

// bddDecisionsProvider requests one run_command call on its first turn and
// answers on the next.
type bddDecisionsProvider struct {
	seen [][]llm.Message
}

func (p *bddDecisionsProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("Complete must not be used by the decisions suite")
}

func (p *bddDecisionsProvider) Stream(_ context.Context, messages []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	p.seen = append(p.seen, append([]llm.Message(nil), messages...))
	if len(p.seen) == 1 {
		args, _ := json.Marshal(map[string]string{"command": "echo " + bddDecisionsMarker})
		tc := llm.ToolCall{ID: bddDecisionsCallID, Name: "run_command", InputJSON: string(args)}
		onChunk(llm.StreamChunk{ToolCall: &tc})
		return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use"}, nil
	}
	onChunk(llm.StreamChunk{TextDelta: bddDecisionsAnswer})
	return &llm.Response{Content: bddDecisionsAnswer, StopReason: "end_turn"}, nil
}

type bddDecisionsSender struct {
	resumePermissionSender
	updates     []interface{}
	permissions int
}

func (s *bddDecisionsSender) SendSessionUpdate(_ string, update interface{}) error {
	s.updates = append(s.updates, update)
	return nil
}

func (s *bddDecisionsSender) RequestPermission(ctx context.Context, p acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.permissions++
	return s.resumePermissionSender.RequestPermission(ctx, p)
}

type bddDecisionsState struct {
	tmpDirs  []string
	st       *session.State
	ag       *Agent
	cfg      *config.Config
	provider *bddDecisionsProvider
	sender   *bddDecisionsSender

	stand     *httptest.Server
	standHits atomic.Int32
	oldBase   string
	hadBase   bool

	stop   string
	runErr error
}

func (s *bddDecisionsState) reset() error {
	s.close()
	s.sender = &bddDecisionsSender{}
	s.provider = nil
	s.stop = ""
	s.runErr = nil
	return nil
}

func (s *bddDecisionsState) close() {
	for _, d := range s.tmpDirs {
		_ = os.RemoveAll(d)
	}
	s.tmpDirs = nil
	s.st = nil
	s.ag = nil
	s.cfg = nil
	if s.stand != nil {
		s.stand.Close()
		s.stand = nil
	}
	if s.hadBase {
		_ = os.Setenv(llm.EnvNeuralDeepBaseURL, s.oldBase)
	} else {
		_ = os.Unsetenv(llm.EnvNeuralDeepBaseURL)
	}
	s.hadBase = false
}

func (s *bddDecisionsState) tempDir() (string, error) {
	d, err := os.MkdirTemp("", "coddy-bdd-decisions-*")
	if err != nil {
		return "", err
	}
	s.tmpDirs = append(s.tmpDirs, d)
	return d, nil
}

func (s *bddDecisionsState) sessionUnderPermissions(mode string) error {
	var permMode string
	switch mode {
	case "bypass":
		permMode = config.PermModeBypass
	case "ask":
		permMode = config.PermModeAsk
	default:
		return fmt.Errorf("the suite spells bypass or ask permissions, got %q", mode)
	}
	cwd, err := s.tempDir()
	if err != nil {
		return err
	}
	sessionDir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.st = &session.State{
		ID:         "sess_bdd_decisions",
		CWD:        cwd,
		Mode:       session.ModeAgent,
		SessionDir: sessionDir,
	}
	s.cfg = &config.Config{
		Paths: config.Paths{Home: cwd},
		// The fake row serves the scripted model; the neuraldeep row is the
		// credential the decisions check resolves.
		Providers: []config.ProviderConfig{
			{Name: "fake", Type: "openai", APIKey: "test"},
			{Name: "neuraldeep", Type: "neuraldeep", APIKey: "sk-bdd-stand-key-0123456789"},
		},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model", MaxTurns: 6},
		Tools:     config.Tools{PermissionMode: permMode},
		Decisions: config.DecisionsConfig{Enabled: true},
	}
	_ = os.Unsetenv("NEURALDEEP_API_KEY")
	return nil
}

// endpointClassifies serves one canned choice for every decision request and
// redirects the client at itself through the base-URL environment variable.
func (s *bddDecisionsState) endpointClassifies(choice string) error {
	body := fmt.Sprintf(`{"answers":{"safety":{"choice":%q,"probabilities":{"safe":0.01,"unsafe":0.99}}}}`, choice)
	s.stand = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.standHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/decisions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	s.oldBase, s.hadBase = os.LookupEnv(llm.EnvNeuralDeepBaseURL)
	_ = os.Setenv(llm.EnvNeuralDeepBaseURL, s.stand.URL)
	return nil
}

func (s *bddDecisionsState) endpointWithoutCredential() error {
	// No stand and no neuraldeep row: the client refuses before any request,
	// which is the no-credential path a machine without a sign-in takes.
	kept := s.cfg.Providers[:0]
	for _, p := range s.cfg.Providers {
		if p.Type != "neuraldeep" {
			kept = append(kept, p)
		}
	}
	s.cfg.Providers = kept
	return nil
}

func (s *bddDecisionsState) modelRunsTheCommandThenAnswers() error {
	if s.st == nil {
		return fmt.Errorf("no session prepared")
	}
	s.provider = &bddDecisionsProvider{}
	s.ag = NewAgent(s.cfg, s.st, s.sender, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return s.provider, nil
	}
	return nil
}

func (s *bddDecisionsState) userAsksQuestion() error {
	if s.ag == nil {
		return fmt.Errorf("no agent prepared")
	}
	var err error
	s.stop, err = s.ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "run the check command"}})
	if err != nil {
		return fmt.Errorf("turn failed: %v (stop=%q)", err, s.stop)
	}
	return nil
}

func (s *bddDecisionsState) toolResult() (string, error) {
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleTool && m.ToolCallID == bddDecisionsCallID {
			return m.Content, nil
		}
	}
	return "", fmt.Errorf("no tool result recorded for %s", bddDecisionsCallID)
}

func (s *bddDecisionsState) toolCallAnsweredWithTheUnsafeRejection() error {
	res, err := s.toolResult()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(res, commandRejectedAsUnsafePrefix) {
		return fmt.Errorf("tool result is not the unsafe rejection: %q", res)
	}
	cancelled := false
	for _, u := range s.sender.updates {
		if up, ok := u.(acp.ToolCallStatusUpdate); ok && up.ToolCallID == bddDecisionsCallID && up.Status == "cancelled" {
			cancelled = true
		}
	}
	if !cancelled {
		return fmt.Errorf("no cancelled tool_call_update was sent for %s", bddDecisionsCallID)
	}
	// The refusal must reach the model on the next round, or it cannot
	// choose another path.
	replayed := false
	for _, m := range s.provider.seen[1] {
		if m.Role == llm.RoleTool && m.ToolCallID == bddDecisionsCallID && strings.HasPrefix(m.Content, commandRejectedAsUnsafePrefix) {
			replayed = true
		}
	}
	if !replayed {
		return fmt.Errorf("the rejection was not replayed to the model as the tool result")
	}
	return nil
}

func (s *bddDecisionsState) toolCallAnsweredWithTheNotExecutedRefusal() error {
	res, err := s.toolResult()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(res, commandNotExecutedPrefix) {
		return fmt.Errorf("tool result is not the not-executed refusal: %q", res)
	}
	return nil
}

func (s *bddDecisionsState) commandOutputInSession() error {
	res, err := s.toolResult()
	if err != nil {
		return err
	}
	if !strings.Contains(res, bddDecisionsMarker) {
		return fmt.Errorf("tool result does not carry the command output: %q", res)
	}
	return nil
}

func (s *bddDecisionsState) commandOutputNotInSession() error {
	res, err := s.toolResult()
	if err != nil {
		return err
	}
	if strings.Contains(res, bddDecisionsMarker) {
		return fmt.Errorf("the command ran anyway: %q", res)
	}
	return nil
}

func (s *bddDecisionsState) decisionsEndpointWasNotAsked() error {
	if hits := s.standHits.Load(); hits != 0 {
		return fmt.Errorf("the decisions endpoint was asked %d time(s)", hits)
	}
	return nil
}

func (s *bddDecisionsState) turnEndsWithAnswer() error {
	if s.runErr != nil {
		return fmt.Errorf("turn failed: %v", s.runErr)
	}
	if s.stop != string(acp.StopReasonEndTurn) {
		return fmt.Errorf("stop reason = %q, want end_turn", s.stop)
	}
	return nil
}

func initializeDecisionsScenario(sc *godog.ScenarioContext) {
	s := &bddDecisionsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a coddy session in agent mode under (bypass|ask) permissions with the decisions check enabled$`, s.sessionUnderPermissions)
	sc.Step(`^a decisions endpoint that classifies every command as (safe|unsafe)$`, s.endpointClassifies)
	sc.Step(`^a decisions endpoint without a credential$`, s.endpointWithoutCredential)
	sc.Step(`^a model that runs the shell command once, then answers$`, s.modelRunsTheCommandThenAnswers)
	sc.Step(`^the user asks a question$`, s.userAsksQuestion)
	sc.Step(`^the tool call is answered with the unsafe rejection$`, s.toolCallAnsweredWithTheUnsafeRejection)
	sc.Step(`^the tool call is answered with the not-executed refusal$`, s.toolCallAnsweredWithTheNotExecutedRefusal)
	sc.Step(`^the command output is in the session$`, s.commandOutputInSession)
	sc.Step(`^the command output is not in the session$`, s.commandOutputNotInSession)
	sc.Step(`^the decisions endpoint was not asked$`, s.decisionsEndpointWasNotAsked)
	sc.Step(`^the turn ends with the model's answer$`, s.turnEndsWithAnswer)
}

func TestDecisionsCheckFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "decisions-check",
		ScenarioInitializer: initializeDecisionsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/decisions_check.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("decisions check feature suite failed")
	}
}
