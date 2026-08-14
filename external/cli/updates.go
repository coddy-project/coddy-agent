//go:build cli

package cli

import (
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// applyLoopMessage applies one queued update to the UI tree. Runs on the UI
// goroutine only.
func (a *App) applyLoopMessage(msg updateMsg) {
	// Internal messages first: they carry their own session semantics.
	switch u := msg.update.(type) {
	case turnDone:
		// The turn belongs to the session that started it; clear the running
		// flag even when the UI has already switched sessions (/new, /resume),
		// otherwise the editor would refuse prompts forever.
		if u.sessionID == a.turnSessionID {
			a.turnActive = false
			a.stopSpinner()
		}
		if u.sessionID != a.sessionID {
			return
		}
		a.curAssistant = nil
		if u.err != nil {
			a.appendStatus(roleError, "Turn failed: "+u.err.Error())
		} else if u.stop == "cancelled" {
			a.appendStatus(roleDim, "Operation aborted")
		}
		return
	case sessionSwitched:
		a.adoptSession(u.res.SessionID, u.res.Modes, u.res.ConfigOptions)
		a.populateHeader()
		a.appendStatus(roleDim, "Started new session "+u.res.SessionID)
		return
	case statusErr:
		a.appendStatus(roleError, u.msg)
		return
	case resumeList:
		a.openResumePicker(u.res)
		return
	}

	// Stale updates from abandoned sessions are dropped.
	if msg.sessionID != "" && a.sessionID != "" && msg.sessionID != a.sessionID {
		return
	}

	switch u := msg.update.(type) {
	case acp.MessageChunkUpdate:
		a.applyMessageChunk(u)
	case acp.ToolCallUpdate:
		tb := newToolBox(a.theme, u.ToolCallID, u.Title, u.Kind, a.loadToolResult)
		a.toolBoxes[u.ToolCallID] = tb
		a.lastToolID = u.ToolCallID
		a.chat.AddChild(tb)
		a.curAssistant = nil
	case acp.ToolCallStatusUpdate:
		tb, ok := a.toolBoxes[u.ToolCallID]
		if !ok {
			tb = newToolBox(a.theme, u.ToolCallID, u.ToolCallID, "other", a.loadToolResult)
			a.toolBoxes[u.ToolCallID] = tb
			a.lastToolID = u.ToolCallID
			a.chat.AddChild(tb)
		}
		a.applyToolStatus(tb, u)
	case acp.PlanUpdate:
		entries := make([]planEntry, 0, len(u.Entries))
		for _, e := range u.Entries {
			entries = append(entries, planEntry{content: e.Content, status: e.Status})
		}
		a.plan.SetEntries(entries)
	case acp.TokenUsageUpdate:
		a.foot.AddTokens(u.InputTokens, u.OutputTokens)
	case acp.UsageUpdate:
		percent := 0.0
		if u.Size > 0 {
			percent = float64(u.Used) / float64(u.Size) * 100
		}
		a.foot.SetContext(percent, u.Size)
	case acp.ModeUpdate:
		a.modeID = u.CurrentModeID
		a.foot.SetSession("", a.modeID)
	case acp.ConfigOptionUpdate:
		for _, opt := range u.ConfigOptions {
			if opt.ID == "model" {
				a.modelID = opt.CurrentValue
			}
		}
		a.refreshFooterModel()
	case acp.AvailableCommandsUpdate:
		a.refreshServerCommands(u.AvailableCommands)
	case acp.MemoryPhaseUpdate:
		if u.Status == "started" {
			a.appendStatus(roleDim, "memory: "+u.Phase+"...")
		}
	case acp.MemoryMessageChunkUpdate:
		// Memory copilot deltas render as a dim collapsed stream; v1 keeps
		// only a lightweight trace to avoid transcript noise.
		_ = u
	}
}

func (a *App) applyMessageChunk(u acp.MessageChunkUpdate) {
	switch u.SessionUpdate {
	case "user_message_chunk":
		// Replay path: render as a user block (live submissions echo locally).
		if u.Content.Text != "" {
			a.chat.AddChild(newUserMessage(a.theme, u.Content.Text))
			a.curAssistant = nil
		}
		return
	default: // agent_message_chunk
		if a.curAssistant == nil {
			a.curAssistant = newAssistantMessage(a.theme, a.mdTheme, a.hideThink)
			a.chat.AddChild(a.curAssistant)
		}
		switch u.Content.Type {
		case "reasoning":
			a.curAssistant.AppendThinking(u.Content.Text)
		default:
			a.curAssistant.AppendText(u.Content.Text)
		}
	}
}

func (a *App) applyToolStatus(tb *toolBox, u acp.ToolCallStatusUpdate) {
	preview := ""
	omitted := 0
	total := 0
	if u.Meta != nil {
		if p, ok := u.Meta["coddy.toolResultPreview"].(string); ok {
			preview = p
		}
		if v, ok := u.Meta["coddy.toolResultOmittedLines"].(float64); ok {
			omitted = int(v)
		}
		if v, ok := u.Meta["coddy.toolResultTotalLines"].(float64); ok {
			total = int(v)
		}
	}
	switch u.Status {
	case "in_progress":
		// Content carries the raw argument JSON while streaming.
		for _, item := range u.Content {
			if item.Content.Text != "" {
				tb.SetArgs(item.Content.Text)
			}
		}
		tb.SetStatus("in_progress", "", 0, 0)
	case "completed", "failed", "cancelled":
		if preview == "" {
			for _, item := range u.Content {
				if item.Content.Text != "" {
					preview = item.Content.Text
					break
				}
			}
		}
		tb.SetStatus(u.Status, preview, omitted, total)
	}
}
