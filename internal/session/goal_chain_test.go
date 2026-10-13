package session_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const nextTask = ".coddy/next-task.md"

// A process with nothing to run a session nobody opened - a local console, a
// local coddy acp - refuses the follow-up file when the goal is set.
func TestGoalChainIsRefusedWithoutAStarter(t *testing.T) {
	h := newGoalHarness(t)
	res := h.prompt("/goal --then-file .coddy/next-task.md ship it")
	if len(h.prompts) != 0 || h.st().GetGoal().Set() || !strings.Contains(res.SettingsNotice, "--then-file needs coddy serve") {
		t.Fatalf("runs=%d goal=%+v notice=%q", len(h.prompts), h.st().GetGoal(), res.SettingsNotice)
	}
}

// supervisor.max_chain_sessions: 0 turns goal chains off: --then-file is
// refused when the goal is set, and a chained goal already stored ends its
// chain when it is met.
func TestGoalChainsTurnedOff(t *testing.T) {
	h := newChainHarness(t)
	h.mgr.Cfg().Supervisor.MaxChainSessions = intp(0)
	res := h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
	if h.st(h.sid).GetGoal().Set() || !strings.Contains(res.SettingsNotice, "goal chains are off (supervisor.max_chain_sessions: 0)") {
		t.Fatalf("goal=%+v notice=%q", h.st(h.sid).GetGoal(), res.SettingsNotice)
	}
	g := mustGoal(t, "ship the fix")
	g.ThenFile, g.Status = nextTask, session.GoalPaused
	h.st(h.sid).SetGoal(g)
	h.writeAt(nextTask, "write the release notes")
	h.prompt(h.sid, "/goal resume")
	first := h.st(h.sid)
	if first.GetGoal().ContinuedIn != "" || h.startCount() != 0 || !h.exists(nextTask) || !hasNotice(first, "Goal chain stopped: goal chains are off") {
		t.Fatalf("goal=%+v starts=%d notices=%q", first.GetGoal(), h.startCount(), notices(first))
	}
}

// The chained session runs where the finished one ran, on its model, its
// reasoning level and its permission mode, and its goal is checked by the same
// checker; the operator's remembered permission mode is not touched.
func TestGoalChainInheritsTheSessionSettings(t *testing.T) {
	h := newChainHarness(t)
	levels := []string{"low", "medium", "high"}
	h.mgr.Cfg().Models[1].ReasoningLevels = &levels // p2/gpt-4o-mini
	first := h.st(h.sid)
	model, reasoning, bypass := "p2/gpt-4o-mini", "medium", config.PermModeBypass
	if _, err := h.mgr.ApplySessionSettings(context.Background(), h.sid, session.SettingsChange{Model: &model, Reasoning: &reasoning, PermissionMode: &bypass, SessionOnly: true}); err != nil {
		t.Fatal(err)
	}
	h.writesInFirstTurn(nextTask, "write the release notes")
	h.prompt(h.sid, "/goal --model claude --then-file .coddy/next-task.md ship the fix")
	next := h.st(h.waitStarted())

	if next.GetCWD() != first.GetCWD() || next.GetMode() != string(session.ModeAgent) {
		t.Fatalf("chained session cwd=%q mode=%q", next.GetCWD(), next.GetMode())
	}
	if next.GetSelectedModelID() != model || next.GetSelectedReasoning() != reasoning || next.GetPermissionMode() != bypass {
		t.Fatalf("chained session model=%q reasoning=%q permission=%q", next.GetSelectedModelID(), next.GetSelectedReasoning(), next.GetPermissionMode())
	}
	g := next.GetGoal()
	if g.Model != "p3/claude-3" || g.ThenFile != nextTask || g.ChainIndex != 1 || g.ContinuedFrom != h.sid {
		t.Fatalf("chained goal = %+v", g)
	}
	if mode := h.mgr.DefaultPermissionMode(); mode == bypass {
		t.Fatal("the chain recorded the inherited permission mode as the operator's choice")
	}
	if got := next.ConversationTitle(); got != "write the release notes" {
		t.Fatalf("chained session title = %q, want the objective", got)
	}
}

