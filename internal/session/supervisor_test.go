package session_test

import (
	"context"
	"errors"
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

// goalStep is what one run of the harness's runner does after recording the
// prompt: by default it calls one tool with fresh arguments and answers.
type goalStep func(ctx context.Context, st *session.State, snd acp.UpdateSender, run int) (string, error)

// goalHarness drives a manager with a scripted runner and a scripted check.
type goalHarness struct {
	t   *testing.T
	mgr *session.Manager
	sid string

	mu       sync.Mutex
	prompts  []string
	markers  []*llm.GoalTurn
	steps    map[int]goalStep
	verdicts []goalVerdictStep
	checks   []session.GoalCheckRequest
	verifies int
}

type goalVerdictStep struct {
	result session.GoalCheckResult
	err    error
}

func newGoalHarness(t *testing.T) *goalHarness {
	t.Helper()
	h := &goalHarness{t: t, steps: map[int]goalStep{}}
	h.mgr, h.sid = newQueueManagerWith(t, h.run)
	h.mgr.SetGoalJudge(h.judge)
	return h
}

func (h *goalHarness) st() *session.State { return h.mgr.SessionByID(h.sid) }

func (h *goalHarness) run(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
	marker := st.TakeTurnGoal()
	text := ""
	if len(prompt) > 0 {
		text = prompt[0].Text
	}
	h.mu.Lock()
	n := len(h.prompts)
	h.prompts = append(h.prompts, text)
	h.markers = append(h.markers, marker)
	step := h.steps[n]
	h.mu.Unlock()
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text, GoalTurn: marker, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
	if step != nil {
		return step(ctx, st, snd, n)
	}
	return toolTurn(st, snd, fmt.Sprintf(`{"path":"file%d.txt"}`, n), "ok "+fmt.Sprint(n)), nil
}

// toolTurn records one finished tool call with args and result, then a final
// answer, the way the agent reports them.
func toolTurn(st *session.State, snd acp.UpdateSender, args, result string) string {
	id := fmt.Sprintf("call_%d", time.Now().UnixNano())
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read", InputJSON: args}}})
	_ = snd.SendSessionUpdate(st.ID, acp.ToolCallUpdate{SessionUpdate: acp.UpdateTypeToolCall, ToolCallID: id, Kind: "read", Title: "read", Status: "pending"})
	_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: id, Status: "in_progress"})
	st.AddMessage(llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: result})
	_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: id, Status: "completed",
		Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: result}}}})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done for now"})
	return string(acp.StopReasonEndTurn)
}

func (h *goalHarness) judge(_ context.Context, _ *config.Config, _ *session.State, req session.GoalCheckRequest) (session.GoalCheckResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks = append(h.checks, req)
	if len(h.verdicts) == 0 {
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "all done"}, nil
	}
	v := h.verdicts[0]
	if len(h.verdicts) > 1 {
		h.verdicts = h.verdicts[1:]
	}
	return v.result, v.err
}

func (h *goalHarness) verdict(results ...goalVerdictStep) { h.verdicts = results }

func notMet(reason string, remaining ...string) goalVerdictStep {
	return goalVerdictStep{result: session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: reason, Remaining: remaining}}
}

func met() goalVerdictStep {
	return goalVerdictStep{result: session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "tests pass"}}
}

func (h *goalHarness) prompt(text string) *acp.SessionPromptResult {
	h.t.Helper()
	res, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}},
	})
	if err != nil {
		h.t.Fatalf("prompt %q: %v", text, err)
	}
	return res
}

func (h *goalHarness) kinds() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.markers))
	for i, m := range h.markers {
		if m == nil {
			out[i] = "user"
		} else {
			out[i] = m.Kind
		}
	}
	return out
}

func intp(v int) *int { return &v }

func TestGoalCommandSetsTheGoalAndStartsWorking(t *testing.T) {
	h := newGoalHarness(t)
	h.prompt("/goal make the tests pass")
	goal := h.st().GetGoal()
	if goal.Objective != "make the tests pass" || goal.Status != session.GoalComplete || goal.Checks != 1 {
		t.Fatalf("goal = %+v", goal)
	}
	if got := h.kinds(); len(got) != 1 || got[0] != acp.GoalTurnKickoff {
		t.Fatalf("turns = %v, want one kickoff", got)
	}
	if !strings.Contains(h.prompts[0], "make the tests pass") || !strings.Contains(h.prompts[0], "[Goal set]") {
		t.Fatalf("kickoff text = %q", h.prompts[0])
	}
	msgs := h.st().GetMessages()
	if msgs[0].GoalTurn == nil || msgs[0].GoalTurn.Kind != acp.GoalTurnKickoff {
		t.Fatalf("the kickoff message carries no marker: %+v", msgs[0])
	}
}

