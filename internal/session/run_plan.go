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
	if state.IsSubagentRun() {
		return nil, fmt.Errorf("%w: %s belongs to %s", ErrSubagentReadOnly, sessionID, subagentParentOf(state))
	}
	// A plan run is a turn. Marking it here covers the HTTP route that calls RunPlan
	// directly; when a prompt delegates to RunPlan its session is already marked, and the
	// registry counts turns, so the nested mark changes nothing for that path.
	clearActive := m.markTurnActive(sessionID)
	defer clearActive()
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
