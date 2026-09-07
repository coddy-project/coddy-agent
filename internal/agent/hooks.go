package agent

// Wiring of operator hooks (internal/hooks) into the ReAct loop: the runner is
// built per turn from the configured definition files, and executeToolCall
// consults it before the permission gate and after the tool ran. See
// docs/hooks.md and docs/plans/hooks.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// buildHookRunner loads the hook definitions visible from the session cwd.
// Definitions are re-read on every turn, so an edit or an approval takes
// effect on the next turn without a restart, the way the trust stores do.
func (a *Agent) buildHookRunner(mode string) *hooks.Runner {
	if a.cfg == nil || !a.cfg.Hooks.ResolvedEnabled() {
		return nil
	}
	loader := hooks.NewLoader(a.cfg.Hooks.Files, a.cfg.Hooks.ResolvedProjectTrust()).
		WithStore(hooks.NewTrustStore(a.cfg.Paths.Home))
	loader.Log = a.log
	sources := loader.Load(a.state.GetCWD(), a.cfg.Paths.Home)
	if len(sources) == 0 {
		return nil
	}
	a.noteHookFiles(sources)
	transcript := ""
	if sd := strings.TrimSpace(a.state.GetPersistedSessionDir()); sd != "" {
		transcript = filepath.Join(sd, session.MessagesFileName)
	}
	sess := hooks.Session{
		ID:             a.state.GetID(),
		CWD:            a.state.GetCWD(),
		TranscriptPath: transcript,
		PermissionMode: effectivePermMode(a.state, a.cfg),
		Mode:           mode,
		Model:          a.state.EffectiveModelID(a.cfg),
		Turn:           session.CountUserTurns(a.state.GetMessages()),
	}
	if a.subagent != nil {
		sess.Subagent = &hooks.Subagent{
			Name:            a.subagent.Name,
			ParentSessionID: a.subagent.ParentSessionID,
			Depth:           a.subagent.Depth,
		}
	}
	return &hooks.Runner{
		Sources:        sources,
		Session:        sess,
		Home:           a.cfg.Paths.Home,
		TimeoutSeconds: a.cfg.Hooks.EffectiveDefaultTimeoutSeconds(),
		MaxOutputChars: a.cfg.Hooks.EffectiveMaxOutputChars(),
		Log:            a.log,
	}
}

// noteHookFiles tells the operator, once per live session and file, about a
// project hooks file that is held until approved and about a file that does
// not parse. The note goes to the agent log and to the session's UI log, so
// the SPA shows it in the transcript; there is no in-chat prompt, because
// every sender auto-allows under permission_mode: bypass.
func (a *Agent) noteHookFiles(sources []*hooks.Source) {
	st := sessionStatePtr(a.state)
	turn := session.CountUserTurns(a.state.GetMessages())
	for _, src := range sources {
		var key, msg string
		switch {
		case src.Err != nil:
			key = "invalid:" + src.Path
			msg = fmt.Sprintf("Hooks file %s is invalid and was skipped: %v", src.Display, src.Err)
		case src.Trust == hooks.TrustNeedsApproval:
			key = "held:" + src.Path
			msg = fmt.Sprintf("Hooks file %s is not approved for this workspace, so its hooks are held. "+
				"Review it, then approve it on the machine running coddy with `coddy hooks trust %s --cwd %s` "+
				"or POST /coddy/hooks/trust, or set hooks.project_trust: allow for a checkout you trust.",
				src.Display, src.Display, a.state.GetCWD())
		default:
			continue
		}
		if st == nil || !st.MarkHookNoticeShown(key) {
			continue
		}
		a.log.Warn("hooks file notice", "file", src.Display, "trust", src.Trust, "error", src.Err)
		st.AppendUILogNotice(turn, msg)
	}
}

// hooksFor returns the turn's runner, building it on first use: the HTTP
// permission resume enters executeToolCall without passing through Run. The
// lock covers a background child that finishes while the parent's turn is
// still running its own tool calls; the build itself (file reads, the notice
// rows) happens outside it, and a build that lost the race is discarded.
func (a *Agent) hooksFor(mode string) *hooks.Runner {
	a.hooksMu.Lock()
	if a.hooksLoaded {
		defer a.hooksMu.Unlock()
		return a.hooks
	}
	a.hooksMu.Unlock()

	built := a.buildHookRunner(mode)

	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	if !a.hooksLoaded {
		a.hooks = built
		a.hooksLoaded = true
	}
	return a.hooks
}