func TestGoalCommandChoosesTheCheckingModelAndLevel(t *testing.T) {
	h := newGoalHarness(t)
	levels := []string{"low", "medium", "high"}
	h.mgr.Cfg().Models[1].ReasoningLevels = &levels // p2/gpt-4o-mini
	var checkedBy, level string
	h.mgr.SetGoalJudge(func(_ context.Context, cfg *config.Config, st *session.State, _ session.GoalCheckRequest) (session.GoalCheckResult, error) {
		checkedBy, level = session.GoalCheckModel(cfg, st), session.GoalCheckReasoning(st)
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
	})
	h.prompt("/goal --model mini --reasoning high ship the fix")
	goal := h.st().GetGoal()
	if goal.Objective != "ship the fix" || goal.Model != "p2/gpt-4o-mini" || goal.Reasoning != "high" || checkedBy != "p2/gpt-4o-mini" || level != "high" {
		t.Fatalf("goal=%+v checked by %q at %q", goal, checkedBy, level)
	}
	if !strings.Contains(session.GoalStatusText(goal, 10, 0), "Checked by: p2/gpt-4o-mini (reasoning high)") {
		t.Fatalf("status = %q", session.GoalStatusText(goal, 10, 0))
	}

	// A resume keeps the pair unless it names another one; a model without
	// a level drops the level chosen for the model before it.
	h.st().UpdateGoal("", func(g *session.GoalState) bool { g.Status = session.GoalPaused; return true })
	h.prompt("/goal resume")
	if g := h.st().GetGoal(); g.Model != "p2/gpt-4o-mini" || g.Reasoning != "high" {
		t.Fatalf("resume dropped the checker: %+v", g)
	}
	h.st().UpdateGoal("", func(g *session.GoalState) bool { g.Status = session.GoalPaused; return true })
	h.prompt("/goal --model claude resume")
	if g := h.st().GetGoal(); g.Model != "p3/claude-3" || g.Reasoning != "" || checkedBy != "p3/claude-3" {
		t.Fatalf("resume with --model: goal=%+v checked by %q", g, checkedBy)
	}
}

func TestGoalCommandRefusesBadOptions(t *testing.T) {
	levels := []string{"low", "high"}
	for text, want := range map[string]string{
		"/goal --model nope ship it":                  `model "nope"`,
		"/goal --model gpt-4o --reasoning ultra ship": "is not offered by model",
		"/goal --frob x ship it":                      "unknown option --frob",
		"/goal --model mini":                          "the goal objective is empty",
	} {
		h := newGoalHarness(t)
		h.mgr.Cfg().Models[0].ReasoningLevels = &levels
		res := h.prompt(text)
		if h.st().GetGoal().Set() || len(h.prompts) != 0 || !strings.Contains(res.SettingsNotice, want) || !strings.Contains(res.SettingsNotice, "Usage: /goal") {
			t.Errorf("%s: notice=%q goal=%+v runs=%d", text, res.SettingsNotice, h.st().GetGoal(), len(h.prompts))
		}
	}
}

func TestGoalContinuesUntilTheCheckIsMet(t *testing.T) {
	h := newGoalHarness(t)
	h.verdict(notMet("tests still fail", "fix TestParse"), met())
	h.prompt("/goal fix the parser")
	if got := h.kinds(); strings.Join(got, ",") != "kickoff,continue" {
		t.Fatalf("turns = %v", got)
	}
	cont := h.prompts[1]
	for _, want := range []string{"[Goal continuation 1 of 10]", "tests still fail", "fix TestParse", "fix the parser"} {
		if !strings.Contains(cont, want) {
			t.Errorf("continuation lacks %q:\n%s", want, cont)
		}
	}
	goal := h.st().GetGoal()
	if goal.Status != session.GoalComplete || goal.Continuations != 1 || goal.Checks != 2 {
		t.Fatalf("goal = %+v", goal)
	}
	if h.markers[1].Index != 1 || h.markers[1].Reason != "tests still fail" || len(h.markers[1].Remaining) != 1 {
		t.Fatalf("continuation marker = %+v", h.markers[1])
	}
}

