package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Decisions a hook can reach. Tool events use allow, ask and deny; the other
// blocking events use block.
const (
	DecisionAllow = "allow"
	DecisionAsk   = "ask"
	DecisionDeny  = "deny"
	DecisionBlock = "block"
)

const (
	defaultTimeoutSeconds = 60
	defaultMaxOutputChars = 10000
	// captureLimit bounds what is kept of a hook's stdout and stderr.
	captureLimit = 256 << 10
	// terminateGrace is how long a timed-out hook gets between SIGTERM and
	// SIGKILL (or their Windows equivalents).
	terminateGrace = 2 * time.Second
	// waitDelay bounds cmd.Wait once the hook itself has exited but a
	// grandchild keeps the output pipes open.
	waitDelay        = 5 * time.Second
	truncationMarker = "\n[truncated by hooks.max_output_chars]"
)

// Outcome is the merged result of every handler that ran for one event.
type Outcome struct {
	// Decision is the most restrictive decision reached (deny > ask > allow
	// for tool events; block for the others); empty when no hook decided.
	Decision string
	// Reason accompanies a deny, ask or block.
	Reason string
	// UpdatedInput is the rewritten tool input when a PreToolUse hook
	// returned one; nil otherwise.
	UpdatedInput map[string]interface{}
	// Context collects every additionalContext value (and plain stdout on the
	// events that accept it), in run order.
	Context []string
	// SystemMessages collects systemMessage values for the user.
	SystemMessages []string
	// Stop is set by continue: false; StopReason carries its stopReason.
	Stop       bool
	StopReason string
	// Errors lists non-blocking failures (crash, timeout, invalid output) so
	// the caller can surface them.
	Errors []string
	// Ran counts the handlers that were started.
	Ran int
}

// Blocked reports whether the outcome denies a tool call or blocks the event.
func (o Outcome) Blocked() bool {
	return o.Decision == DecisionDeny || o.Decision == DecisionBlock
}

// Runner dispatches events to the hooks of a session.
type Runner struct {
	Sources []*Source
	Session Session
	// Home is exported to hook processes as CODDY_HOME when set.
	Home string
	// TimeoutSeconds bounds a handler that gives no timeout of its own.
	TimeoutSeconds int
	// MaxOutputChars caps every text a hook hands to the model or the user.
	MaxOutputChars int
	Log            *slog.Logger
}

type bound struct {
	source  *Source
	handler Handler
}

func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// HasHandlers reports whether any runnable source defines a runnable handler
// for the event, so callers can skip building a payload when nothing would
// run.
func (r *Runner) HasHandlers(event string) bool {
	for _, src := range r.Sources {
		if !src.Runnable() {
			continue
		}
		for _, g := range src.Definition.Events[event] {
			for _, h := range g.Handlers {
				if h.Unsupported == "" {
					return true
				}
			}
		}
	}
	return false
}

func (r *Runner) matching(ev Event) []bound {
	var out []bound
	for _, src := range r.Sources {
		if !src.Runnable() {
			continue
		}
		for _, g := range src.Definition.Events[ev.Name] {
			if !subjectMatches(ev, g.Matcher) {
				continue
			}
			for _, h := range g.Handlers {
				if h.Unsupported != "" {
					continue
				}
				out = append(out, bound{source: src, handler: h})
			}
		}
	}
	return out
}

// subjectMatches applies the matcher to the event's subject: tool events
// match tool names with aliases, events without a subject ignore the
// matcher, everything else is a plain match on the subject.
func subjectMatches(ev Event, matcher string) bool {
	switch ev.Name {
	case EventPreToolUse, EventPostToolUse, EventPostToolUseFailure:
		return MatchTool(matcher, ev.Subject)
	case EventUserPromptSubmit, EventStop:
		return true
	default:
		return Match(matcher, ev.Subject)
	}
}

// Run executes every matching handler in catalog order and merges their
// answers. Handlers run one after another, each seeing the input as
// rewritten by the previous one; all of them run even after a deny, so an
// audit hook sees every call. Async handlers are started and not awaited.
func (r *Runner) Run(ctx context.Context, ev Event) Outcome {
	var out Outcome
	handlers := r.matching(ev)
	if len(handlers) == 0 {
		return out
	}
	fields := make(map[string]interface{}, len(ev.Fields))
	for k, v := range ev.Fields {
		fields[k] = v
	}
	for _, b := range handlers {
		payload, err := json.Marshal(r.Session.payload(ev.Name, fields))
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("hook in %s: encode payload: %v", b.source.Display, err))
			continue
		}
		out.Ran++
		if b.handler.Async {
			go func(b bound, payload []byte) {
				res := r.exec(context.WithoutCancel(ctx), ev.Name, b.handler, payload)
				if msg := res.failure(b.source.Display); msg != "" {
					r.logger().Warn("async hook failed", "event", ev.Name, "error", msg)
				}
			}(b, payload)
			continue
		}
		res := r.exec(ctx, ev.Name, b.handler, payload)
		r.merge(&out, ev, b, res, fields)
	}
	return out
}

