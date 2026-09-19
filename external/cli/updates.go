//go:build cli

package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// applyLoopMessage applies one queued update to the UI tree. Runs on the UI
// goroutine only.
func (a *App) applyLoopMessage(msg updateMsg) {
	// Internal messages first: they carry their own session semantics.
	switch u := msg.update.(type) {
	case uiCall:
		u()
		return
	case turnDone:
		// The turn belongs to the session that started it; clear the running
		// flag even when the UI has already switched sessions (/new, /resume),
		// otherwise the editor would refuse prompts forever.
		if u.sessionID == a.turnSessionID {
			a.turnActive = false
			a.turnStartedAt = time.Time{}
			a.turnTokens = 0
			a.stopSpinner()
			// What the turn left running is what the footer names from here on.
			a.refreshTasks()
			// A remote EOF/error only ends our request. The server may still
			// own a turn and queue (or already have admitted another client).
			if u.sessionID == a.sessionID {
				if a.remoteURL != "" {
					a.refreshRemoteControls()
				} else {
					a.queue.SetRows(nil)
				}
			}
			a.stopUsageResume()
			// A permission or question modal belonging to this turn is now
			// orphaned (the worker already unblocked via ctx cancellation). A
			// background subagent's prompt is not this turn's: its asker is
			// still waiting, so it stays on screen.
			a.dropAbandonedGate()
			if fn := a.pendingSwitch; fn != nil {
				a.pendingSwitch = nil
				fn()
			}
		}
		if u.sessionID != a.sessionID {
			return
		}
		a.curAssistant = nil
		if u.woken && errors.Is(u.err, session.ErrSessionTurnBusy) {
			// The waker asks again once the session is free.
			return
		}
		if u.err != nil {
			a.appendStatus(roleError, "Turn failed: "+u.err.Error())
		} else if u.stop == "cancelled" {
			a.appendStatus(roleDim, "Operation aborted")
		}
		return
	case wakeTurn:
		// A finished background task asks for a turn nobody typed; the
		// console decides on its own goroutine whether one can start now.
		a.startWakeTurn(u)
		return
	case tasksLoaded:
		a.applyTasksLoaded(u)
		return
	case tasksPollDue:
		a.tasksTimer = nil
		a.refreshTasks()
		return
	case taskOutputLoaded:
		a.applyTaskOutputLoaded(u)
		return
	case taskStopped:
		a.applyTaskStopped(u)
		return
	case configReloaded:
		// The process configuration changed for every session, so the header
		// and footer always re-read it; the option set is per session and is
		// adopted only when the committing turn belongs to the visible one.
		if u.opts != nil && (msg.sessionID == "" || a.sessionID == "" || msg.sessionID == a.sessionID) {
			a.configOpts = u.opts
			for _, opt := range u.opts {
				if opt.ID == "model" {
					a.modelID = opt.CurrentValue
				}
			}
		}
		a.refreshFooterModel()
		a.populateHeader()
		// The reload may have switched the active row's usage limits panel
		// (providers[].usage_limits_panel): a cache read brings the line up
		// or takes it down without waiting for the next turn.
		a.refreshUsage(usageProviderOf(a.modelID), false)
		return
	case gateWithdrawn:
		a.dropAbandonedGate()
		return
	case sessionSwitched:
		a.switching = false
		a.adoptSession(u.res.SessionID, u.res.Modes, u.res.ConfigOptions)
		a.populateHeader()
		a.appendStatus(roleDim, "Started new session "+u.res.SessionID)
		return
	case sessionResumed:
		a.switching = false
		a.adoptSession(u.id, u.modes, u.opts)
		a.populateHeader()
		a.refreshFooterModel()
		a.foot.SetSession("", a.modeID)
		return
	case statusErr:
		if u.always || msg.sessionID == "" || a.sessionID == "" || msg.sessionID == a.sessionID {
			a.switching = false
			a.appendStatus(roleError, u.msg)
		}
		return
	case resumeList:
		if msg.sessionID != "" && a.sessionID != "" && msg.sessionID != a.sessionID {
			return
		}
		a.openResumePicker(u.res)
		return
	case localShellOutput:
		u.box.SetOutput(u.text, u.dropped)
		return
	case usageResumeDue:
		// The reset a waiting turn counted down to has passed: the row goes
		// back to the model, and the footer asks the hub for the numbers
		// after the reset instead of keeping the pre-reset snapshot for
		// the whole re-issued call.
		if a.turnActive {
			a.setStatus(newWaitingStatus())
		}
		a.refreshUsage(usageProviderOf(a.modelID), true)
		return
	case usageResetDue:
		// A window's reset passed: one fresh read for the provider that is
		// still active; a switched-away provider gets nothing.
		if u.provider == usageProviderOf(a.modelID) {
			a.refreshUsage(u.provider, u.forced)
		}
		return
	case usageReport:
		if msg.sessionID == "" || a.sessionID == "" || msg.sessionID == a.sessionID {
			a.applyUsageReport(u)
		}
		return
	case localShellDone:
		u.box.SetOutput(u.text, u.dropped)
		u.box.Finish(u.exitCode, u.err)
		// A block from an abandoned transcript still completes, but only the
		// current run may release the console's shell state.
		if a.curShell == u.box {
			a.curShell = nil
			a.shellCmd = nil
			a.shellActive, a.shellStopping = false, false
		}
		return
	}

	// Stale updates from abandoned sessions are dropped.
	if msg.sessionID != "" && a.sessionID != "" && msg.sessionID != a.sessionID {
		return
	}

	switch u := msg.update.(type) {
	case remote.QueueControlUpdate:
		a.queue.ApplyControl(u)
	case remote.ActivityUpdate:
		if u.Revision >= a.remoteActivityRevision {
			a.remoteActivityRevision = u.Revision
			a.remoteTurnActive = u.TurnActive
		}
	case remote.FollowUpdate:
		a.applyFollow(u)
	case remote.CancelUpdate:
		if u.Error != "" {
			a.appendStatus(roleWarning, "Could not stop the turn: "+u.Error+" (escape to retry)")
		} else {
			a.appendStatus(roleDim, "Stop requested")
		}
	case queueResult:
		a.applyQueueResult(u)
	case acp.BackgroundWakeUpdate:
		// A woken turn begins, live or replayed. It shows nothing of its own -
		// the agent carries on where it stopped - but its answer is a block of
		// its own, and /tasks reads the task that woke the agent again.
		a.curAssistant = nil
		a.refreshTasks()
	case acp.MessageChunkUpdate:
		a.applyMessageChunk(u)
	case acp.ToolCallUpdate:
		tb := newToolBox(a.theme, u.ToolCallID, u.Title, u.Kind, a.loadToolResult)
		a.toolBoxes[u.ToolCallID] = tb
		a.lastToolID = u.ToolCallID
		a.chat.AddChild(tb)
		a.curAssistant = nil
		// Title is the plain tool name (internal/agent/react.go); the arguments that name
		// the target arrive on the following in_progress update.
		a.setStatus(newWorkingStatus(statusVerbForTool(u.Title), ""))
	case acp.ToolCallStatusUpdate:
		tb, ok := a.toolBoxes[u.ToolCallID]
		if !ok {
			tb = newToolBox(a.theme, u.ToolCallID, u.ToolCallID, "other", a.loadToolResult)
			a.toolBoxes[u.ToolCallID] = tb
			a.lastToolID = u.ToolCallID
			a.chat.AddChild(tb)
		}
		a.applyToolStatus(tb, u)
		if toolTouchesTasks(tb.name) && (u.Status == "completed" || u.Status == "failed") {
			a.refreshTasks()
		}
	case acp.PlanUpdate:
		entries := make([]planEntry, 0, len(u.Entries))
		for _, e := range u.Entries {
			entries = append(entries, planEntry{content: e.Content, status: e.Status})
		}
		a.plan.SetEntries(entries)
	case acp.TokenUsageUpdate:
		a.foot.AddTokens(u.InputTokens, u.OutputTokens)
	case acp.TurnProgressUpdate:
		a.applyTurnProgress(u)
	case acp.UsageUpdate:
		percent := 0.0
		if u.Size > 0 {
			percent = float64(u.Used) / float64(u.Size) * 100
		}
		a.foot.SetContext(percent, u.Size)
	case acp.ProviderUsageUpdate:
		a.applyProviderUsage(u)
	case acp.ModeUpdate:
		a.modeID = u.CurrentModeID
		a.foot.SetSession("", a.modeID)
	case acp.ConfigOptionUpdate:
		a.configOpts = u.ConfigOptions
		for _, opt := range u.ConfigOptions {
			if opt.ID == "model" {
				a.modelID = opt.CurrentValue
			}
		}
		a.refreshFooterModel()
	case toolFullLoaded:
		delete(a.toolFullPending, u.id)
		if u.ok {
			a.toolFullCache[u.id] = u.text
			if tb, ok := a.toolBoxes[u.id]; ok && a.expanded {
				tb.SetExpanded(true)
			}
		}
	case acp.SessionSettingsUpdate:
		// A change of the session's settings, whoever made it: a command
		// here, the web UI, an editor, the permission dialog, the model's
		// own switch_model. The notice says what changed.
		a.applySettingsSnapshot(u.Settings)
		if notice := strings.TrimSpace(u.Notice); notice != "" {
			a.appendStatus(roleDim, notice)
		}
	case settingsApplied:
		a.applySettingsSnapshot(u.settings)
	case acp.AvailableCommandsUpdate:
		a.refreshServerCommands(u.AvailableCommands)
	case acp.MemoryRunUpdate:
		a.applyMemoryRun(u)
	case acp.MessageQueueUpdate:
		// The queue changes from outside this goroutine - another surface, or
		// the agent reading a batch - so the widget follows the update rather
		// than what this console last sent. The version orders the two streams
		// the same update can arrive down.
		a.queue.Apply(u.Messages, u.Version)
	}
}