func TestGoalCheckReadsTheEvidenceOfTheGoalTurns(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		return toolTurn(st, snd, `{"command":"go test ./..."}`, "FAIL TestParse exit 1"), nil
	}
	h.prompt("/goal fix the parser")
	if len(h.checks) != 1 {
		t.Fatalf("checks = %d", len(h.checks))
	}
	digest := h.checks[0].Digest
	for _, want := range []string{"go test ./...", "FAIL TestParse exit 1", "[supervisor] Goal set: fix the parser", "claims, not evidence", "done for now"} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
}

func TestGoalStopsAfterTheContinuationLimitWithAWrapUp(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.MaxContinuations = intp(2)
	h.verdict(notMet("still open", "write the tests"))
	h.prompt("/goal ship it")
	if got := strings.Join(h.kinds(), ","); got != "kickoff,continue,continue,wrapup" {
		t.Fatalf("turns = %s", got)
	}
	if !strings.Contains(h.prompts[3], "Do not start new work") {
		t.Fatalf("wrap-up text = %q", h.prompts[3])
	}
	goal := h.st().GetGoal()
	if goal.Status != session.GoalLimited || goal.Continuations != 2 || !strings.Contains(goal.StatusReason, "continuations") {
		t.Fatalf("goal = %+v", goal)
	}
}

func TestGoalTokenBudgetEndsTheGoal(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.TokenBudget = intp(100)
	h.steps[0] = func(_ context.Context, st *session.State, snd acp.UpdateSender, n int) (string, error) {
		_ = snd.SendSessionUpdate(st.ID, acp.TokenUsageUpdate{SessionUpdate: acp.UpdateTypeTokenUsage, InputTokens: 150, CachedInputTokens: 20, OutputTokens: 30})
		return toolTurn(st, snd, `{"path":"a"}`, "ok"), nil
	}
	h.verdict(notMet("open"))
	h.prompt("/goal ship it")
	goal := h.st().GetGoal()
	if got := strings.Join(h.kinds(), ","); got != "kickoff,wrapup" {
		t.Fatalf("turns = %s", got)
	}
	if goal.Status != session.GoalLimited || goal.TokensUsed != 160 || !strings.Contains(goal.StatusReason, "token budget") {
		t.Fatalf("goal = %+v", goal)
	}
}

func TestGoalBlocksWhenTheWorkNeedsTheOperator(t *testing.T) {
	for _, verdict := range []session.GoalVerdict{session.GoalVerdictNeedsUser, session.GoalVerdictImpossible} {
		t.Run(string(verdict), func(t *testing.T) {
			h := newGoalHarness(t)
			h.verdict(goalVerdictStep{result: session.GoalCheckResult{Verdict: verdict, Reason: "which database should it use?"}})
			h.prompt("/goal migrate the store")
			goal := h.st().GetGoal()
			if len(h.prompts) != 1 || goal.Status != session.GoalBlocked || goal.StatusReason != "which database should it use?" {
				t.Fatalf("runs=%d goal=%+v", len(h.prompts), goal)
			}
		})
	}
}

func TestGoalBlocksAfterTurnsWithoutProgress(t *testing.T) {
	h := newGoalHarness(t)
	talk := func(_ context.Context, st *session.State, _ acp.UpdateSender, _ int) (string, error) {
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "I will now do it."})
		return string(acp.StopReasonEndTurn), nil
	}
	h.steps[0], h.steps[1] = talk, talk
	h.verdict(notMet("nothing done"))
	h.prompt("/goal do the work")
	goal := h.st().GetGoal()
	if len(h.prompts) != 2 || goal.Status != session.GoalBlocked || !strings.Contains(goal.StatusReason, "no progress") {
		t.Fatalf("runs=%d goal=%+v", len(h.prompts), goal)
	}
}

func TestGoalPausesWhenTheCheckKeepsFailing(t *testing.T) {
	h := newGoalHarness(t)
	h.verdict(goalVerdictStep{err: errors.New("model offline")})
	h.prompt("/goal ship it")
	goal := h.st().GetGoal()
	if len(h.checks) != 2 || goal.Status != session.GoalPaused || !strings.Contains(goal.StatusReason, "model offline") {
		t.Fatalf("checks=%d goal=%+v", len(h.checks), goal)
	}
}

func TestGoalCheckIsRetriedOnceAfterAFailure(t *testing.T) {
	h := newGoalHarness(t)
	h.verdict(goalVerdictStep{err: errors.New("bad json")}, met())
	h.prompt("/goal ship it")
	if goal := h.st().GetGoal(); goal.Status != session.GoalComplete || len(h.checks) != 2 {
		t.Fatalf("checks=%d goal=%+v", len(h.checks), goal)
	}
}

