package session

import (
	"context"
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
	if state.IsSubagentRun() || IsSubagentSessionID(sessionID) {
		return nil, fmt.Errorf("%w: %s belongs to %s", ErrSubagentReadOnly, sessionID, subagentParentOf(state))
	}
	if err := refuseAskModePlanRun(state, slug); err != nil {
		return nil, err
	}
	// A plan run is a turn: the direct route (HTTP plans) is admitted like a
	// prompt, with the lock, the registration and the cancel. A prompt that
	// delegates to a plan is already admitted and calls runPlanAdmitted.
	turnCtx, finish, err := m.beginTurn(ctx, sessionID, state, false)
	if err != nil {
		return nil, err
	}
	defer finish()
	return m.runPlanAdmitted(turnCtx, sessionID, slug, state, sender)
}

// refuseAskModePlanRun fails closed for a read-only ask session: a plan run
// switches to agent with the full tool set, so the prompt shortcuts and the
// HTTP route refuse earlier with friendlier codes, and no caller can run a
// plan out of an ask session by mistake.
func refuseAskModePlanRun(state *State, slug string) error {
	if state.GetMode() == string(ModeAsk) {
		return fmt.Errorf("plan %q cannot be run in ask mode: switch to agent mode first", slug)
	}
	return nil
}

// runPlanAdmitted is the body of RunPlan for a turn that beginTurn already
// admitted; ctx is that turn's context.
func (m *Manager) runPlanAdmitted(ctx context.Context, sessionID, slug string, state *State, sender acp.UpdateSender) (*acp.SessionPromptResult, error) {
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
	doc, err := plans.Read(sd, slug)
	if err != nil {
		return nil, err
	}
	state.SetPendingPlanContext(plans.RunContextText(doc))
	state.SetMode(string(ModeAgent))
	if err := sender.SendSessionUpdate(sessionID, acp.ModeUpdate{
		SessionUpdate: acp.UpdateTypeCurrentModeUpdate,
		CurrentModeID: string(ModeAgent),
	}); err != nil {
		m.log.Warn("failed to send mode update", "error", err)
	}
	m.sendConfigOptionUpdate(sessionID, state)

	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: runPlanUserText}}
	cwdAbs, err := filepath.Abs(state.GetCWD())
	if err != nil {
		return nil, fmt.Errorf("session cwd: %w", err)
	}
	hydrated, err := HydratePromptContentBlocks(cwdAbs, prompt)
	if err != nil {
		return nil, err
	}
	stopReason, err := m.runner(ctx, state, hydrated, sender)
	if err != nil {
		return nil, err
	}
	state.BumpActivitySeq()
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