// execResult is what one hook process left behind.
type execResult struct {
	started   bool
	exitCode  int
	stdout    string
	stderr    string
	timedOut  bool
	cancelled bool
	timeout   int
	err       error
}

// failure describes a run that did not complete normally, or "" for one that
// exited on its own.
func (res execResult) failure(label string) string {
	switch {
	case res.err != nil && !res.started:
		return fmt.Sprintf("hook in %s could not start: %v", label, res.err)
	case res.timedOut:
		return fmt.Sprintf("hook in %s timed out after %ds", label, res.timeout)
	case res.err != nil:
		return fmt.Sprintf("hook in %s failed: %v", label, res.err)
	}
	return ""
}

func (r *Runner) exec(ctx context.Context, event string, h Handler, payload []byte) execResult {
	command := h.Command
	if runtime.GOOS == "windows" && strings.TrimSpace(h.CommandWindows) != "" {
		command = h.CommandWindows
	}
	var cmd *exec.Cmd
	if h.ExecForm {
		cmd = exec.Command(command, h.Args...) // #nosec G204 -- the command is an operator-authored hook definition
	} else {
		exe, args := platform.CurrentShell().Command(command)
		cmd = exec.Command(exe, args...) // #nosec G204 -- the command is an operator-authored hook definition
	}
	if dir := strings.TrimSpace(r.Session.CWD); dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			cmd.Dir = dir
		}
	}
	cmd.Env = append(os.Environ(), r.environment(event)...)
	cmd.Stdin = bytes.NewReader(payload)
	stdout := &limitedBuffer{limit: captureLimit}
	stderr := &limitedBuffer{limit: captureLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = waitDelay
	platform.DetachProcessGroup(cmd)

	timeout := h.TimeoutSeconds
	if timeout <= 0 {
		timeout = r.TimeoutSeconds
	}
	if timeout <= 0 {
		timeout = defaultTimeoutSeconds
	}
	res := execResult{timeout: timeout}
	if err := cmd.Start(); err != nil {
		res.err = err
		return res
	}
	res.started = true

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		res.timedOut = true
		waitErr = terminate(cmd, done)
	case <-ctx.Done():
		res.cancelled = true
		waitErr = terminate(cmd, done)
	}
	res.stdout = platform.DecodeOutput(stdout.Bytes())
	res.stderr = platform.DecodeOutput(stderr.Bytes())
	if waitErr != nil && !errors.Is(waitErr, exec.ErrWaitDelay) {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.exitCode = exitErr.ExitCode()
		} else {
			res.err = waitErr
		}
	}
	return res
}

// terminate stops a hook's whole process group and reaps it, falling back to
// a plain kill of the leader when the group refuses to go.
func terminate(cmd *exec.Cmd, done <-chan error) error {
	_ = platform.TerminateProcessGroup(cmd, terminateGrace)
	select {
	case err := <-done:
		return err
	case <-time.After(terminateGrace + waitDelay):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return <-done
	}
}

func (r *Runner) environment(event string) []string {
	env := []string{
		"CODDY_PROJECT_DIR=" + r.Session.CWD,
		"CLAUDE_PROJECT_DIR=" + r.Session.CWD,
		"CODDY_SESSION_ID=" + r.Session.ID,
		"CODDY_HOOK_EVENT=" + event,
	}
	if home := strings.TrimSpace(r.Home); home != "" {
		env = append(env, "CODDY_HOME="+home)
	}
	return env
}

