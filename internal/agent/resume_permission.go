package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
	toolweb "github.com/EvilFreelancer/coddy-agent/internal/tools/web"
)

var errStalePermission = errors.New("permission is stale")

type stalePermissionError struct {
	call llm.ToolCall
}

func (e *stalePermissionError) Error() string {
	return fmt.Sprintf("%s: tool call %s follows a newer message", errStalePermission, e.call.ID)
}

func (e *stalePermissionError) Unwrap() error { return errStalePermission }

// answeredToolCallError reports a resume asked for a call that already carries
// its own result in the transcript (a refusal written when the turn was
// cancelled, a repaired sibling, an execution that finished). The call has an
// outcome, so only the persisted gate is left to lift - nothing may run.
type answeredToolCallError struct {
	call llm.ToolCall
}

func (e *answeredToolCallError) Error() string {
	return fmt.Sprintf("tool call %s already has a result", e.call.ID)
}

// ResumeAfterPermission executes a tool call that was approved via POST /permission after the HTTP
// stream ended or the server restarted, then continues the ReAct loop from persisted messages.
func (a *Agent) ResumeAfterPermission(ctx context.Context, toolCallID string, perm *acp.PermissionResult) (string, error) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return "", fmt.Errorf("toolCallId is required")
	}
	if perm == nil {
		return "", fmt.Errorf("permission result is nil")
	}
	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())
	tc, err := a.findPendingToolCall(toolCallID)
	if err != nil {
		var staleErr *stalePermissionError
		if errors.As(err, &staleErr) {
			settled, settleErr := settleStalePermission(sd, toolCallID, staleErr.call)
			if settled {
				a.sendStalePermissionUpdate(toolCallID)
			}
			if settleErr != nil {
				return string(acp.StopReasonCancelled), settleErr
			}
			return string(acp.StopReasonCancelled), nil
		}
		var answeredErr *answeredToolCallError
		if errors.As(err, &answeredErr) {
			// The call was resolved without this resume; the gate file is
			// residue that would resurrect the answered card after a reload.
			clearMatchingPendingPermission(sd, toolCallID)
			return string(acp.StopReasonCancelled), nil
		}
		return "", err
	}
	mode := a.state.EffectiveMode()
	toolEnv := a.buildToolEnv(mode, sd)
	if !permission.Approved(perm) {
		// A refusal needs nothing from the bundle: the gate is cleared and
		// the denial recorded before anything is read, so an unreadable
		// arguments file cannot keep a refused call pending.
		if sd != "" {
			_ = session.ClearPendingPermission(sd)
		}
		toolResultMsg := llm.Message{
			Role:       llm.RoleTool,
			Content:    permissionDeniedResult(perm),
			ToolCallID: tc.ID,
		}
		a.state.AddMessage(toolResultMsg)
		if sd != "" {
			_ = session.WriteToolCallResult(sd, tc.ID, toolResultMsg.Content)
			_ = session.MarkToolCallFinished(sd, tc.ID, tc.Name, session.ToolKind(tc.Name), "cancelled")
		}
		a.closeUnexecutedPermissionBatch(toolCallID)
		return a.continueReAct(ctx, mode, toolEnv)
	}
	// The history holds the arguments the model produced; the bundle holds
	// the arguments the prompt showed, after any PreToolUse rewrite (it is
	// written when the call starts and again after a rewrite, or the call is
	// cancelled before the prompt). The approval binds to the latter, so
	// those are what runs and what an allow-always grant is recorded
	// against; a bundle that cannot produce them fails closed, and the gate
	// stays for a retry.
	if sd != "" {
		shown, err := session.ReadToolCallArgs(sd, tc.ID)
		if err != nil {
			return "", fmt.Errorf("resume tool call %s: the approved arguments could not be read: %w", tc.ID, err)
		}
		if strings.TrimSpace(shown) == "" && strings.TrimSpace(tc.InputJSON) != "" {
			return "", fmt.Errorf("resume tool call %s: the approved arguments are missing from the bundle", tc.ID)
		}
		tc.InputJSON = shown
	}
	// An http_request prompt showed the request as the configuration built it
	// then, the headers it adds to every request included. When the
	// configuration has moved since, the answer was given for another request:
	// the call goes through the gate again and asks with the one it would send.
	askAgain := tc.Name == toolweb.ToolHTTPRequest && httpPromptMoved(sd, tc, toolEnv)
	// A call the current mode refuses (a pending agent-mode write approved
	// after switching to ask) must not leave an "allow always" grant behind:
	// the grant would outlive the refusal and apply once the mode changes back.
	_, refusedByMode := toolCallRefusedByMode(mode, tc.Name)
	if st := sessionStatePtr(a.state); st != nil && !refusedByMode && !askAgain {
		permission.RecordAllowAlways(st, tc.Name, tc.InputJSON, toolEnv.CWD, perm)
	}
	if !refusedByMode && !askAgain {
		a.switchPermissionModeFromDialog(ctx, toolEnv, perm)
	}
	if sd != "" {
		_ = session.ClearPendingPermission(sd)
	}
	callRules := a.toolCallRules(mode, tc, toolEnv.CWD)
	result, execErr := a.executeToolCall(ctx, tc, toolEnv, mode, a.state.GetID(), !askAgain)
	a.state.AddMessage(a.callResultMessage(tc, result, execErr, callRules))
	a.closeUnexecutedPermissionBatch(toolCallID)
	return a.continueReAct(ctx, mode, toolEnv)
}