// applyMemoryRun renders the memory subagent's run: the status phrase while
// the turn waits for its report, and one dim line when the run settled or
// could not start. The report itself is in the child transcript (the Tasks
// drawer of the web UI opens it; on disk it is the child bundle under the
// session's subagents folder).
func (a *App) applyMemoryRun(u acp.MemoryRunUpdate) {
	switch u.Status {
	case "started":
		if a.turnActive {
			a.setStatus(newWorkingStatus("Working with memory", ""))
		}
		return
	case "finished", "skipped":
		if a.turnActive {
			a.setStatus(newWaitingStatus())
		}
		a.appendStatus(roleDim, memoryRunLine(u))
	}
}

// memoryRunLine is the one line the console prints about a settled memory
// run, or about one that could not start.
func memoryRunLine(u acp.MemoryRunUpdate) string {
	if u.Status == "skipped" {
		return "memory: skipped - " + strings.TrimSpace(u.Reason)
	}
	elapsed := (time.Duration(u.DurationMs) * time.Millisecond).Round(100 * time.Millisecond)
	switch {
	case u.Delivered:
		return fmt.Sprintf("memory: recalled in %s (task %s)", elapsed, u.TaskID)
	case u.TaskStatus == "succeeded":
		return fmt.Sprintf("memory: finished in %s, nothing reached this turn (task %s)", elapsed, u.TaskID)
	case strings.TrimSpace(u.Reason) != "":
		return fmt.Sprintf("memory: %s after %s (task %s) - %s", u.TaskStatus, elapsed, u.TaskID, strings.TrimSpace(u.Reason))
	default:
		return fmt.Sprintf("memory: %s after %s (task %s)", u.TaskStatus, elapsed, u.TaskID)
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
			// setStatus keeps the existing start time when the verb repeats, so the
			// counter measures the whole reasoning block rather than one chunk.
			a.setStatus(newModelStatus("Thinking…"))
		default:
			a.curAssistant.AppendText(u.Content.Text)
			// The SPA hides the dots entirely once assistant text streams; a console
			// spinner has nowhere to hide, so it names what is happening instead.
			a.setStatus(newModelStatus("Responding"))
		}
	}
}