// Only a goal the supervisor (and the verifier) found met hands over; a goal
// that needs the operator, ran out of budget or paused keeps its file where
// it is and opens nothing.
func TestGoalChainAdvancesOnlyOnAMetGoal(t *testing.T) {
	for name, tc := range map[string]struct {
		judge  session.GoalJudge
		prep   func(h *chainHarness)
		status session.GoalStatus
	}{
		"needs_user": {judge: func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			return session.GoalCheckResult{Verdict: session.GoalVerdictNeedsUser, Reason: "which database?"}, nil
		}, status: session.GoalBlocked},
		"check fails": {judge: func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			return session.GoalCheckResult{}, errors.New("the checking model is down")
		}, status: session.GoalPaused},
		"limited": {judge: func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			return session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: "more to do"}, nil
		}, prep: func(h *chainHarness) { h.mgr.Cfg().Supervisor.MaxContinuations = intp(0) }, status: session.GoalLimited},
	} {
		t.Run(name, func(t *testing.T) {
			h := newChainHarness(t)
			h.mgr.SetGoalJudge(tc.judge)
			if tc.prep != nil {
				tc.prep(h)
			}
			h.writeAt(nextTask, "write the release notes")
			h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
			if g := h.st(h.sid).GetGoal(); g.Status != tc.status || g.ContinuedIn != "" {
				t.Fatalf("goal = %+v, want %s and no chain", g, tc.status)
			}
			if h.startCount() != 0 || !h.exists(nextTask) {
				t.Fatalf("starts=%d, follow-up file present=%v", h.startCount(), h.exists(nextTask))
			}
		})
	}
}

// A goal is not checked in plan mode, so a plan-mode turn never hands over.
func TestGoalChainIsNotAdvancedByAPlanModeTurn(t *testing.T) {
	h := newChainHarness(t)
	g := mustGoal(t, "ship the fix")
	g.ThenFile = nextTask
	h.st(h.sid).SetGoal(g)
	plan := "plan"
	if _, err := h.mgr.ApplySessionSettings(context.Background(), h.sid, session.SettingsChange{Mode: &plan}); err != nil {
		t.Fatal(err)
	}
	h.writeAt(nextTask, "write the release notes")
	h.prompt(h.sid, "how would you ship the fix?")
	if got := h.st(h.sid).GetGoal(); got.Status != session.GoalActive || got.Checks != 0 || h.startCount() != 0 || !h.exists(nextTask) {
		t.Fatalf("goal=%+v starts=%d", got, h.startCount())
	}
}

// A met verdict the verifier could not confirm closes the goal as today, but
// does not hand anything to an unattended session; with verification off the
// supervisor's verdict is enough.
func TestGoalChainNeedsAVerifiedGoal(t *testing.T) {
	t.Run("verifier failed", func(t *testing.T) {
		h := newChainHarness(t)
		h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			return session.GoalCheckResult{}, errors.New("the verifier timed out")
		})
		h.writeAt(nextTask, "write the release notes")
		h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
		first := h.st(h.sid)
		if g := first.GetGoal(); g.Status != session.GoalComplete || g.ContinuedIn != "" || h.startCount() != 0 || !h.exists(nextTask) {
			t.Fatalf("goal=%+v starts=%d", g, h.startCount())
		}
		if !hasNotice(first, "Goal chain stopped: the goal was not verified") {
			t.Fatalf("notices = %q", notices(first))
		}
	})
	t.Run("verification off", func(t *testing.T) {
		h := newChainHarness(t)
		off := false
		h.mgr.Cfg().Supervisor.Verify = &off
		h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			return session.GoalCheckResult{}, errors.New("must not run")
		})
		h.writeAt(nextTask, "write the release notes")
		h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
		if next := h.waitStarted(); h.st(h.sid).GetGoal().ContinuedIn != next {
			t.Fatalf("the chain did not continue in %s", next)
		}
	})
}

