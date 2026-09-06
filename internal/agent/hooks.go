package agent

// Wiring of operator hooks (internal/hooks) into the ReAct loop: the runner is
// built per turn from the configured definition files, and executeToolCall
// consults it before the permission gate and after the tool ran. See
// docs/hooks.md and docs/plans/hooks.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
// permission resume enters executeToolCall without passing through Run.
func (a *Agent) hooksFor(mode string) *hooks.Runner {
	if !a.hooksLoaded {
		a.hooks = a.buildHookRunner(mode)
		a.hooksLoaded = true
	}
	return a.hooks
}

// resetHooks drops the cached runner so the next use re-reads the files.
func (a *Agent) resetHooks() {
	a.hooks = nil
	a.hooksLoaded = false
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
// and non-blocking failures. They go to the agent log; a UI notice row is the
// job of the trust sub-feature.
func (a *Agent) reportHookOutcome(event string, out hooks.Outcome) {
	for _, msg := range out.SystemMessages {
		a.log.Info("hook message", "event", event, "message", msg)
	}
	for _, e := range out.Errors {
		a.log.Warn("hook error", "event", event, "error", e)
	}
}