func (a *App) applyToolStatus(tb *toolBox, u acp.ToolCallStatusUpdate) {
	// The manager truncates previews server-side and describes the cut in
	// nested meta: {"coddy": {"toolResultPreview": {truncated, totalLines,
	// previewLines}}}. The preview text itself arrives in Content.
	omitted := 0
	total := 0
	if u.Meta != nil {
		if coddy, ok := u.Meta["coddy"].(map[string]interface{}); ok {
			if pv, ok := coddy["toolResultPreview"].(map[string]interface{}); ok {
				totalLines := intFromAny(pv["totalLines"])
				previewLines := intFromAny(pv["previewLines"])
				if totalLines > previewLines && previewLines > 0 {
					total = totalLines
					omitted = totalLines - previewLines
				}
			}
		}
	}
	preview := ""
	switch u.Status {
	case "in_progress":
		// Content carries the raw argument JSON while streaming.
		for _, item := range u.Content {
			if item.Content.Text != "" {
				tb.SetArgs(item.Content.Text)
				a.setStatus(newWorkingStatus(
					statusVerbForTool(tb.name),
					statusTargetFromArgs(tb.name, item.Content.Text),
				))
			}
		}
		tb.SetStatus("in_progress", "", 0, 0)
	case "completed", "failed", "cancelled":
		for _, item := range u.Content {
			if item.Content.Text != "" {
				preview = item.Content.Text
				break
			}
		}
		tb.SetStatus(u.Status, preview, omitted, total)
		if a.turnActive {
			// The step is done; the turn is back to waiting on the model.
			a.setStatus(newWaitingStatus())
		}
	}
}

func intFromAny(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// applyFollow tracks a turn the server started on its own - a background wake -
// that this console follows over --remote (internal/remote/follow.go). While it
// runs the status line works as it does for a turn the operator started: the
// clock and the tokens arrive on the turn's own turn_progress frames. When it
// ends, the transcript closes the turn and a prompt it left on screen, now
// answered or withdrawn, is taken down.
func (a *App) applyFollow(u remote.FollowUpdate) {
	a.curAssistant = nil
	if u.Active {
		if !a.turnActive {
			a.stepStatus = newWaitingStatus()
			a.stepBlocked = ""
			a.turnStartedAt, a.turnTokens = time.Time{}, 0
			a.startSpinner()
		}
		return
	}
	if !a.turnActive {
		a.stopSpinner()
		a.turnStartedAt, a.turnTokens = time.Time{}, 0
	}
	a.dropAbandonedGate()
	a.refreshTasks()
}