// resetHooks drops the cached runner so the next use re-reads the files.
func (a *Agent) resetHooks() {
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	a.hooks = nil
	a.hooksLoaded = false
}

// setHookTurn updates the turn index the cached runner reports in payloads;
// under the lock, because a background child may be reading the runner.
func (a *Agent) setHookTurn(turn int) {
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	if a.hooks != nil {
		a.hooks.Session.Turn = turn
	}
}

// preToolUseOutcome is what executeToolCall needs from the PreToolUse hooks.
type preToolUseOutcome struct {
	blocked bool
	reason  string
	allow   bool
	ask     bool
	context []string
}

// runPreToolUseHooks fires PreToolUse for a call. A rewritten input is
// applied to tc in place. The second result reports whether any hook ran.
func (a *Agent) runPreToolUseHooks(ctx context.Context, tc *llm.ToolCall, mode string) (preToolUseOutcome, bool) {
	r := a.hooksFor(mode)
	if r == nil || !r.HasHandlers(hooks.EventPreToolUse) {
		return preToolUseOutcome{}, false
	}
	out := r.Run(ctx, hooks.ToolEvent(hooks.EventPreToolUse, tc.Name, hooks.ToolInput(tc.InputJSON), tc.ID))
	a.reportHookOutcome(hooks.EventPreToolUse, out)
	res := preToolUseOutcome{context: out.Context}
	if out.UpdatedInput != nil {
		if b, err := json.Marshal(out.UpdatedInput); err == nil {
			tc.InputJSON = string(b)
		}
	}
	if out.Stop {
		a.hookStopReason = stopReasonOr(out.StopReason)
		res.blocked = true
		res.reason = "the turn was stopped by a hook: " + a.hookStopReason
		return res, true
	}
	if out.Blocked() {
		res.blocked = true
		res.reason = out.Reason
		return res, true
	}
	switch out.Decision {
	case hooks.DecisionAllow:
		res.allow = true
	case hooks.DecisionAsk:
		res.ask = true
	}
	return res, true
}

// runPostToolUseHooks fires PostToolUse after a successful call or
// PostToolUseFailure after a failed one and returns the feedback to append
// to the model-facing result ("" when there is none).
func (a *Agent) runPostToolUseHooks(ctx context.Context, tc llm.ToolCall, result string, execErr error, duration time.Duration, mode string) string {
	r := a.hooksFor(mode)
	if r == nil {
		return ""
	}
	event := hooks.EventPostToolUse
	if execErr != nil {
		event = hooks.EventPostToolUseFailure
	}
	if !r.HasHandlers(event) {
		return ""
	}
	ev := hooks.ToolEvent(event, tc.Name, hooks.ToolInput(tc.InputJSON), tc.ID)
	ev.Fields["duration_ms"] = duration.Milliseconds()
	if execErr != nil {
		ev.Fields["error"] = execErr.Error()
	} else {
		ev.Fields["tool_response"] = result
	}
	out := r.Run(ctx, ev)
	a.reportHookOutcome(event, out)
	if out.Stop {
		a.hookStopReason = stopReasonOr(out.StopReason)
	}
	var parts []string
	if out.Blocked() && strings.TrimSpace(out.Reason) != "" {
		parts = append(parts, "Hook feedback: "+out.Reason)
	}
	if len(out.Context) > 0 {
		parts = append(parts, hookContextText(out.Context))
	}
	return strings.Join(parts, "\n")
}

// hookContextText renders additionalContext values for the model.
func hookContextText(values []string) string {
	return "Hook context: " + strings.Join(values, "\n")
}

// joinHookText appends hook text to a tool result.
func joinHookText(result, text string) string {
	if strings.TrimSpace(text) == "" {
		return result
	}
	if strings.TrimSpace(result) == "" {
		return text
	}
	return result + "\n\n" + text
}

func stopReasonOr(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "a hook returned continue: false"
	}
	return reason
}

