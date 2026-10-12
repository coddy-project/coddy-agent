package agent

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// goalVerifierTemplate is the system prompt of the goal verifier.
//
//go:embed goal_verifier.md
var goalVerifierTemplate string

// goalVerifierTools are what the verifier may call: reading only. It runs
// unattended, so nothing it can do needs an approval or changes the
// workspace it checks.
var goalVerifierTools = []string{"read", "glob", "grep", "print_tree"}

const (
	// goalVerifierMaxTurns bounds the verifier's reads.
	goalVerifierMaxTurns = 16
	// goalVerifierTimeoutSeconds is the run's hard limit in the task pool;
	// the supervisor's own wait is a little longer.
	goalVerifierTimeoutSeconds = 210
)

// NewGoalVerifier is the session.GoalVerifier a surface installs on its
// manager (session.Manager.SetGoalVerifier): a met verdict is confirmed by a
// system child, the goal verifier, that opens the workspace with read-only
// tools on the supervisor's model. The run is a task of the session like the
// memory subagent's, so what it checked can be read in the Tasks panel.
func NewGoalVerifier(mgr *session.Manager, log *slog.Logger) session.GoalVerifier {
	return func(ctx context.Context, st *session.State, req session.GoalCheckRequest) (session.GoalCheckResult, error) {
		a := NewAgent(mgr.Cfg(), st, nopUpdateSender{}, log)
		a.SetSubagentRuntime(mgr)
		return a.verifyGoal(ctx, req)
	}
}

// verifyGoal runs the verifier child for the parent session a works in and
// reads its verdict.
func (a *Agent) verifyGoal(ctx context.Context, req session.GoalCheckRequest) (session.GoalCheckResult, error) {
	rt := a.subagentRuntime
	if rt == nil {
		return session.GoalCheckResult{}, fmt.Errorf("this surface runs no subagents")
	}
	st := sessionStatePtr(a.state)
	if st == nil {
		return session.GoalCheckResult{}, fmt.Errorf("no session to verify")
	}
	cfg := a.cfg
	model := session.GoalCheckModel(cfg, st)
	parentID := st.GetID()
	snap, run, err := a.launchChildRun(ctx, rt, childLaunch{
		spec: session.SubagentSpec{
			ID:              session.NewSessionID(),
			ParentSessionID: parentID,
			Name:            session.SubagentKindGoalVerifier,
			CWD:             st.GetCWD(),
			Mode:            string(session.ModeAgent),
			PermissionMode:  effectivePermMode(a.state, cfg),
			SelectedModelID: model,
			// The level /goal --reasoning chose for the check; empty keeps the
			// model's default.
			SelectedReasoning: session.GoalCheckReasoning(cfg, st),
			Title:             "Goal verification",
			Tools:             goalVerifierTools,
			Depth:             a.subagentDepth() + 1,
			MaxTurns:          goalVerifierMaxTurns,
			Kind:              session.SubagentKindGoalVerifier,
			PromptTemplate:    goalVerifierTemplate,
		},
		prompt:         goalVerifierTask(req),
		parentMode:     string(session.ModeAgent),
		label:          "Goal verification",
		timeoutSeconds: goalVerifierTimeoutSeconds,
		system:         true,
	})
	if err != nil {
		return session.GoalCheckResult{}, err
	}
	pool := a.backgroundPool(strings.TrimSpace(st.GetPersistedSessionDir()))
	done, err := pool.Wait(ctx, parentID, snap.ID, time.Duration(goalVerifierTimeoutSeconds+30)*time.Second)
	if err != nil {
		return session.GoalCheckResult{}, err
	}
	if !done.Status.Finished() {
		_, _ = pool.Stop(parentID, snap.ID)
		return session.GoalCheckResult{}, fmt.Errorf("the goal verifier did not finish in time")
	}
	run.mu.Lock()
	report := strings.TrimSpace(run.report)
	run.mu.Unlock()
	if report == "" {
		return session.GoalCheckResult{}, fmt.Errorf("the goal verifier ended without a verdict (%s)", done.Status)
	}
	result, err := session.ParseGoalVerdict(report)
	if err != nil {
		return session.GoalCheckResult{}, err
	}
	result.Model = model
	return result, nil
}

// goalVerifierTask is the verifier's task: the objective, the checklist the
// check found met and the evidence digest it read.
func goalVerifierTask(req session.GoalCheckRequest) string {
	var b strings.Builder
	b.WriteString("Verify that the operator's goal below is achieved in the workspace.\n\n")
	b.WriteString(session.GoalJudgeUserMessage(req))
	b.WriteString("\n\nThe supervisor answered met from that record. Check the workspace and give your own verdict in the JSON format of your instructions.")
	return b.String()
}

// nopUpdateSender is the sender of an agent that only launches a child: it
// shows nothing and asks nobody.
type nopUpdateSender struct{}

func (nopUpdateSender) SendSessionUpdate(string, interface{}) error { return nil }
func (nopUpdateSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	// "deny" is not an outcome permission.Approved knows, so it read as an
	// approval; a refusal is cancelled/reject.
	return &acp.PermissionResult{Outcome: permission.OutcomeCancelled, OptionID: permission.OptionReject}, nil
}
func (nopUpdateSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}
