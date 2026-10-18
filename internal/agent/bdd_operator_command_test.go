package agent

// Godog harness for features/operator_command.feature: a real session.Manager
// whose hooks.json blocks run_command (the hook process is this test binary,
// internal/hooks/hooktest), a scripted model that asks run_command first and
// operator_command after the refusal, and a client that plays the operator
// through a real acp.OperatorCommandTicket.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// operatorClient is the recording client of a surface with a person who
// answers operator commands.
type operatorClient struct {
	*recordingClient
	opMu     sync.Mutex
	answer   string
	requests []acp.OperatorCommandRequest
}

func (c *operatorClient) OperatorCommandsAvailable() bool { return true }

func (c *operatorClient) RequestOperatorCommand(ctx context.Context, req acp.OperatorCommandRequest) (*acp.OperatorCommandDecision, error) {
	c.opMu.Lock()
	c.requests = append(c.requests, req)
	answer := c.answer
	c.opMu.Unlock()
	tk := acp.NewOperatorCommandTicket()
	if err := tk.Answer(answer, ""); err != nil {
		return nil, err
	}
	return tk.Wait(ctx)
}

type operatorFeatureState struct {
	hooks    *hooksFeatureState
	operator *operatorClient
}

func (s *operatorFeatureState) reset() error {
	s.hooks = &hooksFeatureState{}
	if err := s.hooks.reset(); err != nil {
		return err
	}
	s.operator = &operatorClient{recordingClient: s.hooks.client, answer: acp.OperatorActionRun}
	return nil
}

func (s *operatorFeatureState) sessionWithBlockingHook() error {
	if err := s.hooks.hookDenies("PreToolUse", "run_command", "echo"); err != nil {
		return err
	}
	return s.hooks.agentSession()
}

func (s *operatorFeatureState) operatorWillPress(button string) error {
	switch button {
	case "Run":
		s.operator.answer = acp.OperatorActionRun
	case "Decline":
		s.operator.answer = acp.OperatorActionDecline
	default:
		return fmt.Errorf("unknown button %q", button)
	}
	return nil
}

func (s *operatorFeatureState) runTurn(sender acp.UpdateSender, steps ...scriptStep) error {
	h := s.hooks
	h.provider.mu.Lock()
	h.provider.steps = steps
	h.provider.calls = 0
	h.provider.offered = nil
	h.provider.requests = nil
	h.provider.mu.Unlock()
	before := len(h.sess.GetMessages())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := h.mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: h.sess.ID,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "do the operator task"}},
	}, sender, nil); err != nil {
		return fmt.Errorf("turn: %w", err)
	}
	for _, m := range h.sess.GetMessages()[before:] {
		if m.Role == llm.RoleTool {
			h.results[m.ToolCallID] = m.Content
		}
	}
	return nil
}

func (s *operatorFeatureState) modelAsksBoth(command string) error {
	runArgs, _ := json.Marshal(map[string]string{"command": command})
	opArgs, _ := json.Marshal(map[string]string{"command": command, "reason": "a hook refused run_command"})
	return s.runTurn(s.operator,
		toolStep(llm.ToolCall{ID: "call-run", Name: "run_command", InputJSON: string(runArgs)}),
		toolStep(llm.ToolCall{ID: "call-op", Name: "operator_command", InputJSON: string(opArgs)}),
		answerStep("done with the operator's output"),
	)
}

func (s *operatorFeatureState) runCommandRefusedByHook() error {
	got := s.hooks.results["call-run"]
	if !strings.Contains(got, "blocked by hook") {
		return fmt.Errorf("run_command result %q is not the hook's block", got)
	}
	return nil
}

func (s *operatorFeatureState) operatorWasShown(command string) error {
	s.operator.opMu.Lock()
	defer s.operator.opMu.Unlock()
	if len(s.operator.requests) != 1 {
		return fmt.Errorf("the operator was asked %d times", len(s.operator.requests))
	}
	req := s.operator.requests[0]
	if req.Command != command {
		return fmt.Errorf("the operator was shown %q, want %q", req.Command, command)
	}
	want, err := platform.ResolveShell("", runtime.GOOS, nil)
	if err != nil {
		return err
	}
	if req.Shell != string(want.Kind) {
		return fmt.Errorf("shell %q, want the operator shell %q", req.Shell, want.Kind)
	}
	if req.ToolCallID != "call-op" || req.CWD == "" || req.Host == "" {
		return fmt.Errorf("the request lacks what the card shows: %+v", req)
	}
	return nil
}