// The operator may clear the goal through REST while the supervisor checks
// it: a met verdict of a goal that is gone hands nothing over.
func TestGoalChainStopsWhenTheGoalChangedDuringTheCheck(t *testing.T) {
	h := newChainHarness(t)
	h.mgr.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		_ = h.mgr.ClearGoal(h.sid)
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
	})
	h.writeAt(nextTask, "write the release notes")
	h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
	if h.st(h.sid).GetGoal().Set() || h.startCount() != 0 || !h.exists(nextTask) {
		t.Fatalf("goal=%+v starts=%d", h.st(h.sid).GetGoal(), h.startCount())
	}
}

// /goal resume keeps the follow-up file, and a chain stops once it has opened
// supervisor.max_chain_sessions sessions (10 by default).
func TestGoalChainResumeAndBound(t *testing.T) {
	for _, tc := range []struct {
		index, limit int
		chain        bool
	}{{index: 3, chain: true}, {index: 10, chain: false}, {index: 2, limit: 3, chain: true}, {index: 3, limit: 3, chain: false}} {
		h := newChainHarness(t)
		want := 10
		if tc.limit > 0 {
			want = tc.limit
			h.mgr.Cfg().Supervisor.MaxChainSessions = intp(tc.limit)
		}
		g := mustGoal(t, "ship the fix")
		g.ThenFile, g.ChainIndex, g.Status = nextTask, tc.index, session.GoalPaused
		h.st(h.sid).SetGoal(g)
		h.writeAt(nextTask, "write the release notes")
		h.prompt(h.sid, "/goal resume")
		first := h.st(h.sid)
		if !tc.chain {
			if first.GetGoal().ContinuedIn != "" || h.startCount() != 0 || !h.exists(nextTask) || !hasNotice(first, fmt.Sprintf("Goal chain stopped: it opened %d sessions", want)) {
				t.Fatalf("index %d: goal=%+v starts=%d notices=%q", tc.index, first.GetGoal(), h.startCount(), notices(first))
			}
			continue
		}
		next := h.st(h.waitStarted())
		if ng := next.GetGoal(); ng.ChainIndex != tc.index+1 || ng.ThenFile != nextTask {
			t.Fatalf("index %d: chained goal = %+v", tc.index, ng)
		}
	}
}

// A follow-up file that is empty, too long or not a regular file ends the
// chain with a notice and stays where it is.
func TestGoalChainRefusesAFileItCannotUse(t *testing.T) {
	for name, tc := range map[string]struct {
		prep   func(h *chainHarness)
		notice string
	}{
		"empty":     {prep: func(h *chainHarness) { h.writeAt(nextTask, " \n\t\n") }, notice: "Goal chain ended: .coddy/next-task.md is empty"},
		"too long":  {prep: func(h *chainHarness) { h.writeAt(nextTask, strings.Repeat("x", 20000)) }, notice: "Goal chain stopped:"},
		"directory": {prep: func(h *chainHarness) { h.writeAt(nextTask+"/inside", "x") }, notice: "is not a regular file"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newChainHarness(t)
			tc.prep(h)
			h.expectRefusedFile(tc.notice)
		})
	}
}

// expectRefusedFile sets a chained goal over the follow-up file the test
// prepared and checks the chain stopped with notice, the file in place, and
// without blocking on it.
func (h *chainHarness) expectRefusedFile(notice string) {
	h.t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		h.t.Fatal("the chain step blocked on the follow-up file")
	}
	first := h.st(h.sid)
	if first.GetGoal().ContinuedIn != "" || h.startCount() != 0 || !h.exists(nextTask) || !hasNotice(first, notice) {
		h.t.Fatalf("goal=%+v starts=%d present=%v notices=%q", first.GetGoal(), h.startCount(), h.exists(nextTask), notices(first))
	}
}