// closeUnexecutedPermissionBatch records cancelled results for calls paired
// with a permission-resumed call. A permission reply approves or refuses one
// call, not its siblings; continuing without closing them would send an
// incomplete assistant batch back to a provider.
func (a *Agent) closeUnexecutedPermissionBatch(toolCallID string) {
	msgs := a.state.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		assistant := msgs[i]
		if assistant.Role != llm.RoleAssistant || len(assistant.ToolCalls) == 0 {
			continue
		}

		containsTarget := false
		for _, tc := range assistant.ToolCalls {
			if tc.ID == toolCallID {
				containsTarget = true
				break
			}
		}
		if !containsTarget {
			continue
		}

		batchEnd := i + 1
		answered := make(map[string]struct{})
		for ; batchEnd < len(msgs); batchEnd++ {
			if !isLLMHistoryMessage(msgs[batchEnd]) {
				continue
			}
			if msgs[batchEnd].Role != llm.RoleTool {
				break
			}
			j := batchEnd
			answered[msgs[j].ToolCallID] = struct{}{}
		}
		for j := batchEnd; j < len(msgs); j++ {
			if !isLLMHistoryMessage(msgs[j]) {
				continue
			}
			if msgs[j].Role == llm.RoleTool {
				// A late real result wins over a synthetic cancellation. Leave its
				// ordering untouched for the send-boundary validator to diagnose.
				answered[msgs[j].ToolCallID] = struct{}{}
			}
		}
		targetIndex := -1
		for j, call := range assistant.ToolCalls {
			if call.ID == toolCallID {
				targetIndex = j
				break
			}
		}
		if targetIndex < 0 {
			return
		}
		seen := make(map[string]struct{})
		var siblings []llm.ToolCall
		for j, tc := range assistant.ToolCalls {
			// Calls before the resumed target are not known to be unstarted:
			// they may have run before the permission gate was reached. Only the
			// later suffix is definitely skipped.
			if j <= targetIndex {
				continue
			}
			if strings.TrimSpace(tc.ID) == "" || tc.ID == toolCallID {
				continue
			}
			if _, exists := answered[tc.ID]; exists {
				continue
			}
			if _, duplicate := seen[tc.ID]; duplicate {
				continue
			}
			seen[tc.ID] = struct{}{}
			siblings = append(siblings, tc)
		}
		if len(siblings) > 0 {
			a.recordSkippedToolCalls(&msgs, siblings, permissionBatchSkippedResult)
		}
		return
	}
}

// httpPromptMoved reports whether the http_request prompt persisted for tc
// reads otherwise than the prompt the call would get in env now. The server
// resumes a call only while its prompt is on record, so a record gone or
// holding another call by the time it is read here means the prompt was
// changed under the answer: that asks again too.
func httpPromptMoved(sessionDir string, tc llm.ToolCall, env *tools.Env) bool {
	if strings.TrimSpace(sessionDir) == "" {
		return false
	}
	rec, err := session.ReadPendingPermission(sessionDir)
	if err != nil || rec == nil || strings.TrimSpace(rec.ToolCall.ToolCallID) != tc.ID {
		return true
	}
	var shown strings.Builder
	for _, item := range rec.ToolCall.Content {
		shown.WriteString(item.Content.Text)
	}
	return shown.String() != permission.HTTPRequestPromptBody(env, tc.InputJSON)
}

