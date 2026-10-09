package session_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
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
	model   *goalStubModel
	updates []interface{}
	mu      sync.Mutex
}

// goalStubModel answers each model call in turn: answers[i] for call i, the
// last one repeated.
type goalStubModel struct {
	mu      sync.Mutex
	calls   int
	answers []string
}

func (m *goalStubModel) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDefinition) (*llm.Response, error) {
	return m.Stream(ctx, messages, tools, func(llm.StreamChunk) {})
}

func (m *goalStubModel) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	m.mu.Lock()
	answer := m.answers[min(m.calls, len(m.answers)-1)]
	m.calls++
	m.mu.Unlock()
	onChunk(llm.StreamChunk{TextDelta: answer})
	return &llm.Response{Content: answer, StopReason: "end_turn"}, nil
}

type recordingSender struct{ w *supervisorWorld }

func (s recordingSender) SendSessionUpdate(_ string, update interface{}) error {
	s.w.mu.Lock()
	s.w.updates = append(s.w.updates, update)
	s.w.mu.Unlock()
	return nil
}
func (recordingSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}
func (recordingSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (w *supervisorWorld) session(answers []string, judge session.GoalJudge) {
	w.model = &goalStubModel{answers: answers}
	var mgr *session.Manager
	w.mgr, w.sid = newQueueManagerWith(w.t, func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		loop := agent.NewAgent(mgr.Cfg(), st, sender, nil)
		loop.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return w.model, nil })
		return loop.Run(ctx, prompt)
	})
	mgr = w.mgr
	w.mgr.SetGoalJudge(judge)
}

func (w *supervisorWorld) secondTurnShips() error {
	w.session([]string{"I looked at the code.", "The fix is shipped."}, func(_ context.Context, _ *config.Config, st *session.State, req session.GoalCheckRequest) (session.GoalCheckResult, error) {
		if strings.Contains(req.Digest, "The fix is shipped.") {
			return session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "the answer reports the fix"}, nil
		}
		return session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: "nothing changed yet", Remaining: []string{"ship the fix"}}, nil
	})
	return nil
}

func (w *supervisorWorld) asksForDatabase() error {
	w.session([]string{"Which database should the store move to?"}, func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		return session.GoalCheckResult{Verdict: session.GoalVerdictNeedsUser, Reason: "which database should the store move to?"}, nil
	})
	return nil
}

func (w *supervisorWorld) send(text string) error {
	_, err := w.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: w.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
	}, recordingSender{w}, nil)
	return err
}

func (w *supervisorWorld) userMessages() []llm.Message {
	var out []llm.Message
	for _, m := range w.mgr.SessionByID(w.sid).GetMessages() {
		if m.Role == llm.RoleUser {
			out = append(out, m)
		}
	}
	return out
}

func (w *supervisorWorld) firstIsKickoff() error {
	users := w.userMessages()
	if len(users) == 0 || users[0].GoalTurn == nil || users[0].GoalTurn.Kind != acp.GoalTurnKickoff || users[0].GoalTurn.Objective != "ship the fix" {
		return fmt.Errorf("first message = %+v, want the goal kickoff", users)
	}
	return nil
}

func (w *supervisorWorld) continuationNamesWhatIsLeft() error {
	users := w.userMessages()
	if len(users) != 2 || users[1].GoalTurn == nil || users[1].GoalTurn.Kind != acp.GoalTurnContinue ||
		!strings.Contains(users[1].Content, "nothing changed yet") || len(users[1].GoalTurn.Remaining) != 1 {
		return fmt.Errorf("user messages = %+v, want a continuation naming what is left", users)
	}
	return nil
}

func (w *supervisorWorld) completeAfterOne() error {
	goal := w.mgr.SessionByID(w.sid).GetGoal()
	if goal.Status != session.GoalComplete || goal.Continuations != 1 || goal.Checks != 2 {
		return fmt.Errorf("goal = %+v, want complete after one continuation", goal)
	}
	return nil
}

func (w *supervisorWorld) noOperatorEcho() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	turns := 0
	for _, u := range w.updates {
		switch u := u.(type) {
		case acp.MessageChunkUpdate:
			if u.SessionUpdate == acp.UpdateTypeUserMessageChunk {
				return fmt.Errorf("a supervisor turn was echoed as the operator's text: %q", u.Content.Text)
			}
		case acp.GoalTurnUpdate:
			turns++
		}
	}
	if turns != 2 {
		return fmt.Errorf("goal_turn updates = %d, want 2", turns)
	}
	return nil
}

func (w *supervisorWorld) blockedWithQuestion() error {
	goal := w.mgr.SessionByID(w.sid).GetGoal()
	if goal.Status != session.GoalBlocked || !strings.Contains(goal.StatusReason, "which database") {
		return fmt.Errorf("goal = %+v, want blocked with the question", goal)
	}
	return nil
}

func (w *supervisorWorld) noContinuation() error {
	if users := w.userMessages(); len(users) != 1 {
		return fmt.Errorf("user messages = %d, want the kickoff only", len(users))
	}
	return nil
}

func TestSessionSupervisorFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "session-supervisor",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			w := &supervisorWorld{t: t}
			sc.Step(`^an agent session whose model ships the fix only on its second turn$`, w.secondTurnShips)
			sc.Step(`^an agent session whose model asks which database to use$`, w.asksForDatabase)
			sc.Step(`^the operator sends "([^"]*)"$`, w.send)
			sc.Step(`^the first turn is the goal kickoff$`, w.firstIsKickoff)
			sc.Step(`^the supervisor continues the work naming what is left$`, w.continuationNamesWhatIsLeft)
			sc.Step(`^the goal is complete after one continuation$`, w.completeAfterOne)
			sc.Step(`^no supervisor turn reads as a message the operator typed$`, w.noOperatorEcho)
			sc.Step(`^the goal is blocked with the question for the operator$`, w.blockedWithQuestion)
			sc.Step(`^no continuation was started$`, w.noContinuation)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/session_supervisor.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("session supervisor feature failed")
	}
}