// A session that cannot be created leaves the follow-up file as it was.
func TestGoalChainRestoresTheFileWhenTheSessionCannotStart(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the test cannot write into")
	}
	h := newChainHarness(t)
	h.writeAt(nextTask, "write the release notes")
	h.onRun = func(*session.State, int) {
		// The store refuses a new bundle from now on; the existing one is
		// still written.
		_ = os.Chmod(h.store.Root, 0o555)
	}
	t.Cleanup(func() { _ = os.Chmod(h.store.Root, 0o755) })
	h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
	first := h.st(h.sid)
	if first.GetGoal().ContinuedIn != "" || h.startCount() != 0 || !h.exists(nextTask) || len(h.startedCopies(nextTask)) != 0 {
		t.Fatalf("goal=%+v starts=%d copies=%v", first.GetGoal(), h.startCount(), h.startedCopies(nextTask))
	}
	if !hasNotice(first, "Goal chain stopped:") {
		t.Fatalf("notices = %q", notices(first))
	}
}

// What a watcher writes during the chained first turn is a message of its
// own, never a second kickoff.
func TestGoalChainQueuedMessageIsNotAKickoff(t *testing.T) {
	h := newChainHarness(t)
	h.onRun = func(st *session.State, run int) {
		switch run {
		case 0:
			h.writeAt(nextTask, "write the release notes")
		case 1:
			if _, _, err := h.mgr.EnqueueTurnMessage(st.ID, "and mention the fix"); err != nil {
				t.Errorf("enqueue: %v", err)
			}
		}
	}
	h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
	next := h.waitStarted()
	runs := h.runsIn(next)
	var follow *chainRun
	for i := range runs {
		if runs[i].prompt == "and mention the fix" {
			follow = &runs[i]
		}
	}
	if follow == nil || follow.marker != nil {
		t.Fatalf("chained session runs = %+v, want the queued message without a goal marker", runs)
	}
}

// The chained session's first turn is retried while something else holds its
// turn lock.
func TestGoalChainRetriesABusySession(t *testing.T) {
	h := newChainHarness(t)
	h.mgr.SetGoalChainRetryForTest(20*time.Millisecond, 50*time.Millisecond, 5*time.Second)
	var release func()
	starter := h.start
	h.mgr.SetGoalChainStarter(func(ctx context.Context, params acp.SessionPromptParams, opts *session.PromptRunOpts) error {
		if release == nil {
			_, rel, err := h.mgr.BeginSessionWork(context.Background(), params.SessionID)
			if err != nil {
				t.Errorf("hold the new session: %v", err)
				return err
			}
			release = rel
			time.AfterFunc(150*time.Millisecond, rel)
		}
		return starter(ctx, params, opts)
	})
	h.writesInFirstTurn(nextTask, "write the release notes")
	h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
	var next string
	deadline := time.After(10 * time.Second)
	for {
		select {
		case id := <-h.started:
			next = id
		case <-deadline:
			t.Fatal("the chained turn never ran")
		}
		if runs := h.runsIn(next); len(runs) > 0 {
			if runs[0].marker == nil || runs[0].marker.Kind != acp.GoalTurnKickoff {
				t.Fatalf("first run = %+v", runs[0])
			}
			return
		}
	}
}

