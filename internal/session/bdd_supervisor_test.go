package session_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

type supervisorWorld struct {
	t       *testing.T
	mgr     *session.Manager
	sid     string
	prompts []string
	err     error
}

type goalStubModel struct{ calls int }

func (m *goalStubModel) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return m.Stream(ctx, messages, tools, func(llm.StreamChunk) {})
}

func (m *goalStubModel) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	m.calls++
	answer := "still working"
	if m.calls > 1 {
		answer = "the fix is shipped"
	}
	onChunk(llm.StreamChunk{TextDelta: answer})
	return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
}

func (w *supervisorWorld) goal() error {
	model := &goalStubModel{}
	var mgr *session.Manager
	w.mgr, w.sid = newQueueManagerWith(w.t, func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		text := prompt[0].Text
		w.prompts = append(w.prompts, text)
		loop := agent.NewAgent(mgr.Cfg(), st, sender, nil)
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return model, nil })
		return loop.Run(ctx, prompt)
	})
	mgr = w.mgr
	w.mgr.SessionByID(w.sid).SetGoal(session.GoalState{Text: "ship the fix", Status: session.GoalActive})
	w.mgr.SetSupervisorJudge(func(_ context.Context, _ *config.Config, st *session.State, _ string) (session.SupervisorVerdict, error) {
		for _, msg := range st.GetMessages() {
			if msg.Role == llm.RoleAssistant && msg.Content == "the fix is shipped" {
				return session.SupervisorVerdict{Done: true}, nil
			}
		}
		return session.SupervisorVerdict{Remaining: "ship the fix"}, nil
	})
	return nil
}

func (w *supervisorWorld) firstTurnUnfinished() error { return nil }

func (w *supervisorWorld) handle() error {
	_, w.err = w.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: w.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "please ship it"}},
	})
	return w.err
}

func (w *supervisorWorld) continuation() error {
	if len(w.prompts) != 2 || !strings.Contains(w.prompts[1], "[Supervisor continuation]") {
		return fmt.Errorf("prompts = %#v, want a visible continuation", w.prompts)
	}
	messages := w.mgr.SessionByID(w.sid).GetMessages()
	if len(messages) != 4 || !strings.Contains(messages[2].Content, "[Supervisor continuation]") {
		return fmt.Errorf("transcript = %+v, want a distinct continuation row", messages)
	}
	return nil
}

func (w *supervisorWorld) complete() error {
	goal := w.mgr.SessionByID(w.sid).GetGoal()
	if goal.Status != session.GoalComplete || goal.Continuations != 1 {
		return fmt.Errorf("goal = %+v, want complete after one continuation", goal)
	}
	return nil
}

func TestSessionSupervisorFeature(t *testing.T) {
	w := &supervisorWorld{t: t}
	suite := godog.TestSuite{
		Name: "session-supervisor",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a session goal to ship a fix$`, w.goal)
			sc.Step(`^the task runner ends its first turn without the fix$`, w.firstTurnUnfinished)
			sc.Step(`^the session handles the request$`, w.handle)
			sc.Step(`^the supervisor starts a visible continuation$`, w.continuation)
			sc.Step(`^the session goal is complete after the fix$`, w.complete)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/session_supervisor.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("session supervisor feature failed")
	}
}