func (a *Agent) findPendingToolCall(toolCallID string) (llm.ToolCall, error) {
	msgs := a.state.GetMessages()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != llm.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		var pending *llm.ToolCall
		for _, candidate := range m.ToolCalls {
			if strings.TrimSpace(candidate.ID) == toolCallID {
				call := candidate
				pending = &call
				break
			}
		}
		if pending == nil {
			continue
		}
		// Only a result inside this batch's contiguous tool chain answers the
		// call: a same-ID result after an intervening message belongs to a
		// different occurrence or is misplaced, and never makes this one
		// answered.
		j := i + 1
		for ; j < len(msgs); j++ {
			if !isLLMHistoryMessage(msgs[j]) {
				continue
			}
			if msgs[j].Role != llm.RoleTool {
				break
			}
			if strings.TrimSpace(msgs[j].ToolCallID) == toolCallID {
				return llm.ToolCall{}, &answeredToolCallError{call: *pending}
			}
		}
		if j < len(msgs) {
			return llm.ToolCall{}, &stalePermissionError{call: *pending}
		}
		return *pending, nil
	}
	return llm.ToolCall{}, fmt.Errorf("tool call %s not found in session history", toolCallID)
}

// clearMatchingPendingPermission lifts the persisted gate only when it names
// this call: a gate belonging to a different pending call must survive.
func clearMatchingPendingPermission(sessionDir, toolCallID string) {
	rec, err := session.ReadPendingPermission(sessionDir)
	if err != nil || rec == nil {
		return
	}
	if strings.TrimSpace(rec.ToolCall.ToolCallID) != toolCallID {
		return
	}
	_ = session.ClearPendingPermission(sessionDir)
}

func settleStalePermission(sessionDir, toolCallID string, call llm.ToolCall) (bool, error) {
	if strings.TrimSpace(sessionDir) == "" {
		return false, nil
	}
	rec, err := session.ReadPendingPermission(sessionDir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read stale permission %s: %w", toolCallID, err)
	}
	if rec == nil || strings.TrimSpace(rec.ToolCall.ToolCallID) != toolCallID {
		return false, nil
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = strings.TrimSpace(rec.ToolName)
	}
	kind := ""
	if name != "" {
		kind = session.ToolKind(name)
	}
	if err := session.MarkToolCallFinished(sessionDir, toolCallID, name, kind, "cancelled"); err != nil {
		return false, fmt.Errorf("record stale permission %s: %w", toolCallID, err)
	}
	if err := session.ClearPendingPermission(sessionDir); err != nil {
		return false, fmt.Errorf("clear stale permission %s: %w", toolCallID, err)
	}
	return true, nil
}

func (a *Agent) sendStalePermissionUpdate(toolCallID string) {
	if a.server == nil {
		return
	}
	_ = a.server.SendSessionUpdate(a.state.GetID(), acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    toolCallID,
		Status:        "cancelled",
		Content: []acp.ToolCallResultItem{{
			Type:    "content",
			Content: acp.ContentBlock{Type: "text", Text: "not executed: the permission became stale after a newer message"},
		}},
	})
}