// What happened while the first turn waited for its retry is checked again
// under the turn lock: a goal replaced meanwhile, or a person's turn that ran
// there first, drops the kickoff instead of running it a second time or under
// another goal.
func TestGoalChainRetryChecksAgainUnderTheLock(t *testing.T) {
	for name, meanwhile := range map[string]func(h *chainHarness, id string){
		"goal replaced": func(h *chainHarness, id string) {
			if _, err := h.mgr.SetGoalObjective(id, "another goal"); err != nil {
				h.t.Fatal(err)
			}
		},
		"a person's turn ran": func(h *chainHarness, id string) {
			h.st(id).AddMessage(llm.Message{Role: llm.RoleUser, Content: "stop, I will look first"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newChainHarness(t)
			h.mgr.SetGoalChainRetryForTest(10*time.Millisecond, 10*time.Millisecond, 5*time.Second)
			starter := h.start
			calls := 0
			h.mgr.SetGoalChainStarter(func(ctx context.Context, params acp.SessionPromptParams, opts *session.PromptRunOpts) error {
				calls++
				if calls == 1 {
					return session.ErrSessionTurnBusy
				}
				meanwhile(h, params.SessionID)
				return starter(ctx, params, opts)
			})
			h.writesInFirstTurn(nextTask, "write the release notes")
			h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
			next := h.waitStarted()
			if runs := h.runsIn(next); len(runs) != 0 {
				t.Fatalf("the kickoff ran after %s: %+v", name, runs)
			}
		})
	}
}

// Whether the verifier ran is what this check did, not what a configuration
// reloaded after it says.
func TestGoalChainReadsTheVerificationOfItsOwnCheck(t *testing.T) {
	h := newChainHarness(t)
	off := false
	h.mgr.Cfg().Supervisor.Verify = &off
	h.mgr.SetGoalVerifier(func(context.Context, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		return session.GoalCheckResult{}, errors.New("must not run")
	})
	h.mgr.SetGoalJudge(func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
		next := *h.mgr.Cfg()
		on := true
		next.Supervisor.Verify = &on
		h.mgr.ReplaceConfig(&next)
		return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
	})
	h.writeAt(nextTask, "write the release notes")
	h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
	if next := h.waitStarted(); h.st(h.sid).GetGoal().ContinuedIn != next {
		t.Fatalf("the chain did not continue: goal=%+v notices=%q", h.st(h.sid).GetGoal(), notices(h.st(h.sid)))
	}
}

// A session a chain opened keeps the inherited permission mode across a
// restart of the process, like any session switched by hand.
func TestGoalChainPermissionModeSurvivesARestart(t *testing.T) {
	h := newChainHarness(t)
	bypass := config.PermModeBypass
	if _, err := h.mgr.ApplySessionSettings(context.Background(), h.sid, session.SettingsChange{PermissionMode: &bypass, SessionOnly: true}); err != nil {
		t.Fatal(err)
	}
	h.writesInFirstTurn(nextTask, "write the release notes")
	h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
	next := h.waitStarted()

	again := session.NewManager(h.mgr.Cfg(), noopSender{}, noopRunner, slog.Default(), h.cwd, h.store)
	if _, err := again.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: next}); err != nil {
		t.Fatal(err)
	}
	st := again.SessionByID(next)
	if st.GetPermissionMode() != bypass || st.GetGoal().ContinuedFrom != h.sid || st.GetGoal().ThenFile != nextTask {
		t.Fatalf("after a restart: permission=%q goal=%+v", st.GetPermissionMode(), st.GetGoal())
	}
}

// Every goal text of a chained goal tells the worker where the next task
// goes; a goal without a follow-up file says nothing about it.
func TestGoalChainTellsTheWorkerWhereTheNextTaskGoes(t *testing.T) {
	h := newChainHarness(t)
	h.mgr.SetGoalJudge(func() session.GoalJudge {
		n := 0
		return func(context.Context, *config.Config, *session.State, session.GoalCheckRequest) (session.GoalCheckResult, error) {
			n++
			if n == 1 {
				return session.GoalCheckResult{Verdict: session.GoalVerdictNotMet, Reason: "half done", Remaining: []string{"the rest"}}, nil
			}
			return session.GoalCheckResult{Verdict: session.GoalVerdictMet}, nil
		}
	}())
	h.prompt(h.sid, "/goal --then-file .coddy/next-task.md ship the fix")
	runs := h.runsIn(h.sid)
	if len(runs) != 2 {
		t.Fatalf("runs = %+v", runs)
	}
	for _, r := range runs {
		if !strings.Contains(r.prompt, "`.coddy/next-task.md`") {
			t.Errorf("%s turn does not name the follow-up file:\n%s", r.marker.Kind, r.prompt)
		}
	}
	plain := newChainHarness(t)
	plain.prompt(plain.sid, "/goal ship the fix")
	if r := plain.runsIn(plain.sid); strings.Contains(r[0].prompt, "chain") {
		t.Fatalf("a goal without a follow-up file mentions a chain:\n%s", r[0].prompt)
	}
}