func TestGoalVerifierConfirmsOrReopensAMetVerdict(t *testing.T) {
	t.Run("confirmed", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			h.verifies++
			return session.GoalCheckResult{Verdict: session.GoalVerdictMet, Reason: "read the files, all there"}, nil
		})
		h.prompt("/goal ship it")
		goal := h.st().GetGoal()
		if h.verifies != 1 || goal.Status != session.GoalComplete || goal.LastCheck == nil || !goal.LastCheck.Verified {
			t.Fatalf("verifies=%d goal=%+v", h.verifies, goal)
		}
	})
	t.Run("reopened", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			h.verifies++
			if h.verifies == 1 {
				return session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: "README not updated", Remaining: []string{"update README"}}, nil
			}
			return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
		})
		h.prompt("/goal ship it")
		if got := strings.Join(h.kinds(), ","); got != "kickoff,continue" || !strings.Contains(h.prompts[1], "update README") {
			t.Fatalf("turns=%s continuation=%q", got, h.prompts[1])
		}
		if goal := h.st().GetGoal(); goal.Status != session.GoalComplete || !goal.LastCheck.Verified {
			t.Fatalf("goal = %+v", goal)
		}
	})
	t.Run("verify off", func(t *testing.T) {
		h := newGoalHarness(t)
		off := false
		h.mgr.Cfg().Supervisor.Verify = &off
		h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			h.verifies++
			return session.GoalCheckResult{}, nil
		})
		h.prompt("/goal ship it")
		if h.verifies != 0 || h.st().GetGoal().Status != session.GoalComplete {
			t.Fatalf("verifies=%d goal=%+v", h.verifies, h.st().GetGoal())
		}
	})
}

func TestGoalInterruptsAStalledTurnAndRecovers(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.StallSeconds = intp(1)
	h.steps[0] = func(ctx context.Context, _ *session.State, _ acp.UpdateSender, _ int) (string, error) {
		<-ctx.Done()
		return string(acp.StopReasonCancelled), ctx.Err()
	}
	h.verdict(notMet("nothing done yet", "write the code"), met())
	h.prompt("/goal ship it")
	if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" {
		t.Fatalf("turns = %s", got)
	}
	if !strings.Contains(h.prompts[1], "stalled") || h.st().GetGoal().Status != session.GoalComplete {
		t.Fatalf("recovery=%q goal=%+v", h.prompts[1], h.st().GetGoal())
	}
}

func TestGoalStallTimerWaitsForARunningTool(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.StallSeconds = intp(1)
	h.steps[0] = func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "slow", Name: "run_command", InputJSON: `{"command":"make"}`}}})
		_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: "slow", Status: "in_progress"})
		select {
		case <-ctx.Done():
			return string(acp.StopReasonCancelled), ctx.Err()
		case <-time.After(1600 * time.Millisecond):
		}
		_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: "slow", Status: "completed"})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "built"})
		return string(acp.StopReasonEndTurn), nil
	}
	h.prompt("/goal build it")
	if got := strings.Join(h.kinds(), ","); got != "kickoff" {
		t.Fatalf("a running tool was cut as a stall: turns = %s", got)
	}
}

func TestGoalStallTimerPausesForPermission(t *testing.T) {
	sender := &holdingPermissionSender{started: make(chan struct{}), release: make(chan struct{})}
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.StallSeconds = intp(1)
	h.st().SetGoal(mustGoal(t, "finish the task"))
	h.steps[0] = func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		if _, err := snd.RequestPermission(ctx, acp.PermissionRequestParams{}); err != nil {
			return string(acp.StopReasonCancelled), err
		}
		return toolTurn(st, snd, `{"path":"x"}`, "ok"), nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
			SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "start"}},
		}, sender, nil)
		done <- err
	}()
	<-sender.started
	select {
	case err := <-done:
		t.Fatalf("permission wait was interrupted early: %v", err)
	case <-time.After(1300 * time.Millisecond):
	}
	close(sender.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.kinds(), ","); got != "user" {
		t.Fatalf("turns = %s", got)
	}
}

func repeatTurn(args, result string, times int) goalStep {
	return func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		for i := 0; i < times; i++ {
			id := fmt.Sprintf("loop_%d_%d", time.Now().UnixNano(), i)
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "trying again", ToolCalls: []llm.ToolCall{{ID: id, Name: "edit", InputJSON: args}}})
			_ = snd.SendSessionUpdate(st.ID, acp.ToolCallStatusUpdate{SessionUpdate: acp.UpdateTypeToolCallUpdate, ToolCallID: id, Status: "completed",
				Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: "text", Text: result}}}})
			if ctx.Err() != nil {
				return string(acp.StopReasonCancelled), ctx.Err()
			}
		}
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})
		return string(acp.StopReasonEndTurn), nil
	}
}

