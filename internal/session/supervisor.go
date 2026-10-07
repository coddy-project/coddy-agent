package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// GoalCheckRequest is what the supervisor's check reads: the objective, the
// requirements the previous check tracked, and the evidence digest the
// harness built from the goal's turns (goal_digest.go).
type GoalCheckRequest struct {
	Objective string
	// Implicit says there is no goal: the objective is the latest request
	// (supervisor.enable).
	Implicit  bool
	Checklist []GoalItem
	Digest    string
	// Remaining is what the check that ran before this verification found
	// open; empty for the check itself.
	Remaining []string
}

// GoalCheckResult is one verdict.
type GoalCheckResult struct {
	Verdict   GoalVerdict
	Reason    string
	Remaining []string
	Checklist []GoalItem
	// Model is the model that answered.
	Model string
	// unverified is why the verifier could not confirm a met verdict.
	unverified string
}

// GoalJudge checks a finished goal turn against the objective, without tools.
type GoalJudge func(ctx context.Context, cfg *config.Config, st *State, req GoalCheckRequest) (GoalCheckResult, error)

// GoalVerifier confirms a met verdict by reading the workspace: the
// goal-verifier subagent (internal/agent/goal_verifier.go).
type GoalVerifier func(ctx context.Context, st *State, req GoalCheckRequest) (GoalCheckResult, error)

// SetGoalJudge replaces the model-based check. Tests use it to drive the
// continuation policy without a model.
func (m *Manager) SetGoalJudge(judge GoalJudge) { m.goalJudge = judge }

// SetGoalVerifier installs the workspace verification of a met verdict. A
// surface that runs no subagents leaves it unset and the check's verdict
// stands alone.
func (m *Manager) SetGoalVerifier(verify GoalVerifier) { m.goalVerifier = verify }

// LoopStopError is how the agent ends a turn its own loop guard stopped: the
// model kept repeating a call or a passage after every nudge. The supervisor
// takes it for a stuck turn rather than a failure.
type LoopStopError struct{ Msg string }

func (e *LoopStopError) Error() string { return e.Msg }

// goalCheckTimeout bounds one check; goalVerifyTimeout one verification run.
const (
	goalCheckTimeout  = 90 * time.Second
	goalVerifyTimeout = 4 * time.Minute
	// goalIdleTurnsLimit is how many goal turns in a row may end without a
	// single tool call before the goal is blocked: a model that only talks
	// is not making progress the next continuation would change.
	goalIdleTurnsLimit = 2
)

// agentCommandPrompts are the built-ins Agent.Run answers without a model
// turn; there is no work in them for the supervisor to check.
var agentCommandPrompts = []string{"/compact", "/plugin", "/export"}

func isAgentCommandPrompt(prompt []acp.ContentBlock) bool {
	if len(prompt) != 1 || prompt[0].Type != acp.ContentTypeText {
		return false
	}
	text := strings.TrimSpace(prompt[0].Text)
	for _, command := range agentCommandPrompts {
		if text == command || strings.HasPrefix(text, command+" ") || strings.HasPrefix(text, command+"\n") {
			return true
		}
	}
	return false
}

// goalRun is one run of the supervisor over a prompt: the turns it starts,
// the recoveries it has spent and whether the wrap-up already ran.
type goalRun struct {
	m      *Manager
	st     *State
	sender acp.UpdateSender
	cfg    *config.Config
	// persist is true for the session's goal, false for the implicit goal
	// of supervisor.enable, which lives only as long as the run.
	persist  bool
	implicit GoalState
	// goalID is the goal this run works on. The operator may pause, clear
	// or replace it from another surface while a check runs, outside the
	// turn lock: the run then leaves it alone.
	goalID string
	nudges int
	idle   int
}

// stillOurs reports whether the goal is still the one this run started on
// and still active.
func (r *goalRun) stillOurs() bool {
	if !r.persist {
		return true
	}
	g := r.st.GetGoal()
	return g.ID == r.goalID && g.Active()
}