// reportHookOutcome surfaces what the user should see: systemMessage values
// and non-blocking failures. Both go to the agent log and to the session's
// UI log as notice rows; a failure is recorded once per session and message,
// so a broken hook does not add a row on every tool call.
func (a *Agent) reportHookOutcome(event string, out hooks.Outcome) {
	st := sessionStatePtr(a.state)
	turn := session.CountUserTurns(a.state.GetMessages())
	for _, msg := range out.SystemMessages {
		a.log.Info("hook message", "event", event, "message", msg)
		if st != nil {
			st.AppendUILogNotice(turn, fmt.Sprintf("Hook (%s): %s", event, msg))
		}
	}
	for _, e := range out.Errors {
		a.log.Warn("hook error", "event", event, "error", e)
		if st != nil && st.MarkHookNoticeShown("error:"+e) {
			st.AppendUILogNotice(turn, "Hook error: "+e)
		}
	}
}

// hookNotificationPermissionPrompt is the Notification kind for a pending
// permission prompt.
const hookNotificationPermissionPrompt = "permission_prompt"

// stopHookPrefix marks the follow-up a Stop hook submits as the next user
// message, so the transcript says where it came from.
const stopHookPrefix = "[Stop hook] "

// runUserPromptHooks fires UserPromptSubmit before the prompt becomes a
// message. A rejected prompt is reported with its reason; context the hooks
// hand over is kept for this turn's system prompt.
func (a *Agent) runUserPromptHooks(ctx context.Context, mode, prompt string) (reason string, rejected bool) {
	r := a.hooksFor(mode)
	if r == nil || !r.HasHandlers(hooks.EventUserPromptSubmit) {
		return "", false
	}
	// The prompt is not a message yet; the payload counts it as the turn
	// it is about to start.
	a.setHookTurn(session.CountUserTurns(a.state.GetMessages()) + 1)
	out := r.Run(ctx, hooks.PromptEvent(prompt))
	a.reportHookOutcome(hooks.EventUserPromptSubmit, out)
	switch {
	case out.Stop:
		return stopReasonOr(out.StopReason), true
	case out.Blocked():
		return reasonOr(out.Reason, "rejected by a hook"), true
	}
	a.turnHookContext = strings.Join(out.Context, "\n")
	return "", false
}

// runStopHooks fires Stop when the loop is about to end the turn. It returns
// the follow-up a hook wants submitted as the next user message; continue:
// false lets the turn end as it was going to.
func (a *Agent) runStopHooks(ctx context.Context, mode, lastAssistant string, active bool) (followUp string, again bool) {
	r := a.hooksFor(mode)
	if r == nil || !r.HasHandlers(hooks.EventStop) {
		return "", false
	}
	out := r.Run(ctx, hooks.StopEvent(active, lastAssistant))
	a.reportHookOutcome(hooks.EventStop, out)
	if out.Stop || !out.Blocked() {
		return "", false
	}
	text := reasonOr(out.Reason, "continue")
	if len(out.Context) > 0 {
		text += "\n\n" + hookContextText(out.Context)
	}
	return text, true
}

// runPreCompactHooks fires PreCompact and returns the veto reason when a hook
// blocked the compaction.
func (a *Agent) runPreCompactHooks(ctx context.Context, mode, trigger, instructions string) (string, bool) {
	r := a.hooksFor(mode)
	if r == nil || !r.HasHandlers(hooks.EventPreCompact) {
		return "", false
	}
	out := r.Run(ctx, hooks.CompactEvent(hooks.EventPreCompact, trigger, map[string]interface{}{
		"custom_instructions": instructions,
	}))
	a.reportHookOutcome(hooks.EventPreCompact, out)
	if out.Stop {
		return stopReasonOr(out.StopReason), true
	}
	if out.Blocked() {
		return reasonOr(out.Reason, "vetoed by a hook"), true
	}
	return "", false
}

// postCompactSummaryMax bounds the summary handed to PostCompact hooks.
const postCompactSummaryMax = 4000

