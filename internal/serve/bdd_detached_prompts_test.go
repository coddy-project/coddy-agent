package serve

// Godog harness for features/subagents_detached_prompts.feature: how the runtime
// hands a detached subagent's permission prompt to the surfaces of one
// `coddy serve` process. The surfaces are stand-ins, because the question is the
// runtime's fan-out, not how any one surface draws the prompt.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
)

// promptSurface is a stand-in for a surface that shows a detached prompt and
// waits for a person to answer it.
type promptSurface struct {
	// owns is whether this surface can show the prompt at all.
	owns bool

	shown     chan agent.DetachedPermissionRequest
	answers   chan *acp.PermissionResult
	withdrawn chan struct{}
	declined  chan struct{}
}

func newPromptSurface(owns bool) *promptSurface {
	return &promptSurface{
		owns:      owns,
		shown:     make(chan agent.DetachedPermissionRequest, 1),
		answers:   make(chan *acp.PermissionResult, 1),
		withdrawn: make(chan struct{}, 1),
		declined:  make(chan struct{}, 1),
	}
}

func (p *promptSurface) RequestDetachedPermission(ctx context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	if !p.owns {
		p.declined <- struct{}{}
		return nil, agent.ErrNoDetachedApprover
	}
	p.shown <- req
	select {
	case res := <-p.answers:
		return res, nil
	case <-ctx.Done():
		p.withdrawn <- struct{}{}
		return nil, nil
	}
}

type detachedPromptsState struct {
	rt    *Runtime
	web   *promptSurface
	chat  *promptSurface
	agent string

	result chan *acp.PermissionResult
	errs   chan error
}

func (s *detachedPromptsState) reset() {
	s.rt = &Runtime{}
	s.web, s.chat = nil, nil
	s.agent = ""
	s.result = make(chan *acp.PermissionResult, 1)
	s.errs = make(chan error, 1)
}

const promptStepTimeout = 5 * time.Second

func (s *detachedPromptsState) webAndChatCanShow() error {
	s.web = newPromptSurface(true)
	s.chat = newPromptSurface(true)
	s.rt.AddDetachedPermissionApprover(s.web)
	s.rt.AddDetachedPermissionApprover(s.chat)
	return nil
}

func (s *detachedPromptsState) webCanShow() error {
	s.web = newPromptSurface(true)
	s.rt.AddDetachedPermissionApprover(s.web)
	return nil
}

func (s *detachedPromptsState) chatDoesNotKnowTheSession() error {
	s.chat = newPromptSurface(false)
	s.rt.AddDetachedPermissionApprover(s.chat)
	return nil
}

func (s *detachedPromptsState) subagentAsks(name string) error {
	s.agent = name
	req := agent.DetachedPermissionRequest{
		ParentSessionID: "sess_parent",
		ChildSessionID:  "sess_child",
		TaskID:          "bg_1",
		AgentName:       name,
		Params: acp.PermissionRequestParams{
			SessionID: "sess_child",
			ToolCall:  acp.PermissionToolCall{ToolCallID: "call_1", Title: "[subagent " + name + "] Run: echo hi"},
			Options: []acp.PermissionOption{
				{OptionID: "allow", Name: "Allow"},
				{OptionID: "reject", Name: "Reject"},
			},
		},
	}
	go func() {
		res, err := s.rt.RequestDetachedPermission(context.Background(), req)
		if err != nil {
			s.errs <- err
			return
		}
		s.result <- res
	}()
	return nil
}

func (s *detachedPromptsState) surface(name string) (*promptSurface, error) {
	switch name {
	case "web":
		return s.web, nil
	case "chat":
		return s.chat, nil
	}
	return nil, fmt.Errorf("no %s surface in this scenario", name)
}

func (s *detachedPromptsState) surfaceShowsPromptOf(name, agentName string) error {
	p, err := s.surface(name)
	if err != nil {
		return err
	}
	select {
	case req := <-p.shown:
		if req.AgentName != agentName {
			return fmt.Errorf("the %s surface shows the prompt of %q, want %q", name, req.AgentName, agentName)
		}
		return nil
	case <-time.After(promptStepTimeout):
		return fmt.Errorf("the %s surface never showed the prompt", name)
	}
}

func (s *detachedPromptsState) surfaceShowsNothing(name string) error {
	p, err := s.surface(name)
	if err != nil {
		return err
	}
	select {
	case <-p.declined:
	case <-time.After(promptStepTimeout):
		return fmt.Errorf("the %s surface was never offered the prompt", name)
	}
	select {
	case <-p.shown:
		return fmt.Errorf("the %s surface showed a prompt it cannot own", name)
	default:
		return nil
	}
}

func (s *detachedPromptsState) surfaceAnswers(name, optionID string) error {
	p, err := s.surface(name)
	if err != nil {
		return err
	}
	p.answers <- &acp.PermissionResult{Outcome: "selected", OptionID: optionID}
	return nil
}

func (s *detachedPromptsState) subagentAnswered(optionID string) error {
	select {
	case res := <-s.result:
		if res == nil || res.OptionID != optionID {
			return fmt.Errorf("the subagent was answered %+v, want %q", res, optionID)
		}
		return nil
	case err := <-s.errs:
		return fmt.Errorf("the subagent got an error instead of an answer: %w", err)
	case <-time.After(promptStepTimeout):
		return errors.New("the subagent was never answered")
	}
}

func (s *detachedPromptsState) promptWithdrawnFrom(name string) error {
	p, err := s.surface(name)
	if err != nil {
		return err
	}
	select {
	case <-p.withdrawn:
		return nil
	case <-time.After(promptStepTimeout):
		return fmt.Errorf("the prompt stayed on the %s surface after it was answered", name)
	}
}

func initializeDetachedPromptsScenario(sc *godog.ScenarioContext) {
	s := &detachedPromptsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.Step(`^a web surface and a chat surface that can both show the prompt$`, s.webAndChatCanShow)
	sc.Step(`^a web surface that can show the prompt$`, s.webCanShow)
	sc.Step(`^a chat surface that does not know the parent session$`, s.chatDoesNotKnowTheSession)
	sc.Step(`^a detached subagent "([^"]*)" asks for permission$`, s.subagentAsks)
	sc.Step(`^the (web|chat) surface shows the prompt of "([^"]*)"$`, s.surfaceShowsPromptOf)
	sc.Step(`^the (web|chat) surface shows nothing$`, s.surfaceShowsNothing)
	sc.Step(`^the (web|chat) surface answers "([^"]*)"$`, s.surfaceAnswers)
	sc.Step(`^the subagent is answered "([^"]*)"$`, s.subagentAnswered)
	sc.Step(`^the prompt is withdrawn from the (web|chat) surface$`, s.promptWithdrawnFrom)
}

func TestSubagentsDetachedPromptsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "subagents-detached-prompts",
		ScenarioInitializer: initializeDetachedPromptsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/subagents_detached_prompts.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("subagents detached prompts feature suite failed")
	}
}
