package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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
	// judged and verified count the checks and the verifications that ran.
	judged, verified int
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

// workerRuns: the worker is a scripted runner rather than the real agent, so
// the command is recorded and never executed. Its first turn calls run_command
// with the command, stores an executed result and answers; every judge verdict
// is met, and the judge and the verifier count their calls.
func (w *supervisorWorld) workerRuns(command string) error {
	turn := 0
	w.mgr, w.sid = newQueueManagerWith(w.t, func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		turn++
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: prompt[0].Text, GoalTurn: st.TakeTurnGoal(), CreatedAt: time.Now().UTC().Format(time.RFC3339)})
		if turn == 1 {
			args, _ := json.Marshal(map[string]string{"command": command})
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "run_command", InputJSON: string(args)}}})
			st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: "call_1", Content: "Container db  Removed\nVolume db-data  Removed"})
		}
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "The old containers are gone."})
		return string(acp.StopReasonEndTurn), nil
	})
	w.mgr.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		w.mu.Lock()
		w.judged++
		w.mu.Unlock()
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "the containers are gone"}, nil
	})
	w.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		w.mu.Lock()
		w.verified++
		w.mu.Unlock()
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
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

func (w *supervisorWorld) blockedNaming(command string) error {
	goal := w.mgr.SessionByID(w.sid).GetGoal()
	if goal.Status != session.GoalBlocked || !strings.Contains(goal.StatusReason, command) {
		return fmt.Errorf("goal = %+v, want blocked naming %q", goal, command)
	}
	return nil
}

func (w *supervisorWorld) noCheckRan() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.judged != 0 || w.verified != 0 {
		return fmt.Errorf("the judge was asked %d times and the verifier ran %d times, want neither", w.judged, w.verified)
	}
	return nil
}

func (w *supervisorWorld) completeWithoutBlockingAgain() error {
	st := w.mgr.SessionByID(w.sid)
	goal := st.GetGoal()
	w.mu.Lock()
	defer w.mu.Unlock()
	if goal.Status != session.GoalComplete || w.judged != 1 || w.verified != 1 {
		return fmt.Errorf("goal = %+v after %d checks and %d verifications, want complete after one of each", goal, w.judged, w.verified)
	}
	blocks := 0
	for _, n := range st.GetUILog() {
		if n.Level == session.UILogLevelNotice && strings.HasPrefix(n.Message, "Goal blocked") {
			blocks++
		}
	}
	accepted := 0
	for _, m := range st.GetMessages() {
		if m.RiskAcknowledged && len(m.ToolCalls) == 1 && m.ToolCalls[0].Name == "run_command" {
			accepted++
		}
	}
	if blocks != 1 || accepted != 1 {
		return fmt.Errorf("blocked %d times and accepted %d calls, want one block and the one call accepted", blocks, accepted)
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
			sc.Step(`^an agent session whose worker runs "([^"]*)" and whose checker would answer met$`, w.workerRuns)
			sc.Step(`^the goal is blocked naming "([^"]*)"$`, w.blockedNaming)
			sc.Step(`^neither the judge nor the verifier ran$`, w.noCheckRan)
			sc.Step(`^the goal is complete and the command did not block it again$`, w.completeWithoutBlockingAgain)
		},
		Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/session_supervisor.feature"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("session supervisor feature failed")
	}
}