// lastToolResultSent is the operator_command result as the model received it
// in its next request.
func (s *operatorFeatureState) lastToolResultSent() (string, error) {
	p := s.hooks.provider
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.requests) - 1; i >= 0; i-- {
		for _, m := range p.requests[i] {
			if m.Role == llm.RoleTool && m.ToolCallID == "call-op" {
				return m.Content, nil
			}
		}
	}
	return "", fmt.Errorf("no request to the model carried the operator_command result")
}

func (s *operatorFeatureState) modelReceivedOutput(output string, code int) error {
	got, err := s.lastToolResultSent()
	if err != nil {
		return err
	}
	if !strings.Contains(got, output) || !strings.Contains(got, fmt.Sprintf("exit code %d", code)) {
		return fmt.Errorf("the model received %q", got)
	}
	return nil
}

func (s *operatorFeatureState) turnEndedWithAnswer() error {
	msgs := s.hooks.sess.GetMessages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant || !strings.Contains(last.Content, "done with the operator's output") {
		return fmt.Errorf("last message = %s %q", last.Role, last.Content)
	}
	return nil
}

func (s *operatorFeatureState) modelToldDeclined() error {
	got, err := s.lastToolResultSent()
	if err != nil {
		return err
	}
	if !strings.Contains(got, "declined") {
		return fmt.Errorf("the model received %q", got)
	}
	return nil
}

func (s *operatorFeatureState) workspaceHasNoFile(name string) error {
	if _, err := os.Stat(filepath.Join(s.hooks.cwd, name)); !os.IsNotExist(err) {
		return fmt.Errorf("%s exists (stat err %v): the declined command ran", name, err)
	}
	return nil
}

func (s *operatorFeatureState) offeredWithOperator() error {
	if err := s.runTurn(s.operator, answerStep("hello")); err != nil {
		return err
	}
	if !s.hooks.provider.everOffered("operator_command") {
		return fmt.Errorf("a turn with an operator was not offered operator_command")
	}
	return nil
}

func (s *operatorFeatureState) notOfferedWithoutOperator() error {
	if err := s.runTurn(s.hooks.client, answerStep("hello")); err != nil {
		return err
	}
	if s.hooks.provider.everOffered("operator_command") {
		return fmt.Errorf("a turn without an operator was offered operator_command")
	}
	return nil
}

func initializeOperatorCommandScenario(sc *godog.ScenarioContext) {
	s := &operatorFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.hooks.close()
		return ctx, nil
	})
	sc.Step(`^an operator command session where a hook blocks run_command$`, s.sessionWithBlockingHook)
	sc.Step(`^the operator will press (Run|Decline)$`, s.operatorWillPress)
	sc.Step(`^the model asks for "([^"]*)" with run_command and then with operator_command$`, s.modelAsksBoth)
	sc.Step(`^run_command was refused by the hook$`, s.runCommandRefusedByHook)
	sc.Step(`^the operator was shown the command "([^"]*)" in the operator shell$`, s.operatorWasShown)
	sc.Step(`^the model received the output "([^"]*)" with exit code (\d+)$`, s.modelReceivedOutput)
	sc.Step(`^the turn ended with the model's answer$`, s.turnEndedWithAnswer)
	sc.Step(`^the model was told the operator declined to run the command$`, s.modelToldDeclined)
	sc.Step(`^the workspace has no file "([^"]*)"$`, s.workspaceHasNoFile)
	sc.Step(`^a turn on a surface with an operator is offered operator_command$`, s.offeredWithOperator)
	sc.Step(`^a turn on a surface without an operator is not offered operator_command$`, s.notOfferedWithoutOperator)
}

func TestOperatorCommandFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "operator-command",
		ScenarioInitializer: initializeOperatorCommandScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/operator_command.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("operator command feature suite failed")
	}
}