// runPostCompactHooks fires PostCompact after a compaction; the outcome is
// observational.
func (a *Agent) runPostCompactHooks(ctx context.Context, mode, trigger, summary string) {
	r := a.hooksFor(mode)
	if r == nil || !r.HasHandlers(hooks.EventPostCompact) {
		return
	}
	if len(summary) > postCompactSummaryMax {
		summary = summary[:postCompactSummaryMax]
	}
	out := r.Run(ctx, hooks.CompactEvent(hooks.EventPostCompact, trigger, map[string]interface{}{
		"summary": summary,
	}))
	a.reportHookOutcome(hooks.EventPostCompact, out)
}

// hookContextBlock renders the context hooks handed over for the system
// prompt: the session-level part from SessionStart (persisted with the
// session) and the turn-level part from UserPromptSubmit.
func (a *Agent) hookContextBlock() string {
	var parts []string
	if st := sessionStatePtr(a.state); st != nil {
		if c := strings.TrimSpace(st.GetHookContext()); c != "" {
			parts = append(parts, c)
		}
	}
	if c := strings.TrimSpace(a.turnHookContext); c != "" {
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		return ""
	}
	return "## Hook context\n\nThe operator's hooks handed over the following context.\n\n" + strings.Join(parts, "\n\n")
}

func reasonOr(reason, fallback string) string {
	if strings.TrimSpace(reason) == "" {
		return fallback
	}
	return reason
}

// subagentReportMax bounds the report handed to SubagentStop hooks.
const subagentReportMax = 4000

// runSubagentStartHooks fires SubagentStart in the parent before a child's
// turn. A block refuses the spawn with its reason; context is returned for
// the child's task prompt.
func (a *Agent) runSubagentStartHooks(ctx context.Context, mode, name, childID, prompt string, background bool) (reason, contextText string, blocked bool) {
	r := a.hooksFor(mode)
	if r == nil || !r.HasHandlers(hooks.EventSubagentStart) {
		return "", "", false
	}
	out := r.Run(ctx, hooks.SubagentStartEvent(name, childID, prompt, background))
	a.reportHookOutcome(hooks.EventSubagentStart, out)
	switch {
	case out.Stop:
		return stopReasonOr(out.StopReason), "", true
	case out.Blocked():
		return reasonOr(out.Reason, "refused by a hook"), "", true
	}
	if len(out.Context) > 0 {
		return "", hookContextText(out.Context), false
	}
	return "", "", false
}

// runSubagentStopHooks fires SubagentStop in the parent after a child's turn
// ended; the outcome is observational.
func (a *Agent) runSubagentStopHooks(ctx context.Context, mode, name, childID, taskID, status, report string, turns int) {
	r := a.hooksFor(mode)
	if r == nil || !r.HasHandlers(hooks.EventSubagentStop) {
		return
	}
	if len(report) > subagentReportMax {
		report = report[:subagentReportMax]
	}
	out := r.Run(ctx, hooks.SubagentStopEvent(name, childID, taskID, status, report, turns))
	a.reportHookOutcome(hooks.EventSubagentStop, out)
}

// runNotificationHooks fires Notification when a permission prompt is about
// to be sent to the client; the outcome is observational.
func (a *Agent) runNotificationHooks(ctx context.Context, mode, kind string, tc llm.ToolCall, message string) {
	r := a.hooksFor(mode)
	if r == nil || !r.HasHandlers(hooks.EventNotification) {
		return
	}
	out := r.Run(ctx, hooks.NotificationEvent(kind, message, map[string]interface{}{
		"tool_name":   tc.Name,
		"tool_input":  hooks.ToolInput(tc.InputJSON),
		"tool_use_id": tc.ID,
	}))
	a.reportHookOutcome(hooks.EventNotification, out)
}

// sameToolArgs reports whether two argument documents describe the same call.
// The bundle stores arguments pretty-printed and a hook answers them compact,
// so a byte comparison would read a formatting difference as a rewrite and
// cancel a resume the very hook that produced the arguments answers again.
func sameToolArgs(a, b string) bool {
	if a == b {
		return true
	}
	av, ok := decodeToolArgs(a)
	if !ok {
		return false
	}
	bv, ok := decodeToolArgs(b)
	if !ok {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// decodeToolArgs decodes one JSON document with its number literals kept
// verbatim: decoded as float64, integers past 2^53 would compare equal and a
// rewrite of one into another would pass as no change.
func decodeToolArgs(s string) (any, bool) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return v, true
}