func (r *goalRun) goal() GoalState {
	if r.persist {
		return r.st.GetGoal()
	}
	return r.implicit
}

// update changes this run's goal while it is still ours and active; a goal
// paused, cleared or replaced meanwhile is left as the operator left it.
func (r *goalRun) update(notice string, fn func(*GoalState) bool) bool {
	if r.persist {
		return r.st.UpdateGoal(notice, func(g *GoalState) bool {
			if g.ID != r.goalID || !g.Active() {
				return false
			}
			return fn(g)
		})
	}
	return fn(&r.implicit)
}

// runSupervisedTurn runs one prompt of a top-level agent session. A /goal
// command is answered here, whichever surface or queue it came from; a turn
// of a session with an active goal (or any turn, with supervisor.enable) is
// checked when it ends, and unfinished work gets bounded continuation turns.
func (m *Manager) runSupervisedTurn(ctx context.Context, st *State, prompt []acp.ContentBlock, sender acp.UpdateSender, opts *PromptRunOpts) (string, error) {
	topLevel := st.Subagent() == nil && (opts == nil || !opts.subagentTurn)
	var first *llm.GoalTurn
	if topLevel {
		if cmd := goalCommandOf(prompt); cmd.Kind != GoalCommandNone {
			var notice string
			var err error
			prompt, first, notice, err = m.applyGoalCommand(st, cmd, opts)
			if err != nil {
				notice = err.Error()
			}
			if first == nil {
				m.answerGoalCommand(st, sender, notice)
				return string(acp.StopReasonEndTurn), nil
			}
		}
	}
	if !topLevel || st.GetMode() != string(ModeAgent) || isAgentCommandPrompt(prompt) {
		return m.runner(ctx, st, prompt, sender)
	}
	cfg := m.activeCfg()
	run := &goalRun{m: m, st: st, sender: sender, cfg: cfg, persist: true}
	if !st.GetGoal().Active() {
		woken := opts != nil && opts.BackgroundWake != nil
		objective := strings.TrimSpace(contentBlocksToPlainText(prompt))
		if !cfg.Supervisor.Enable || woken || objective == "" {
			return m.runner(ctx, st, prompt, sender)
		}
		run.persist = false
		run.implicit = GoalState{Objective: objective, Status: GoalActive}
	}
	return run.loop(ctx, prompt, first)
}

// answerGoalCommand tells the operator what a /goal command did, on the
// surface that sent it and in the transcript's notices.
func (m *Manager) answerGoalCommand(st *State, sender acp.UpdateSender, notice string) {
	notice = strings.TrimSpace(notice)
	if notice == "" {
		return
	}
	AnnounceSettingsNotice(sender, st.ID, notice)
	st.AppendUILogNotice(CountUserTurns(st.GetMessages()), notice)
	st.setGoalCommandNotice(notice)
}