func TestGoalCutsARepeatedToolOperation(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.StallSeconds = intp(0)
	// Assistant text between the calls does not hide the loop.
	h.steps[0] = repeatTurn(`{"path":"a.go","old":"x","new":"y"}`, "no match", 5)
	h.verdict(notMet("a.go unchanged", "fix a.go"), met())
	h.prompt("/goal fix a.go")
	if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" || !strings.Contains(h.prompts[1], "repeating") {
		t.Fatalf("turns=%s recovery=%q", got, h.prompts[len(h.prompts)-1])
	}
}

func TestGoalLoopDetectionCountsResultsAndCycles(t *testing.T) {
	t.Run("same call, new results", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.Cfg().Supervisor.StallSeconds = intp(0)
		h.steps[0] = func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
			for i := 0; i < 4; i++ {
				toolTurn(st, snd, `{"command":"go test ./..."}`, fmt.Sprintf("FAIL %d", i))
			}
			return string(acp.StopReasonEndTurn), nil
		}
		h.prompt("/goal fix tests")
		if got := strings.Join(h.kinds(), ","); got != "kickoff" {
			t.Fatalf("progressing results were taken for a loop: %s", got)
		}
	})
	t.Run("alternating pair", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.Cfg().Supervisor.StallSeconds = intp(0)
		h.steps[0] = func(ctx context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
			for i := 0; i < 6; i++ {
				if i%2 == 0 {
					toolTurn(st, snd, `{"path":"a.go","new":"v1"}`, "edited")
				} else {
					toolTurn(st, snd, `{"command":"go test"}`, "FAIL")
				}
				if ctx.Err() != nil {
					return string(acp.StopReasonCancelled), ctx.Err()
				}
			}
			return string(acp.StopReasonEndTurn), nil
		}
		h.verdict(notMet("tests fail", "fix the test"), met())
		h.prompt("/goal fix tests")
		if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" {
			t.Fatalf("an edit-test cycle with the same results was not cut: %s", got)
		}
	})
}

func TestGoalStuckTurnIsCheckedBeforeARecovery(t *testing.T) {
	stuck := func(context.Context, *session.State, acp.UpdateSender, int) (string, error) {
		return string(acp.StopReasonRefused), &session.LoopStopError{Msg: "stopped: the model kept requesting the same run_command call with identical arguments"}
	}
	t.Run("needs the operator", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = stuck
		h.verdict(goalVerdictStep{result: session.GoalCheckResult{Verdict: session.GoalVerdictNeedsUser, Reason: "which registry, and with what credentials?"}})
		res := h.prompt("/goal upload the build to the registry")
		goal := h.st().GetGoal()
		if len(h.prompts) != 1 || goal.Status != session.GoalBlocked || goal.StatusReason != "which registry, and with what credentials?" || res.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("runs=%d res=%+v goal=%+v", len(h.prompts), res, goal)
		}
	})
	t.Run("work left", func(t *testing.T) {
		h := newGoalHarness(t)
		h.steps[0] = stuck
		h.verdict(notMet("the upload never ran", "find the upload script"), met())
		h.prompt("/goal upload the build")
		if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" {
			t.Fatalf("turns = %s", got)
		}
		rec := h.prompts[1]
		if !strings.Contains(rec, "kept requesting the same run_command call") || !strings.Contains(rec, "find the upload script") || !strings.Contains(rec, "ask them plainly") {
			t.Fatalf("recovery text = %q", rec)
		}
	})
	t.Run("recoveries run out", func(t *testing.T) {
		h := newGoalHarness(t)
		h.mgr.Cfg().Supervisor.MaxNudges = intp(1)
		h.steps[0], h.steps[1] = stuck, stuck
		h.verdict(notMet("still stuck"))
		h.prompt("/goal upload the build")
		goal := h.st().GetGoal()
		if strings.Join(h.kinds(), ",") != "kickoff,recover" || goal.Status != session.GoalBlocked ||
			!strings.HasPrefix(goal.StatusReason, "still stuck - stuck after 1 recovery turns") || !strings.Contains(goal.StatusReason, "kept requesting") {
			t.Fatalf("turns=%v goal=%+v", h.kinds(), goal)
		}
	})
}

func TestGoalRecoversFromATransientTurnError(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = func(context.Context, *session.State, acp.UpdateSender, int) (string, error) {
		return "", fmt.Errorf("stream: %w", io.ErrUnexpectedEOF)
	}
	h.prompt("/goal ship it")
	if got := strings.Join(h.kinds(), ","); got != "kickoff,recover" {
		t.Fatalf("turns = %s", got)
	}
}

