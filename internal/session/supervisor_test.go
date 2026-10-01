package session_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type supervisorHeldTask struct {
	once    sync.Once
	release chan struct{}
}

func (h *supervisorHeldTask) Wait() (int, error) { <-h.release; return 0, nil }
func (h *supervisorHeldTask) Stop(time.Duration) error {
	h.once.Do(func() { close(h.release) })
	return nil
}
func (*supervisorHeldTask) PID() int                    { return 0 }
func (*supervisorHeldTask) ProcessStartedAt() time.Time { return time.Time{} }

type holdingPermissionSender struct {
	started chan struct{}
	release chan struct{}
}

func (*holdingPermissionSender) SendSessionUpdate(string, interface{}) error { return nil }
func (s *holdingPermissionSender) RequestPermission(ctx context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	close(s.started)
	select {
	case <-s.release:
		return &acp.PermissionResult{Outcome: "allow"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*holdingPermissionSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func supervisorTestManager(t *testing.T, run session.AgentRunner) (*session.Manager, string) {
	t.Helper()
	mgr, sid := newQueueManagerWith(t, run)
	mgr.SessionByID(sid).SetGoal(session.GoalState{Text: "finish the task", Status: session.GoalActive})
	mgr.SetSupervisorJudge(func(context.Context, *config.Config, *session.State, string) (session.SupervisorVerdict, error) {
		return session.SupervisorVerdict{Done: true}, nil
	})
	return mgr, sid
}

func runSupervisorPrompt(t *testing.T, mgr *session.Manager, sid string) *acp.SessionPromptResult {
	t.Helper()
	result, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "start"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSupervisorInterruptsStalledToolAndContinues(t *testing.T) {
	calls := 0
	mgr, sid := supervisorTestManager(t, func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		calls++
		if calls == 1 {
			<-ctx.Done()
			return string(acp.StopReasonCancelled), ctx.Err()
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: prompt[0].Text})
		return string(acp.StopReasonEndTurn), nil
	})
	one := 1
	mgr.Cfg().Supervisor.StallSeconds = &one
	if got := runSupervisorPrompt(t, mgr, sid); got.StopReason != acp.StopReasonEndTurn || calls != 2 {
		t.Fatalf("stop=%s calls=%d, want continuation after stall", got.StopReason, calls)
	}
}

func TestSupervisorNudgesRepeatedToolOperations(t *testing.T) {
	calls := 0
	mgr, sid := supervisorTestManager(t, func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		calls++
		if calls == 1 {
			toolCalls := make([]llm.ToolCall, 0, 3)
			for i := 0; i < 3; i++ {
				toolCalls = append(toolCalls, llm.ToolCall{ID: fmt.Sprintf("call_%d", i), Name: "write_file", InputJSON: `{"path":"file.txt","content":"same edit"}`})
			}
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: toolCalls})
			for _, call := range toolCalls {
				_ = sender.SendSessionUpdate(st.ID, acp.ToolCallUpdate{SessionUpdate: acp.UpdateTypeToolCall, ToolCallID: call.ID, Kind: "write", Title: "write_file"})
				_ = sender.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: call.ID, Status: "completed"})
			}
			<-ctx.Done()
			return string(acp.StopReasonCancelled), ctx.Err()
		}
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: prompt[0].Text})
		return string(acp.StopReasonEndTurn), nil
	})
	zero := 0
	mgr.Cfg().Supervisor.StallSeconds = &zero
	if got := runSupervisorPrompt(t, mgr, sid); got.StopReason != acp.StopReasonEndTurn || calls != 2 {
		t.Fatalf("stop=%s calls=%d, want loop nudge", got.StopReason, calls)
	}
}

func TestSupervisorDoesNotTreatDifferentArgumentsAsLoop(t *testing.T) {
	calls := 0
	mgr, sid := supervisorTestManager(t, func(_ context.Context, st *session.State, _ []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		calls++
		toolCalls := make([]llm.ToolCall, 0, 3)
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("call_%d", i)
			toolCalls = append(toolCalls, llm.ToolCall{ID: id, Name: "read_file", InputJSON: fmt.Sprintf(`{"path":"file%d.txt"}`, i)})
		}
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: toolCalls})
		for _, call := range toolCalls {
			_ = sender.SendSessionUpdate(st.ID, acp.ToolCallUpdate{SessionUpdate: acp.UpdateTypeToolCall, ToolCallID: call.ID, Kind: "read", Title: "read_file"})
			_ = sender.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: call.ID, Status: "completed"})
		}
		return string(acp.StopReasonEndTurn), nil
	})
	zero := 0
	mgr.Cfg().Supervisor.StallSeconds = &zero
	runSupervisorPrompt(t, mgr, sid)
	if calls != 1 {
		t.Fatalf("distinct tool arguments caused %d runs, want one", calls)
	}
}