// applyGoalCommand carries out a /goal command. A command that starts work
// (set, resume) returns the prompt and the marker of the turn to run; the
// others return only the notice.
func (m *Manager) applyGoalCommand(st *State, cmd GoalCommand, opts *PromptRunOpts) ([]acp.ContentBlock, *llm.GoalTurn, string, error) {
	cfg := m.activeCfg()
	if cmd.Kind != GoalCommandShow && opts != nil && opts.Restriction != nil {
		return nil, nil, "", fmt.Errorf("only the bot's admins can change the session goal")
	}
	if cmd.Err != "" {
		return nil, nil, "", fmt.Errorf("%s. %s", cmd.Err, GoalUsage)
	}
	// A goal is worked on in agent mode: setting or resuming one elsewhere
	// would start its turn unsupervised, in a mode that cannot edit.
	if (cmd.Kind == GoalCommandSet || cmd.Kind == GoalCommandResume) && st.GetMode() != string(ModeAgent) {
		return nil, nil, "", fmt.Errorf("a goal is worked on in agent mode; switch to agent mode first (/agent)")
	}
	switch cmd.Kind {
	case GoalCommandShow:
		return nil, nil, GoalStatusText(st.GetGoal(), cfg.Supervisor.ContinuationLimit(), cfg.Supervisor.EffectiveTokenBudget()), nil
	case GoalCommandClear:
		return nil, nil, m.clearGoal(st), nil
	case GoalCommandPause:
		notice, err := m.pauseGoal(st, "paused by the operator")
		return nil, nil, notice, err
	case GoalCommandResume:
		model, reasoning, err := resolveGoalChecker(cfg, st, cmd, st.GetGoal())
		if err != nil {
			return nil, nil, "", fmt.Errorf("%v. %s", err, GoalUsage)
		}
		if err := m.resumeGoal(st, model, reasoning); err != nil {
			return nil, nil, "", err
		}
		g := st.GetGoal()
		marker := &llm.GoalTurn{Kind: acp.GoalTurnResume, Objective: g.Objective, Limit: cfg.Supervisor.ContinuationLimit()}
		return goalPrompt(goalResumeText(g, cfg)), marker, "", nil
	case GoalCommandSet:
		goal, err := NewGoal(cmd.Objective)
		if err != nil {
			return nil, nil, "", err
		}
		if goal.Model, goal.Reasoning, err = resolveGoalChecker(cfg, st, cmd, GoalState{}); err != nil {
			return nil, nil, "", fmt.Errorf("%v. %s", err, GoalUsage)
		}
		st.SetGoalWithNotice(goal, "Goal set: "+goal.Objective)
		marker := &llm.GoalTurn{Kind: acp.GoalTurnKickoff, Objective: goal.Objective, Limit: cfg.Supervisor.ContinuationLimit()}
		return goalPrompt(goalKickoffText(goal, cfg)), marker, "", nil
	}
	return nil, nil, "", nil
}

func goalPrompt(text string) []acp.ContentBlock {
	return []acp.ContentBlock{{Type: acp.ContentTypeText, Text: text}}
}