// hookOutput is the JSON a hook may print on stdout. Unknown fields are
// ignored so definitions written for other agents do not fail.
type hookOutput struct {
	Continue      *bool  `json:"continue"`
	StopReason    string `json:"stopReason"`
	SystemMessage string `json:"systemMessage"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason"`
	Specific      *struct {
		HookEventName            string                 `json:"hookEventName"`
		PermissionDecision       string                 `json:"permissionDecision"`
		PermissionDecisionReason string                 `json:"permissionDecisionReason"`
		UpdatedInput             map[string]interface{} `json:"updatedInput"`
		AdditionalContext        string                 `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

func (o hookOutput) reason() string {
	if o.Specific != nil && strings.TrimSpace(o.Specific.PermissionDecisionReason) != "" {
		return strings.TrimSpace(o.Specific.PermissionDecisionReason)
	}
	return strings.TrimSpace(o.Reason)
}

// looksJSON reports whether stdout was meant as JSON. A leading brace is
// enough: an object that then fails to parse is reported as an error rather
// than silently read as plain text, the stricter reading Codex applies.
func looksJSON(s string) bool {
	return strings.HasPrefix(s, "{")
}

func parseOutput(s string) (hookOutput, error) {
	var o hookOutput
	err := json.Unmarshal([]byte(s), &o)
	return o, err
}

// merge folds one handler's result into the outcome following the exit-code
// contract: 0 with JSON is a decision, 0 with text is context on the events
// that take it, 2 blocks with the reason from JSON or stderr, anything else
// is a non-blocking error unless the handler fails closed.
func (r *Runner) merge(out *Outcome, ev Event, b bound, res execResult, fields map[string]interface{}) {
	label := b.source.Display
	if res.cancelled {
		return
	}
	if msg := res.failure(label); msg != "" {
		r.fail(out, ev, b, msg)
		return
	}
	stdout := strings.TrimSpace(res.stdout)
	if res.exitCode == 2 {
		reason := ""
		if looksJSON(stdout) {
			if parsed, err := parseOutput(stdout); err == nil {
				reason = parsed.reason()
			}
		}
		if reason == "" {
			reason = strings.TrimSpace(res.stderr)
		}
		if reason == "" {
			reason = fmt.Sprintf("blocked by hook in %s (exit status 2)", label)
		}
		r.block(out, ev, r.truncate(reason))
		return
	}
	if res.exitCode != 0 {
		msg := fmt.Sprintf("hook in %s exited with exit status %d", label, res.exitCode)
		if line := firstLine(res.stderr); line != "" {
			msg += ": " + line
		}
		r.fail(out, ev, b, msg)
		return
	}
	if looksJSON(stdout) {
		parsed, err := parseOutput(stdout)
		if err != nil {
			r.fail(out, ev, b, fmt.Sprintf("hook in %s printed invalid JSON: %v", label, err))
			return
		}
		r.apply(out, ev, label, parsed, fields)
		return
	}
	if stdout == "" {
		return
	}
	switch ev.Name {
	case EventSessionStart, EventUserPromptSubmit:
		out.Context = append(out.Context, r.truncate(stdout))
	default:
		r.logger().Debug("hook printed plain text that this event ignores", "event", ev.Name, "source", label)
	}
}

func (r *Runner) fail(out *Outcome, ev Event, b bound, msg string) {
	r.logger().Warn("hook failed", "event", ev.Name, "source", b.source.Display, "error", msg)
	if b.handler.FailClosed {
		r.block(out, ev, r.truncate(msg))
		return
	}
	out.Errors = append(out.Errors, msg)
}

// block records a deny (tool events) or a block (other events).
func (r *Runner) block(out *Outcome, ev Event, reason string) {
	decision := DecisionBlock
	if ev.Name == EventPreToolUse {
		decision = DecisionDeny
	}
	mergeDecision(out, decision, reason)
}

var decisionRank = map[string]int{DecisionAllow: 1, DecisionAsk: 2, DecisionDeny: 3, DecisionBlock: 3}

// mergeDecision keeps the most restrictive decision; the reason follows the
// decision that set it, and a later equal decision fills a missing reason.
func mergeDecision(out *Outcome, decision, reason string) {
	if decision == "" {
		return
	}
	cur, next := decisionRank[out.Decision], decisionRank[decision]
	switch {
	case next > cur:
		out.Decision = decision
		out.Reason = reason
	case next == cur && out.Reason == "" && reason != "":
		out.Reason = reason
	}
}

func (r *Runner) apply(out *Outcome, ev Event, label string, o hookOutput, fields map[string]interface{}) {
	if o.Continue != nil && !*o.Continue {
		out.Stop = true
		if out.StopReason == "" {
			out.StopReason = r.truncate(strings.TrimSpace(o.StopReason))
		}
	}
	if msg := strings.TrimSpace(o.SystemMessage); msg != "" {
		out.SystemMessages = append(out.SystemMessages, r.truncate(msg))
	}
	switch strings.ToLower(strings.TrimSpace(o.Decision)) {
	case DecisionBlock, DecisionDeny:
		reason := strings.TrimSpace(o.Reason)
		if reason == "" {
			reason = "blocked by hook in " + label
		}
		r.block(out, ev, r.truncate(reason))
	case DecisionAllow, "approve":
		if ev.Name == EventPreToolUse {
			mergeDecision(out, DecisionAllow, "")
		}
	}
	sp := o.Specific
	if sp == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(sp.PermissionDecision)) {
	case DecisionDeny:
		reason := strings.TrimSpace(sp.PermissionDecisionReason)
		if reason == "" {
			reason = "blocked by hook in " + label
		}
		r.block(out, ev, r.truncate(reason))
	case DecisionAsk:
		mergeDecision(out, DecisionAsk, r.truncate(strings.TrimSpace(sp.PermissionDecisionReason)))
	case DecisionAllow:
		mergeDecision(out, DecisionAllow, "")
	}
	if sp.UpdatedInput != nil && ev.Name == EventPreToolUse {
		fields["tool_input"] = sp.UpdatedInput
		out.UpdatedInput = sp.UpdatedInput
	}
	if text := strings.TrimSpace(sp.AdditionalContext); text != "" {
		out.Context = append(out.Context, r.truncate(text))
	}
}

// truncate caps a text at MaxOutputChars on a rune boundary and marks the cut.
func (r *Runner) truncate(s string) string {
	limit := r.MaxOutputChars
	if limit <= 0 {
		limit = defaultMaxOutputChars
	}
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + truncationMarker
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// limitedBuffer keeps the first limit bytes written and drops the rest, so a
// chatty hook cannot grow memory without bound.
type limitedBuffer struct {
	buf     bytes.Buffer
	limit   int
	dropped bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.buf.Len()
	switch {
	case room <= 0:
		b.dropped = true
	case len(p) > room:
		b.buf.Write(p[:room])
		b.dropped = true
	default:
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}