func TestGoalPausesOnAUsageLimit(t *testing.T) {
	h := newGoalHarness(t)
	limit := &llm.QuotaResetError{ResetAt: time.Now().Add(time.Hour), Delay: time.Hour, Cause: errors.New("429")}
	h.steps[0] = func(context.Context, *session.State, acp.UpdateSender, int) (string, error) { return "", limit }
	_, err := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal ship it"}},
	})
	goal := h.st().GetGoal()
	if err == nil || goal.Status != session.GoalPaused || !strings.Contains(goal.StatusReason, "usage limit") {
		t.Fatalf("err=%v goal=%+v", err, goal)
	}
}

func TestGoalCommandsShowPauseResumeAndClear(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.MaxContinuations = intp(1)
	h.verdict(notMet("open", "finish"))
	h.prompt("/goal ship it")
	if goal := h.st().GetGoal(); goal.Status != session.GoalLimited {
		t.Fatalf("goal = %+v", goal)
	}
	runs := len(h.prompts)

	res := h.prompt("/goal")
	if !strings.Contains(res.SettingsNotice, "Goal (limited): ship it") || len(h.prompts) != runs {
		t.Fatalf("show: notice=%q runs=%d", res.SettingsNotice, len(h.prompts))
	}

	h.verdict(met())
	h.prompt("/goal resume")
	goal := h.st().GetGoal()
	if goal.Status != session.GoalComplete || h.markers[len(h.markers)-1].Kind != acp.GoalTurnResume || goal.Continuations != 0 {
		t.Fatalf("resume: kinds=%v goal=%+v", h.kinds(), goal)
	}

	h.st().SetGoal(mustGoal(t, "second goal"))
	res = h.prompt("/goal pause")
	if h.st().GetGoal().Status != session.GoalPaused || !strings.Contains(res.SettingsNotice, "paused") {
		t.Fatalf("pause: notice=%q goal=%+v", res.SettingsNotice, h.st().GetGoal())
	}
	runs, checks := len(h.prompts), len(h.checks)
	h.prompt("an ordinary question")
	if len(h.prompts) != runs+1 || len(h.checks) != checks {
		t.Fatalf("a paused goal was checked: runs=%d checks=%d", len(h.prompts), len(h.checks))
	}
	res = h.prompt("/goal clear")
	if h.st().GetGoal().Set() || !strings.Contains(res.SettingsNotice, "Goal cleared: second goal") {
		t.Fatalf("clear: notice=%q goal=%+v", res.SettingsNotice, h.st().GetGoal())
	}
}

func TestGoalCommandIsRefusedInARestrictedTurn(t *testing.T) {
	h := newGoalHarness(t)
	res, err := h.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal take over"}},
	}, nil, &session.PromptRunOpts{Restriction: &session.TurnRestriction{}})
	if err != nil {
		t.Fatal(err)
	}
	if h.st().GetGoal().Set() || len(h.prompts) != 0 || !strings.Contains(res.SettingsNotice, "admins") {
		t.Fatalf("notice=%q goal=%+v runs=%d", res.SettingsNotice, h.st().GetGoal(), len(h.prompts))
	}
}

func TestGoalQueuedClearPreemptsTheContinuation(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		if _, err := st.EnqueueMessage("/goal clear"); err != nil {
			t.Fatal(err)
		}
		return toolTurn(st, snd, `{"path":"a"}`, "ok"), nil
	}
	h.verdict(notMet("unfinished"))
	h.prompt("/goal ship it")
	if len(h.checks) != 0 || h.st().GetGoal().Set() || len(h.prompts) != 1 {
		t.Fatalf("checks=%d goal=%+v runs=%d", len(h.checks), h.st().GetGoal(), len(h.prompts))
	}
}

func TestGoalStopDuringTheCheckStartsNothing(t *testing.T) {
	h := newGoalHarness(t)
	checking := make(chan struct{})
	h.mgr.SetGoalJudge(func(ctx context.Context, _ *config.Config, _ *session.State, _ session.GoalCheckRequest) (session.GoalCheckResult, error) {
		close(checking)
		<-ctx.Done()
		return session.GoalCheckResult{}, ctx.Err()
	})
	done := make(chan *acp.SessionPromptResult, 1)
	go func() {
		res, _ := h.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: h.sid, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/goal ship it"}},
		})
		done <- res
	}()
	<-checking
	h.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: h.sid})
	select {
	case res := <-done:
		if res == nil || res.StopReason != acp.StopReasonCancelled || len(h.prompts) != 1 || h.st().GetGoal().Status != session.GoalActive {
			t.Fatalf("Stop started more work or changed the goal: res=%+v runs=%d goal=%+v", res, len(h.prompts), h.st().GetGoal())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the supervisor did not stop after Stop")
	}
}