// loop runs prompt, checks the result and continues until the check is met,
// the goal leaves active, a person takes over or a budget runs out.
func (r *goalRun) loop(ctx context.Context, prompt []acp.ContentBlock, marker *llm.GoalTurn) (string, error) {
	st, cfg := r.st, r.cfg
	sup := cfg.Supervisor
	r.goalID = r.goal().ID
	for {
		stop, watch, err := r.runStep(ctx, prompt, marker)
		if ctx.Err() != nil || st.IsUserCancelledTurn() || errors.Is(err, context.Canceled) && watch.cause() == "" {
			return stop, err
		}
		if !r.stillOurs() {
			// Cleared, paused or replaced while the turn ran.
			return stop, err
		}
		goal := r.goal()
		if marker != nil && marker.Kind == acp.GoalTurnWrapUp {
			// The last turn of a used-up budget: how it ended still counts -
			// a usage limit pauses the goal, an error is the run's answer.
			_, kind, fatal := r.unfinished(stop, err, watch)
			if kind == turnFatal {
				return stop, fatal
			}
			r.finish(GoalLimited, limitReason(goal, sup), "Goal stopped: "+limitReason(goal, sup))
			return string(acp.StopReasonEndTurn), nil
		}
		// What the operator wrote meanwhile goes first; the supervisor picks
		// the goal up again after it, since that prompt runs supervised too.
		if len(st.QueuedMessages()) > 0 {
			return stop, err
		}

		// A turn that did not finish. A provider that failed on the way gets a
		// recovery turn; a stuck turn - cut by the watchdog, stopped by the
		// agent's loop guard or refused - is checked first, since a worker
		// that loops is often one that needs the operator.
		reason, kind, fatal := r.unfinished(stop, err, watch)
		switch kind {
		case turnFatal:
			return stop, fatal
		case turnTransient, turnStuck:
			var remaining []string
			if kind == turnStuck {
				result, checkErr := r.check(ctx)
				if ctx.Err() != nil || st.IsUserCancelledTurn() {
					return string(acp.StopReasonCancelled), nil
				}
				if len(st.QueuedMessages()) > 0 || !r.stillOurs() {
					return string(acp.StopReasonEndTurn), nil
				}
				if checkErr == nil {
					switch result.Verdict {
					case GoalVerdictMet:
						r.finish(GoalComplete, "", r.completeNotice(goal, result))
						return string(acp.StopReasonEndTurn), nil
					case GoalVerdictNeedsUser, GoalVerdictImpossible:
						question := firstNonEmpty(result.Reason, reason)
						r.finish(GoalBlocked, question, "Goal blocked: "+question)
						return string(acp.StopReasonEndTurn), nil
					}
					remaining = result.Remaining
				}
			}
			if r.nudges >= sup.NudgeLimit() {
				blocked := r.stuckReason(reason)
				r.finish(GoalBlocked, blocked, "Goal blocked: "+blocked)
				return string(acp.StopReasonEndTurn), nil
			}
			r.nudges++
			goal = r.goal()
			marker = &llm.GoalTurn{Kind: acp.GoalTurnRecover, Index: r.nudges, Limit: sup.NudgeLimit(), Objective: goal.Objective, Reason: reason, Remaining: remaining}
			prompt = goalPrompt(goalRecoveryText(goal, reason, remaining, cfg))
			continue
		}
		if awaitedBackgroundWork(st.ID) {
			// The result is not in yet: the wake that reports the work
			// starts the next supervised turn.
			r.publishNotice("Goal check deferred: background work is still running")
			return stop, nil
		}

		result, checkErr := r.check(ctx)
		if ctx.Err() != nil || st.IsUserCancelledTurn() {
			return string(acp.StopReasonCancelled), nil
		}
		if len(st.QueuedMessages()) > 0 || !r.stillOurs() {
			return stop, nil
		}
		if checkErr != nil {
			r.m.log.Warn("goal check failed", "session", st.ID, "error", checkErr)
			r.finish(GoalPaused, "the supervisor check failed: "+checkErr.Error(), "Goal paused: the supervisor check failed")
			return stop, nil
		}
		if watch.toolCalls() == 0 && result.Verdict == GoalVerdictNotMet {
			r.idle++
		} else {
			r.idle = 0
		}
		switch result.Verdict {
		case GoalVerdictMet:
			r.finish(GoalComplete, "", r.completeNotice(goal, result))
			return stop, nil
		case GoalVerdictNeedsUser, GoalVerdictImpossible:
			reason := firstNonEmpty(result.Reason, "the supervisor needs your decision")
			r.finish(GoalBlocked, reason, "Goal blocked: "+reason)
			return stop, nil
		}
		if r.idle >= goalIdleTurnsLimit {
			reason := fmt.Sprintf("no progress: %d turns in a row ended without a tool call", r.idle)
			r.finish(GoalBlocked, reason, "Goal blocked: "+reason)
			return stop, nil
		}
		goal = r.goal()
		if goal.Continuations >= sup.ContinuationLimit() || overBudget(goal, sup) {
			if !r.persist {
				r.st.SetTurnStopNotice(fmt.Sprintf("Supervisor stopped after %d automatic continuations. Remaining: %s", goal.Continuations, strings.Join(result.Remaining, "; ")))
				return string(acp.StopReasonRefused), nil
			}
			marker = &llm.GoalTurn{Kind: acp.GoalTurnWrapUp, Objective: goal.Objective, Reason: limitReason(goal, sup), Remaining: result.Remaining}
			prompt = goalPrompt(goalWrapUpText(goal, result, cfg))
			continue
		}
		used := goal.Continuations + 1
		if !r.update("", func(g *GoalState) bool {
			g.Continuations = used
			return true
		}) {
			return stop, nil
		}
		marker = &llm.GoalTurn{Kind: acp.GoalTurnContinue, Index: used, Limit: sup.ContinuationLimit(), Objective: goal.Objective,
			Reason: result.Reason, Remaining: result.Remaining}
		prompt = goalPrompt(goalContinuationText(r.goal(), result, used, cfg))
	}
}

