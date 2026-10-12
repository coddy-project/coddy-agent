package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
)

const runPlanUserText = "Implement the plan."

// RunPlan switches to agent mode, injects design plan context, and starts an agent turn.
// It does not modify the session todo checklist (SetPlan).
func (m *Manager) RunPlan(ctx context.Context, sessionID, slug string, sender acp.UpdateSender) (*acp.SessionPromptResult, error) {
	if sender == nil {
		sender = m.server
	}
	state := m.getSession(sessionID)
	if state == nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if err := readOnlyRefusal(state, sessionID); err != nil {
		return nil, err
	}
	if err := refuseAskModePlanRun(state, slug); err != nil {
		return nil, err
	}
	// A plan run is a turn: the direct route (HTTP plans) is admitted like a
	// prompt, with the lock, the registration and the cancel. A prompt that
	// delegates to a plan is already admitted and calls runPlanAdmitted.
	turnCtx, finish, err := m.beginTurn(ctx, sessionID, state, turnAdmission{publishUsage: true})
	if err != nil {
		return nil, err
	}
	defer finish()
	return m.runPlanAdmitted(turnCtx, sessionID, slug, state, sender)
}

// refuseAskModePlanRun fails closed for a read-only ask session: a plan run
// switches to agent with the full tool set, so the prompt shortcuts and the
// HTTP route refuse earlier with friendlier codes, and no caller can run a
// plan out of an ask session by mistake. It reads the session's own mode, not
// a turn's: an /ask --once or --count=N detour in an agent session ends with
// the plan run, as it would with a session-wide /agent (runPlanAdmitted).
func refuseAskModePlanRun(state *State, slug string) error {
	if state.GetMode() == string(ModeAsk) {
		return fmt.Errorf("plan %q cannot be run in ask mode: switch to agent mode first", slug)
	}
	return nil
}

// runPlanAdmitted is the body of RunPlan for a turn that beginTurn already
// admitted; ctx is that turn's context.
func (m *Manager) runPlanAdmitted(ctx context.Context, sessionID, slug string, state *State, sender acp.UpdateSender) (result *acp.SessionPromptResult, err error) {
	if sender == nil {
		sender = m.server
	}
	if err := refuseAskModePlanRun(state, slug); err != nil {
		return nil, err
	}
	sd := strings.TrimSpace(state.GetPersistedSessionDir())
	if sd == "" {
		return nil, fmt.Errorf("session has no persisted bundle")
	}
	outcome := ActivityOutcomeSuccess
	defer func() {
		if ctx.Err() != nil && outcome == ActivityOutcomeSuccess {
			outcome = ActivityOutcomeCanceled
		} else if err != nil && outcome == ActivityOutcomeSuccess {
			outcome = ActivityOutcomeFailure
		}
		state.RecordActivityOutcome(outcome)
	}()
	doc, err := plans.Read(sd, slug)
	if err != nil {
		return nil, err
	}
	state.SetPendingPlanContext(plans.RunContextText(doc))
	// Running a plan switches the session to agent mode the way /agent does:
	// it is the operator's last word on the mode, so a detour armed with
	// --once or --count (/plan or /ask), which this very turn may already
	// hold, ends here. Otherwise the turn would carry the plan out in the
	// detour's mode: without the plan text and with every write refused.
	modeOverridden := false
	for _, o := range state.TurnOverrides() {
		if o.Setting == SettingMode {
			modeOverridden = true
		}
	}
	state.SetMode(string(ModeAgent))
	state.ClearTurnOverride(SettingMode)
	if err := sender.SendSessionUpdate(sessionID, acp.ModeUpdate{
		SessionUpdate: acp.UpdateTypeCurrentModeUpdate,
		CurrentModeID: string(ModeAgent),
	}); err != nil {
		m.log.Warn("failed to send mode update", "error", err)
	}
	m.sendConfigOptionUpdate(sessionID, state)
	if modeOverridden {
		m.PublishSessionSettings(sessionID, state, "", "plan_run")
	}

	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: runPlanUserText}}
	cwdAbs, err := filepath.Abs(state.GetCWD())
	if err != nil {
		return nil, fmt.Errorf("session cwd: %w", err)
	}
	hydrated, err := HydratePromptContentBlocks(cwdAbs, prompt)
	if err != nil {
		return nil, err
	}
	MarkTurnRan(ctx)
	stopReason, err := m.runSupervisedTurn(ctx, state, hydrated, sender, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			outcome = ActivityOutcomeCanceled
		} else {
			outcome = ActivityOutcomeFailure
		}
		return nil, err
	}
	return &acp.SessionPromptResult{StopReason: acp.StopReason(stopReason)}, nil
}

// RunPlanSlugFromPromptMeta reads coddy.dev/runPlanSlug from session/prompt _meta.
func RunPlanSlugFromPromptMeta(meta map[string]interface{}) string {
	if meta == nil {
		return ""
	}
	v, _ := meta[plans.MetaRunPlanSlug].(string)
	return strings.TrimSpace(v)
}