type heldTask struct {
	once    sync.Once
	release chan struct{}
}

func (h *heldTask) Wait() (int, error) { <-h.release; return 0, nil }
func (h *heldTask) Stop(time.Duration) error {
	h.once.Do(func() { close(h.release) })
	return nil
}
func (*heldTask) PID() int                    { return 0 }
func (*heldTask) ProcessStartedAt() time.Time { return time.Time{} }

func TestGoalCheckWaitsOnlyForWorkThatWakesTheSession(t *testing.T) {
	launch := func(t *testing.T, h *goalHarness, notify bool) {
		t.Helper()
		pool := bgtask.Default()
		held := &heldTask{release: make(chan struct{})}
		started := make(chan struct{})
		if _, err := pool.Launch(bgtask.Spec{SessionID: h.sid, Kind: bgtask.KindCommand, Label: "build", Command: "test-build", NoTimeout: true, NotifyOnFinish: notify},
			func(string, io.Writer) (bgtask.Handle, error) { close(started); return held, nil }); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { pool.StopSession(h.sid) })
		<-started
	}
	// The process has a waker, as coddy serve, the console and coddy acp do.
	bgtask.Default().SubscribeKeyed(bgtask.WakeWatcherKey, func(bgtask.Snapshot) {})
	t.Cleanup(func() { bgtask.Default().SubscribeKeyed(bgtask.WakeWatcherKey, nil) })

	t.Run("a task that wakes the session defers the check", func(t *testing.T) {
		h := newGoalHarness(t)
		launch(t, h, true)
		h.prompt("/goal wait for the build")
		if len(h.checks) != 0 || h.st().GetGoal().Status != session.GoalActive {
			t.Fatalf("the check ran before the wake: checks=%d goal=%+v", len(h.checks), h.st().GetGoal())
		}
	})
	t.Run("a task that wakes nobody does not", func(t *testing.T) {
		h := newGoalHarness(t)
		launch(t, h, false)
		h.prompt("/goal wait for the build")
		if len(h.checks) != 1 || h.st().GetGoal().Status != session.GoalComplete {
			t.Fatalf("a check that no wake would ever start was deferred: checks=%d goal=%+v", len(h.checks), h.st().GetGoal())
		}
	})
}

func TestSupervisorEnableChecksTheLatestRequestWithoutAGoal(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.Enable = true
	h.verdict(notMet("half done", "finish"), met())
	h.prompt("refactor the parser")
	if len(h.checks) != 2 || h.checks[0].Objective != "refactor the parser" || !h.checks[0].Implicit {
		t.Fatalf("checks = %+v", h.checks)
	}
	if got := strings.Join(h.kinds(), ","); got != "user,continue" || h.st().GetGoal().Set() {
		t.Fatalf("turns=%s goal=%+v", got, h.st().GetGoal())
	}
}

func TestSupervisorIgnoresAFinishedGoalForANewRequest(t *testing.T) {
	h := newGoalHarness(t)
	h.mgr.Cfg().Supervisor.Enable = true
	g := mustGoal(t, "old task")
	g.Status = session.GoalComplete
	h.st().SetGoal(g)
	h.prompt("start")
	if len(h.checks) != 1 || h.checks[0].Objective != "start" {
		t.Fatalf("a finished goal was reused: %+v", h.checks)
	}
}

func TestGoalChangesArePublished(t *testing.T) {
	h := newGoalHarness(t)
	var mu sync.Mutex
	var updates []acp.SessionGoalUpdate
	h.mgr.AddSessionGoalObserver(func(u acp.SessionGoalUpdate) {
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
	})
	h.verdict(notMet("open", "x"), met())
	h.prompt("/goal ship it")
	h.prompt("/goal clear")
	mu.Lock()
	defer mu.Unlock()
	if len(updates) < 4 {
		t.Fatalf("updates = %d", len(updates))
	}
	var last uint64
	for _, u := range updates {
		if u.Version <= last || u.SessionID != h.sid {
			t.Fatalf("versions out of order: %+v", updates)
		}
		last = u.Version
	}
	if updates[0].Goal == nil || updates[0].Goal.Objective != "ship it" || !strings.HasPrefix(updates[0].Notice, "Goal set") {
		t.Fatalf("first update = %+v", updates[0])
	}
	if final := updates[len(updates)-1]; final.Goal != nil || !strings.HasPrefix(final.Notice, "Goal cleared") {
		t.Fatalf("last update = %+v", final)
	}
}