// runStep runs one turn under the watchdog and charges its time and tokens to
// the goal.
func (r *goalRun) runStep(ctx context.Context, prompt []acp.ContentBlock, marker *llm.GoalTurn) (string, *turnWatch, error) {
	sup := r.cfg.Supervisor
	stepCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watch := newTurnWatch(r.sender, r.st, cancel, r.stallTimeout(), sup.LoopRepeatLimit())
	done := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watch.run(stepCtx, done)
	}()
	r.st.SetTurnGoal(marker)
	started := time.Now()
	stop, err := r.m.runner(stepCtx, r.st, prompt, watch)
	r.st.TakeTurnGoal()
	close(done)
	cancel()
	<-watchDone
	elapsed, tokens := time.Since(started), watch.tokens()
	if r.persist {
		r.st.UpdateGoal("", func(g *GoalState) bool {
			if g.ID != r.goalID {
				return false
			}
			g.ActiveMs += elapsed.Milliseconds()
			g.TokensUsed += tokens
			return true
		})
	}
	return stop, watch, err
}

// turnOutcome is how a turn ended, as the supervisor reads it.
type turnOutcome int

const (
	// turnDone ended with an answer: the result is checked.
	turnDone turnOutcome = iota
	// turnTransient failed on a passing provider error: recover.
	turnTransient
	// turnStuck was cut by the watchdog, stopped by the agent's loop guard
	// or refused: check, then recover.
	turnStuck
	// turnFatal failed for good: the goal is paused and the run ends with
	// the error.
	turnFatal
)

// stallTimeout is how long the turn may stay silent. A model row that does
// not stream sends nothing while it answers, so its own timeout bounds the
// call and the stall timer waits at least that long; with no timeout on the
// row the timer is off, rather than cutting an answer on its way.
func (r *goalRun) stallTimeout() time.Duration {
	stall := r.cfg.Supervisor.StallTimeout()
	if stall <= 0 {
		return 0
	}
	rm, err := r.cfg.ResolveLLM(r.st.EffectiveModelID(r.cfg))
	if err != nil || rm.Stream {
		return stall
	}
	if rm.TimeoutMS <= 0 {
		return 0
	}
	return max(stall, time.Duration(rm.TimeoutMS)*time.Millisecond+30*time.Second)
}

// unfinished classifies how a turn ended; reason says why for every outcome
// but turnDone, fatal is the error of a turnFatal one.
func (r *goalRun) unfinished(stop string, err error, watch *turnWatch) (reason string, kind turnOutcome, fatal error) {
	turn := CountUserTurns(r.st.GetMessages())
	if cause := watch.cause(); cause != "" {
		if err != nil && !errors.Is(err, context.Canceled) {
			r.st.AppendUILogNotice(turn, "Turn interrupted: "+err.Error())
		}
		return "the previous turn was interrupted because it was " + cause, turnStuck, nil
	}
	if err != nil {
		var reset *llm.QuotaResetError
		var loop *LoopStopError
		switch {
		case errors.As(err, &loop):
			// The agent ended the turn itself; the supervisor takes it over.
			r.st.TakeTurnStopNotice()
			r.st.AppendUILogNotice(turn, "Turn stopped: "+loop.Msg)
			return "the previous turn was " + strings.TrimPrefix(loop.Msg, "stopped: ") + " and was stopped", turnStuck, nil
		case errors.As(err, &reset) || llm.UpstreamStatus(err) == 429:
			r.finish(GoalPaused, "usage limit reached: "+err.Error(), "Goal paused: usage limit reached")
			return "", turnFatal, err
		case llm.IsTransientProviderError(err):
			r.st.AppendUILogNotice(turn, "Turn failed: "+err.Error()+"; the supervisor continues the goal")
			return "the previous turn ended early: " + err.Error(), turnTransient, nil
		default:
			r.finish(GoalPaused, "the turn failed: "+err.Error(), "Goal paused: the turn failed")
			return "", turnFatal, err
		}
	}
	switch acp.StopReason(stop) {
	case acp.StopReasonEndTurn, acp.StopReasonCancelled:
		return "", turnDone, nil
	}
	notice := r.st.TakeTurnStopNotice()
	if notice != "" {
		r.st.AppendUILogNotice(turn, notice)
	}
	return firstNonEmpty(notice, "the previous turn stopped ("+stop+")"), turnStuck, nil
}