func TestSupervisorStopsAtContinuationLimit(t *testing.T) {
	calls := 0
	mgr, sid := supervisorTestManager(t, func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		calls++
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: prompt[0].Text})
		return string(acp.StopReasonEndTurn), nil
	})
	mgr.SetSupervisorJudge(func(context.Context, *config.Config, *session.State, string) (session.SupervisorVerdict, error) {
		return session.SupervisorVerdict{Remaining: "write the tests"}, nil
	})
	two := 2
	mgr.Cfg().Supervisor.MaxContinuations = &two
	result := runSupervisorPrompt(t, mgr, sid)
	goal := mgr.SessionByID(sid).GetGoal()
	if calls != 3 || result.StopReason != acp.StopReasonRefused || goal.Status != session.GoalStopped || goal.Continuations != 2 {
		t.Fatalf("calls=%d stop=%s goal=%+v", calls, result.StopReason, goal)
	}
	if !strings.Contains(result.StopNotice, "2 automatic continuations") {
		t.Fatalf("stop notice = %q", result.StopNotice)
	}
}

func TestSupervisorStallTimerPausesForPermission(t *testing.T) {
	sender := &holdingPermissionSender{started: make(chan struct{}), release: make(chan struct{})}
	mgr, sid := supervisorTestManager(t, func(ctx context.Context, st *session.State, _ []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		if _, err := snd.RequestPermission(ctx, acp.PermissionRequestParams{}); err != nil {
			return string(acp.StopReasonCancelled), err
		}
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "approved"})
		return string(acp.StopReasonEndTurn), nil
	})
	one := 1
	mgr.Cfg().Supervisor.StallSeconds = &one
	done := make(chan error, 1)
	go func() {
		_, err := mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
			SessionID: sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "start"}},
		}, sender, nil)
		done <- err
	}()
	<-sender.started
	select {
	case err := <-done:
		t.Fatalf("permission wait was interrupted early: %v", err)
	case <-time.After(1200 * time.Millisecond):
	}
	close(sender.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorChecksLatestRequestWithoutGoalWhenEnabled(t *testing.T) {
	mgr, sid := newQueueManagerWith(t, func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	})
	mgr.Cfg().Supervisor.Enable = true
	var objective string
	mgr.SetSupervisorJudge(func(_ context.Context, _ *config.Config, _ *session.State, text string) (session.SupervisorVerdict, error) {
		objective = text
		return session.SupervisorVerdict{Done: true}, nil
	})
	runSupervisorPrompt(t, mgr, sid)
	if objective != "start" {
		t.Fatalf("implicit objective = %q, want latest request", objective)
	}
}

func TestSupervisorIgnoresCompletedGoalForNewRequest(t *testing.T) {
	mgr, sid := newQueueManagerWith(t, func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	})
	mgr.Cfg().Supervisor.Enable = true
	mgr.SessionByID(sid).SetGoal(session.GoalState{Text: "old task", Status: session.GoalComplete, Continuations: 3})
	var objective string
	mgr.SetSupervisorJudge(func(_ context.Context, _ *config.Config, _ *session.State, text string) (session.SupervisorVerdict, error) {
		objective = text
		return session.SupervisorVerdict{Done: true}, nil
	})
	runSupervisorPrompt(t, mgr, sid)
	if objective != "start" {
		t.Fatalf("completed goal reused as objective: %q", objective)
	}
}

func TestSupervisorWaitsForRunningBackgroundWork(t *testing.T) {
	mgr, sid := newQueueManagerWith(t, func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	})
	mgr.SessionByID(sid).SetGoal(session.GoalState{Text: "wait for build", Status: session.GoalActive})
	checks := 0
	mgr.SetSupervisorJudge(func(context.Context, *config.Config, *session.State, string) (session.SupervisorVerdict, error) {
		checks++
		return session.SupervisorVerdict{Done: false, Remaining: "build result"}, nil
	})
	pool := bgtask.Default()
	h := &supervisorHeldTask{release: make(chan struct{})}
	started := make(chan struct{})
	if _, err := pool.Launch(bgtask.Spec{SessionID: sid, Kind: bgtask.KindCommand, Label: "build", Command: "test-build", NoTimeout: true},
		func(string, io.Writer) (bgtask.Handle, error) { close(started); return h, nil }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.StopSession(sid) })
	<-started
	runSupervisorPrompt(t, mgr, sid)
	if checks != 0 || mgr.SessionByID(sid).GetGoal().Status != session.GoalActive {
		t.Fatalf("background work was interrupted or evaluated: checks=%d goal=%+v", checks, mgr.SessionByID(sid).GetGoal())
	}
}

func TestSupervisorDoesNotContinueAfterUserStopDuringVerdict(t *testing.T) {
	calls := 0
	mgr, sid := newQueueManagerWith(t, func(_ context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		calls++
		return string(acp.StopReasonEndTurn), nil
	})
	mgr.SessionByID(sid).SetGoal(session.GoalState{Text: "finish the task", Status: session.GoalActive})
	checking := make(chan struct{})
	mgr.SetSupervisorJudge(func(ctx context.Context, _ *config.Config, _ *session.State, _ string) (session.SupervisorVerdict, error) {
		close(checking)
		<-ctx.Done()
		return session.SupervisorVerdict{}, ctx.Err()
	})
	done := make(chan *acp.SessionPromptResult, 1)
	go func() {
		result, _ := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "start"}},
		})
		done <- result
	}()
	<-checking
	mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: sid})
	select {
	case result := <-done:
		if result == nil || result.StopReason != acp.StopReasonCancelled || calls != 1 || mgr.SessionByID(sid).GetGoal().Status != session.GoalActive {
			t.Fatalf("Stop started more work or changed goal: result=%+v calls=%d goal=%+v", result, calls, mgr.SessionByID(sid).GetGoal())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not stop after user cancellation")
	}
}