func mustGoal(t *testing.T, objective string) session.GoalState {
	t.Helper()
	g, err := session.NewGoal(objective)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

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

// The operator pauses, clears or replaces the goal from another surface while
// the check runs: the verdict changes nothing and starts nothing.
func TestGoalChangedDuringTheCheckIsLeftAlone(t *testing.T) {
	for name, change := range map[string]func(*goalHarness){
		"paused":   func(h *goalHarness) { _, _ = h.mgr.PauseGoal(h.sid) },
		"cleared":  func(h *goalHarness) { _ = h.mgr.ClearGoal(h.sid) },
		"replaced": func(h *goalHarness) { _, _ = h.mgr.SetGoalObjective(h.sid, "another goal") },
	} {
		for _, verdict := range []goalVerdictStep{met(), notMet("open", "more work")} {
			t.Run(name+"/"+string(verdict.result.Verdict), func(t *testing.T) {
				h := newGoalHarness(t)
				h.mgr.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
					change(h)
					return verdict.result, nil
				})
				h.prompt("/goal ship it")
				goal := h.st().GetGoal()
				if len(h.prompts) != 1 {
					t.Fatalf("a continuation ran for a goal the operator %s: runs=%d", name, len(h.prompts))
				}
				switch name {
				case "paused":
					if goal.Status != session.GoalPaused || goal.Checks != 0 {
						t.Fatalf("goal = %+v", goal)
					}
				case "cleared":
					if goal.Set() {
						t.Fatalf("goal = %+v", goal)
					}
				case "replaced":
					if goal.Objective != "another goal" || goal.Status != session.GoalActive || goal.LastCheck != nil || goal.Checks != 0 {
						t.Fatalf("the new goal inherited the old check: %+v", goal)
					}
				}
			})
		}
	}
}

func TestGoalCommandRulesAboutCompleteGoalsAndModes(t *testing.T) {
	h := newGoalHarness(t)
	h.prompt("/goal ship it")
	if res := h.prompt("/goal pause"); h.st().GetGoal().Status != session.GoalComplete || !strings.Contains(res.SettingsNotice, "complete") {
		t.Fatalf("a complete goal was paused: notice=%q goal=%+v", res.SettingsNotice, h.st().GetGoal())
	}
	g := mustGoal(t, "second")
	g.Status = session.GoalPaused
	h.st().SetGoal(g)
	plan := "plan"
	if _, err := h.mgr.ApplySessionSettings(context.Background(), h.sid, session.SettingsChange{Mode: &plan}); err != nil {
		t.Fatal(err)
	}
	runs := len(h.prompts)
	res := h.prompt("/goal resume")
	if len(h.prompts) != runs || h.st().GetGoal().Status != session.GoalPaused || !strings.Contains(res.SettingsNotice, "agent mode") {
		t.Fatalf("a goal resumed in plan mode: notice=%q runs=%d goal=%+v", res.SettingsNotice, len(h.prompts)-runs, h.st().GetGoal())
	}
}

// A /goal command written while a goal turn runs waits for the boundary, in
// whatever mode it was queued, and is run there before any continuation.
func TestGoalCommandQueuedDuringATurnRunsAtTheBoundary(t *testing.T) {
	h := newGoalHarness(t)
	h.steps[0] = func(_ context.Context, st *session.State, snd acp.UpdateSender, _ int) (string, error) {
		msg, _, err := h.mgr.EnqueueTurnMessageWithMode(st.ID, "/goal pause", session.QueueModeSteer, nil)
		if err != nil {
			t.Fatal(err)
		}
		if msg.Mode != session.QueueModeAfterTurn {
			t.Errorf("a queued /goal command reads into the running step: mode %s", msg.Mode)
		}
		if _, err := h.mgr.SetQueuedTurnMessageMode(st.ID, msg.ID, session.QueueModeSteer); err != session.ErrGoalCommandAfterTurn {
			t.Errorf("switching it to steer: %v", err)
		}
		return toolTurn(st, snd, `{"path":"a"}`, "ok"), nil
	}
	h.verdict(notMet("open"))
	h.prompt("/goal ship it")
	if goal := h.st().GetGoal(); goal.Status != session.GoalPaused || len(h.prompts) != 1 || len(h.checks) != 0 {
		t.Fatalf("goal=%+v runs=%d checks=%d", goal, len(h.prompts), len(h.checks))
	}
}