// check asks the judge, once more on a failure, and records the verdict. A
// met verdict of the session's goal is confirmed by the verifier.
func (r *goalRun) check(ctx context.Context) (GoalCheckResult, error) {
	judge := r.m.goalJudge
	if judge == nil {
		judge = judgeSessionGoal
	}
	goal := r.goal()
	req := GoalCheckRequest{
		Objective: goal.Objective,
		Implicit:  !r.persist,
		Checklist: goal.Checklist,
		Digest:    buildGoalDigest(r.st.GetMessages(), goal, !r.persist),
	}
	// The check and the verification take a while with the turn lock held:
	// a surface that shows notices says what the session is waiting for.
	r.publishNotice("Goal check: the supervisor is reviewing the turn")
	var result GoalCheckResult
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		checkCtx, cancel := context.WithTimeout(ctx, goalCheckTimeout)
		result, err = judge(checkCtx, r.cfg, r.st, req)
		cancel()
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return GoalCheckResult{}, err
	}
	result.Verdict = normalizeVerdict(result.Verdict)
	verified := false
	if result.Verdict == GoalVerdictMet && r.persist && r.cfg.Supervisor.VerifyEnabled() && r.m.goalVerifier != nil {
		vreq := req
		vreq.Checklist = firstChecklist(result.Checklist, req.Checklist)
		r.publishNotice("Goal verification: a read-only subagent is checking the workspace")
		verifyCtx, cancel := context.WithTimeout(ctx, goalVerifyTimeout)
		vres, verr := r.m.goalVerifier(verifyCtx, r.st, vreq)
		cancel()
		switch {
		case ctx.Err() != nil:
			return GoalCheckResult{}, ctx.Err()
		case verr != nil:
			r.m.log.Warn("goal verification failed; the check's verdict stands", "session", r.st.ID, "error", verr)
			result.Reason = strings.TrimSpace(result.Reason + " (not verified: " + verr.Error() + ")")
			result.unverified = verr.Error()
		default:
			vres.Verdict = normalizeVerdict(vres.Verdict)
			if vres.Verdict == GoalVerdictMet {
				verified = true
				if len(vres.Checklist) > 0 {
					result.Checklist = vres.Checklist
				}
				result.Reason = firstNonEmpty(vres.Reason, result.Reason)
			} else {
				if vres.Model == "" {
					vres.Model = result.Model
				}
				result = vres
			}
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r.update("", func(g *GoalState) bool {
		g.Checks++
		g.LastCheck = &GoalCheck{Verdict: result.Verdict, Reason: strings.TrimSpace(result.Reason), Remaining: result.Remaining,
			Verified: verified, At: now, Model: result.Model}
		if len(result.Checklist) > 0 {
			g.Checklist = result.Checklist
		}
		return true
	})
	return result, nil
}

// finish sets the goal's final status for this run. The implicit goal of
// supervisor.enable has no status to keep: a blocked one tells the operator
// through the turn's stop notice.
func (r *goalRun) finish(status GoalStatus, reason, notice string) {
	if !r.persist {
		if status == GoalBlocked || status == GoalPaused {
			r.st.SetTurnStopNotice("Supervisor stopped: " + reason)
		}
		return
	}
	if !r.update(notice, func(g *GoalState) bool {
		g.Status, g.StatusReason = status, strings.TrimSpace(reason)
		return true
	}) {
		return
	}
	if notice != "" {
		r.st.AppendUILogNotice(CountUserTurns(r.st.GetMessages()), notice)
	}
}

// completeNotice says the goal is done, and when the verifier could not
// confirm it, that it was not verified and why.
func (r *goalRun) completeNotice(goal GoalState, result GoalCheckResult) string {
	if c := r.goal().LastCheck; c != nil && !c.Verified && result.unverified != "" {
		return "Goal complete (not verified: " + result.unverified + "): " + goal.Objective
	}
	return "Goal complete: " + goal.Objective
}

func (r *goalRun) publishNotice(notice string) {
	if r.persist {
		r.m.PublishSessionGoal(r.st, notice)
	}
}

// stuckReason is what a goal blocked after its recovery turns ran out tells
// the operator: what the supervisor's last check found open comes first, the
// way the turns failed after it.
func (r *goalRun) stuckReason(cause string) string {
	head := fmt.Sprintf("stuck after %d recovery turns (%s)", r.nudges, strings.TrimPrefix(cause, "the previous turn "))
	if c := r.goal().LastCheck; c != nil && strings.TrimSpace(c.Reason) != "" {
		return strings.TrimSpace(c.Reason) + " - " + head
	}
	return head
}

func overBudget(g GoalState, sup config.Supervisor) bool {
	budget := sup.EffectiveTokenBudget()
	return budget > 0 && g.BudgetTokens() >= budget
}

func limitReason(g GoalState, sup config.Supervisor) string {
	if overBudget(g, sup) {
		return fmt.Sprintf("the token budget is used up (%d of %d)", g.BudgetTokens(), sup.EffectiveTokenBudget())
	}
	return fmt.Sprintf("all %d automatic continuations are used", sup.ContinuationLimit())
}

func normalizeVerdict(v GoalVerdict) GoalVerdict {
	switch GoalVerdict(strings.ToLower(strings.TrimSpace(string(v)))) {
	case GoalVerdictMet:
		return GoalVerdictMet
	case GoalVerdictNeedsUser:
		return GoalVerdictNeedsUser
	case GoalVerdictImpossible:
		return GoalVerdictImpossible
	}
	return GoalVerdictNotMet
}

func firstChecklist(lists ...[]GoalItem) []GoalItem {
	for _, l := range lists {
		if len(l) > 0 {
			return l
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// runningBackgroundWork reports whether the session has a command or a
// subagent run in flight: the stall timer waits for it. A preview server runs
// until stopped and is no result anybody waits for.
func runningBackgroundWork(sessionID string) bool {
	for _, task := range bgtask.Default().List(sessionID) {
		if task.Kind != bgtask.KindServer && (task.Status == bgtask.StatusRunning || task.Status == bgtask.StatusQueued) {
			return true
		}
	}
	return false
}

// awaitedBackgroundWork reports whether the check of a goal turn waits for
// running work: only work whose end wakes the session again, so the deferred
// check is sure to run. A system task (the memory run of every turn), a task
// started without notify_on_finish, or a process with no waker would leave
// the goal active and never checked.
func awaitedBackgroundWork(sessionID string) bool {
	pool := bgtask.Default()
	if !pool.CanWake() {
		return false
	}
	for _, task := range pool.List(sessionID) {
		if task.Kind == bgtask.KindServer || task.SystemTask() || !task.NotifyOnFinish {
			continue
		}
		if task.Status == bgtask.StatusRunning || task.Status == bgtask.StatusQueued {
			return true
		}
	}
	return false
}