func (a *Agent) buildToolEnv(mode, sessionDir string) *tools.Env {
	env := &tools.Env{
		CWD:              a.state.GetCWD(),
		PermissionMode:   effectivePermMode(a.state, a.cfg),
		CommandAllowlist: a.cfg.Tools.CommandAllowlist,
		SessionID:        a.state.GetID(),
		SessionDir:       sessionDir,
		ArchiveActiveMarkdown: func() error {
			if sessionDir == "" {
				return nil
			}
			return session.ArchiveActiveTodo(sessionDir)
		},
		WriteArchivedPlanMarkdown: func(md string) (string, error) {
			if sessionDir == "" {
				return "", nil
			}
			return session.WritePlanArchivedMarkdown(sessionDir, md)
		},
		Sender:  a.server,
		GetPlan: a.state.GetPlan,
		SetPlan: a.state.SetPlan,
		SetSessionMode: func(m string) error {
			a.state.SetMode(strings.TrimSpace(m))
			return nil
		},
		PersistPlanDocument: func(doc plans.Document) {
			a.state.AppendPlanDocument(doc)
		},
		LoadSkillBody:     a.loadSkillBody,
		SSHConnectTimeout: a.cfg.Tools.SSHConnectTimeout,
		ConfigPath:        a.cfg.Paths.ConfigPath,
		ConfigHome:        a.cfg.Paths.Home,
		ConfigCWD:         a.cfg.Paths.CWD,
		OutputLineLimits:  a.cfg.Tools.OutputLimits.AsMap(),
		Background:        a.backgroundPool(sessionDir),
		BackgroundEnabled: a.cfg.Tools.Background.ResolvedEnabled(),
		WebSearch:         webSearchSettings(a.cfg),
		AttachImage:       a.attachToolImage,
		ImageRefusal:      a.toolImageRefusal,
	}
	httpRequestEnv(env, a.cfg)
	a.wireWorkspaceTool(env)
	a.applySubagentEnv(env, mode)
	if a.subagent == nil && a.settings() != nil {
		env.SwitchModel = a.switchModel
	}
	if a.configReloader != nil {
		env.ReloadConfig = func(ctx context.Context) ([]string, error) {
			warnings, err := a.configReloader(ctx)
			if err != nil {
				return warnings, err
			}
			next, err := config.LoadWithPaths(a.cfg.Paths)
			if err != nil {
				return warnings, err
			}
			a.cfg = next
			a.registry = tools.NewRegistryForEnvironment(next, a.environment)
			env.PermissionMode = effectivePermMode(a.state, next)
			env.CommandAllowlist = append([]string(nil), next.Tools.CommandAllowlist...)
			httpRequestEnv(env, next)
			env.SSHConnectTimeout = next.Tools.SSHConnectTimeout
			env.OutputLineLimits = next.Tools.OutputLimits.AsMap()
			env.Background = a.backgroundPool(sessionDir)
			env.BackgroundEnabled = next.Tools.Background.ResolvedEnabled()
			env.WebSearch = webSearchSettings(next)
			return warnings, nil
		}
	}
	return env
}

// backgroundPool returns the process-wide task pool, telling it where this
// session persists so a task started from here mirrors its output into the
// session bundle.
func (a *Agent) backgroundPool(sessionDir string) *bgtask.Pool {
	pool := bgtask.Default()
	pool.SetConfig(backgroundConfig(a.cfg))
	if strings.TrimSpace(sessionDir) != "" {
		pool.SetSessionDir(a.state.GetID(), sessionDir)
	}
	return pool
}

// backgroundConfig translates the operator's YAML into the pool's bounds.
func backgroundConfig(cfg *config.Config) bgtask.Config {
	if cfg == nil {
		return bgtask.Config{}
	}
	resolved := cfg.Tools.Background.Resolved()
	return bgtask.Config{
		MaxConcurrent:         resolved.MaxConcurrent,
		DefaultTimeoutSeconds: resolved.DefaultTimeoutSeconds,
		MaxTimeoutSeconds:     resolved.MaxTimeoutSeconds,
		OutputBufferBytes:     resolved.OutputBufferBytes,
	}
}

// continueReAct runs the ReAct loop using messages already on the session (no
// new user turn): the turn's account of time spent on usage limits carries
// over, since this is the same user turn.
func (a *Agent) continueReAct(ctx context.Context, mode string, toolEnv *tools.Env) (string, error) {
	a.limitLedgerFor()
	userText := lastUserText(a.state.GetMessages())
	contextFiles := extractContextFiles(nil)
	activeSkills := FilterSkillsForContext(a.state.GetSkills(), contextFiles)
	toolDefs := a.currentToolDefinitions(mode)
	transport, err := a.getProvider(mode)
	if err != nil {
		return string(acp.StopReasonRefused), fmt.Errorf("no LLM configured: %w", err)
	}
	sys := a.buildSystemPromptParts(mode, activeSkills, toolDefs)
	messages := a.buildMessages(sys.Content)
	// The continuation is the last part of the turn that ran the plan, unless
	// it stops on another gate of its own (react.go).
	defer a.releasePlanContext()
	// The same check Run makes before its first call: runReActLoop only checks
	// between steps, and the result just approved may be what crossed the
	// threshold.
	if a.maybeAutoCompact(ctx) {
		sys = a.buildSystemPromptParts(mode, activeSkills, toolDefs)
		messages = a.buildMessages(sys.Content)
	}
	maxTurns := a.turnCap()
	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())
	toolEnv.SendDesignPlanUpdate = func(doc plans.Document) {
		tools.SendDesignPlanUpdate(toolEnv, doc)
	}
	stop, err := a.runReActLoop(ctx, mode, sys, messages, toolDefs, transport, toolEnv, sd, userText, contextFiles, activeSkills, maxTurns)
	a.noteStopReason(stop, err, maxTurns)
	return stop, err
}

func lastUserText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			return msgs[i].Content
		}
	}
	return ""
}
