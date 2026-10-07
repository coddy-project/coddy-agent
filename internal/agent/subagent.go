package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// SubagentRuntime is what the agent needs from the session manager to run a
// child: create and register its session, run its one turn through the normal
// prompt path, and retire it afterwards. session.Manager implements it, and
// every surface that runs turns wires it - the scheduler's runs are children
// of their job session and go through it too. A surface that leaves it unset
// has the spawn_agent tool answer that subagents are unavailable.
type SubagentRuntime interface {
	CreateSubagentSession(ctx context.Context, spec session.SubagentSpec) (*session.State, error)
	RunSubagentTurn(ctx context.Context, sessionID string, prompt []acp.ContentBlock, sender acp.UpdateSender) (*acp.SessionPromptResult, error)
	RetireSubagentSession(sessionID string)
}

// SetSubagentRuntime wires the manager that owns child sessions.
func (a *Agent) SetSubagentRuntime(rt SubagentRuntime) {
	if a == nil {
		return
	}
	a.subagentRuntime = rt
}

// DetachedPermissionRequest is one prompt from a child whose parent turn has
// already ended. It carries the ids the surface needs to show it against the
// right task and to route the answer back.
type DetachedPermissionRequest struct {
	ParentSessionID string
	ChildSessionID  string
	TaskID          string
	AgentName       string
	// Params.SessionID is the child's id: the answer is posted against the
	// session that is actually waiting, not against the finished parent turn.
	Params acp.PermissionRequestParams
}

// DetachedPermissionBroker shows a detached child's permission prompt where the
// person reading the parent conversation is, and blocks until it is answered or
// ctx - the run's own context - ends.
//
// Same shape as SubagentRuntime: the agent declares what it needs, the surface
// implements it. Only a surface that can ask somebody after a turn has ended
// provides one: the interactive console asks through its modal, and coddy serve
// offers the prompt to every surface of the process at once (the web chat of
// the parent session, a console attached over --remote, the Telegram chat that
// owns the session), the first answer winning. A surface that cannot (ACP,
// print mode, a scheduled run) leaves it unset, and the relay refuses with a
// reason the child can report instead of silently reading as "the user said
// no".
type DetachedPermissionBroker interface {
	RequestDetachedPermission(ctx context.Context, req DetachedPermissionRequest) (*acp.PermissionResult, error)
}

// ErrNoDetachedApprover is what a broker answers when nothing that could show
// the prompt is attached right now - in coddy serve, no surface is up, or none
// of them owns the parent conversation. The relay reads it exactly like a
// missing broker.
var ErrNoDetachedApprover = errors.New("no surface can show a detached subagent's permission prompt")

// SetDetachedPermissionBroker wires the surface that can answer a detached
// child's prompt.
func (a *Agent) SetDetachedPermissionBroker(b DetachedPermissionBroker) {
	if a == nil {
		return
	}
	a.detachedPermissions = b
}

// Mandatory exclusions from every child's tool set: a child cannot ask the
// user, cannot rewrite the agent's own configuration, and cannot leave plan
// mode on the operator's behalf.
var subagentMandatoryExclusions = []string{
	"question",
	"config_set", "config_changes", "config_commit", "config_revert", "config_rollback",
	"plan_exit",
	// A child runs on the model its parent (or its definition) chose.
	"switch_model",
	// A child shares the parent's workspace; moving it mid-run would strand
	// every tool of both, and the child has no switcher wired anyway.
	"worktree_create",
}

// The process-wide limiter and the per-parent permission arbiters. Both are
// process-wide on purpose, like the task pool: the cap the operator sets is a
// number of LLM loops, whatever session started them, and the prompts several
// children raise in one parent chat have to be serialised across all of them.
var (
	subagentLimiter = subagents.NewLimiter(0)

	arbitersMu sync.Mutex
	arbiters   = map[string]*permissionArbiter{}
)

// permissionArbiter serialises the permission prompts of every child of one
// parent session, so at most one prompt is in flight for that parent (the HTTP
// pending-permission record can hold exactly one). It carries no context: each
// relay owns its own turn and child contexts.
type permissionArbiter struct {
	slot chan struct{}
	refs int
	// waiting counts relays blocked on the slot; tests use it to make an
	// overlap deterministic instead of timing it.
	waiting atomic.Int32
}

// relayedPermissionOptions keeps the per-call answers of a forwarded prompt
// and drops the "always" ones: a run of a child is one turn, and a resumed run
// starts without the grants of the earlier one, so a standing grant could not
// outlive the call it was given for and would only mislead.
func relayedPermissionOptions(opts []acp.PermissionOption) []acp.PermissionOption {
	out := make([]acp.PermissionOption, 0, len(opts))
	for _, o := range opts {
		if strings.HasPrefix(strings.TrimSpace(o.OptionID), "allow_always") || strings.HasPrefix(strings.TrimSpace(o.Kind), "allow_always") {
			continue
		}
		out = append(out, o)
	}
	return out
}

// arbiterWaiters reports how many relays of a parent are queued on its slot.
func arbiterWaiters(parentID string) int {
	arbitersMu.Lock()
	arb := arbiters[parentID]
	arbitersMu.Unlock()
	if arb == nil {
		return 0
	}
	return int(arb.waiting.Load())
}

func acquireArbiter(parentID string) *permissionArbiter {
	arbitersMu.Lock()
	defer arbitersMu.Unlock()
	arb := arbiters[parentID]
	if arb == nil {
		arb = &permissionArbiter{slot: make(chan struct{}, 1)}
		arbiters[parentID] = arb
	}
	arb.refs++
	return arb
}

func releaseArbiter(parentID string) {
	arbitersMu.Lock()
	defer arbitersMu.Unlock()
	arb := arbiters[parentID]
	if arb == nil {
		return
	}
	arb.refs--
	if arb.refs <= 0 {
		delete(arbiters, parentID)
	}
}

// permissionRelay forwards a child's permission requests to the parent's
// sender while the parent turn that spawned the child is still alive, and to
// the surface's detached-permission broker once that turn has ended (failing
// closed, with a reason, when there is none). It is created per spawn, so a
// child spawned by a later turn carries that turn's context.
type permissionRelay struct {
	parent          acp.UpdateSender
	parentSessionID string
	childSessionID  string
	agentName       string
	// taskID is assigned inside the pool's launch callback, before the run
	// goroutine starts, so the goroutine sees it without a lock.
	taskID string
	// broker answers prompts raised after turnCtx is done; nil on a surface
	// that cannot show one.
	broker DetachedPermissionBroker
	// childPermissionMode is the child's effective mode, stamped on every
	// forwarded request so a sender never mistakes it for the parent's.
	childPermissionMode string
	turnCtx             context.Context
	childCtx            context.Context
	arbiter             *permissionArbiter
}

func deniedPermission(reason string) *acp.PermissionResult {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject", Reason: reason}
}

// Reasons a child's prompt is refused without anyone answering it. The child
// is told which, so it reports honestly instead of claiming the user refused.
const (
	permissionReasonNoClient   = "no interactive client is attached to the session that spawned this subagent, so nobody could be asked. Do not retry the same call; finish and report what you could not do"
	permissionReasonNoApprover = "this subagent is running detached: the turn that spawned it has ended and no interactive client is attached, so nobody could be asked. Do not retry the same call; finish and report what you could not do"
	permissionReasonUnanswered = "the approval request was raised but nobody answered it before the run ended"
	permissionReasonStopped    = "the run was stopped while waiting for approval"
)

// relayedPermissionTitle prefixes a forwarded prompt with the subagent it is
// asked on behalf of, so whoever answers knows it is not the parent asking.
func relayedPermissionTitle(agentName, title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Run a tool"
	}
	return fmt.Sprintf("[subagent %s] %s", agentName, title)
}

// Request forwards one permission prompt. While the parent turn is alive it
// goes to the parent's client; once that turn is over - a detached run's
// normal state - it goes to the broker, and a prompt still unanswered on the
// parent's screen when the turn ends moves there too. The child being stopped
// or the caller's own context ending resolve as a denial without leaving a
// goroutine parked on the transport.
func (r *permissionRelay) Request(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if r == nil || r.parent == nil {
		return deniedPermission(permissionReasonNoClient), nil
	}
	if r.turnCtx.Err() != nil {
		return r.requestDetached(ctx, params)
	}
	r.arbiter.waiting.Add(1)
	select {
	case r.arbiter.slot <- struct{}{}:
		r.arbiter.waiting.Add(-1)
	case <-r.turnCtx.Done():
		r.arbiter.waiting.Add(-1)
		return r.requestDetached(ctx, params)
	case <-r.childCtx.Done():
		r.arbiter.waiting.Add(-1)
		return deniedPermission(permissionReasonStopped), nil
	case <-ctx.Done():
		r.arbiter.waiting.Add(-1)
		return deniedPermission(permissionReasonStopped), nil
	}
	// The slot is handed back on every exit, and before any detached wait: that
	// wait can last minutes, and a sibling's live prompt must not queue behind
	// it.
	slotHeld := true
	releaseSlot := func() {
		if slotHeld {
			slotHeld = false
			<-r.arbiter.slot
		}
	}
	defer releaseSlot()
	if r.turnCtx.Err() != nil {
		releaseSlot()
		return r.requestDetached(ctx, params)
	}

	// The prompt as it arrived, kept for the broker: the parent-facing copy
	// below is addressed, titled and narrowed for the parent's client, and
	// requestDetached does the same for the child-addressed one.
	asked := params
	params.SessionID = r.parentSessionID
	// The stamp is the stricter of what arrived and this child's own mode: a
	// grandchild's prompt crosses two relays, and the intermediate child must
	// not re-widen a narrower stamp on its way to the parent-facing sender.
	params.EffectivePermissionMode = subagents.NarrowPermissionMode(r.childPermissionMode, params.EffectivePermissionMode)
	// A child runs one turn and is retired, so an "always" answer could only
	// ever cover that one run; the parent-facing modal offers the honest
	// choices, allow once or reject.
	params.Options = relayedPermissionOptions(params.Options)
	params.ToolCall.Title = relayedPermissionTitle(r.agentName, params.ToolCall.Title)

	reqCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	type outcome struct {
		res *acp.PermissionResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := r.parent.RequestPermission(reqCtx, params)
		done <- outcome{res: res, err: err}
	}()
	// A forwarded prompt is never persisted by the parent-facing senders (the
	// HTTP bridge skips the pending record for a stamped request), so giving
	// up leaves nothing behind but the cancelled forward.
	abandon := func(reason string) *acp.PermissionResult {
		cancel()
		return deniedPermission(reason)
	}
	select {
	case out := <-done:
		if out.err != nil || out.res == nil {
			return abandon(permissionReasonUnanswered), nil
		}
		return out.res, nil
	case <-r.turnCtx.Done():
		// An answer that landed in the same instant is the person's answer and
		// is kept: handing the prompt over after it would ask a second time.
		select {
		case out := <-done:
			if out.err == nil && out.res != nil {
				return out.res, nil
			}
		default:
		}
		// The parent turn ended with the prompt still on its screen, and that
		// screen goes away with the stream that carried it: the parent's client
		// can no longer answer. Withdraw that copy - the console takes its modal
		// down, the bridge unregisters its wait, a chat marks its message as no
		// longer waiting - and raise the prompt where the conversation is still
		// read, exactly like a prompt asked after the turn had ended. The
		// person answers it once, as the detached prompt; where no broker
		// exists nobody can, and the child is told so. An answer that arrives
		// between this check and the cancel is lost with the forward, and the
		// prompt is asked once more: never executed on nobody's word.
		cancel()
		releaseSlot()
		return r.requestDetached(ctx, asked)
	case <-r.childCtx.Done():
		return abandon(permissionReasonStopped), nil
	case <-ctx.Done():
		return abandon(permissionReasonStopped), nil
	}
}

// requestDetached hands the prompt to the broker, which shows it where the
// parent conversation is read, and blocks until it is answered, the child is
// stopped, or the run's own deadline passes.
//
// It deliberately does not take the arbiter slot. The arbiter serialises
// prompts inside one parent chat because the parent holds a single pending
// record; a detached prompt is keyed by its own child session instead, and a
// wait that can last minutes must not block a sibling's live prompt.
func (r *permissionRelay) requestDetached(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if r.broker == nil {
		return deniedPermission(permissionReasonNoApprover), nil
	}
	params.SessionID = r.childSessionID
	params.EffectivePermissionMode = subagents.NarrowPermissionMode(r.childPermissionMode, params.EffectivePermissionMode)
	params.Options = relayedPermissionOptions(params.Options)
	params.ToolCall.Title = relayedPermissionTitle(r.agentName, params.ToolCall.Title)

	// The wait ends with the run: the pool's timeout, an explicit stop and
	// shutdown all cancel the child's context, whatever the caller passed.
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopWatching := context.AfterFunc(r.childCtx, cancel)
	defer stopWatching()

	res, err := r.broker.RequestDetachedPermission(waitCtx, DetachedPermissionRequest{
		ParentSessionID: r.parentSessionID,
		ChildSessionID:  r.childSessionID,
		TaskID:          r.taskID,
		AgentName:       r.agentName,
		Params:          params,
	})
	switch {
	case errors.Is(err, ErrNoDetachedApprover):
		return deniedPermission(permissionReasonNoApprover), nil
	case err == nil && res != nil:
		return res, nil
	case r.childCtx.Err() != nil || ctx.Err() != nil:
		return deniedPermission(permissionReasonStopped), nil
	default:
		return deniedPermission(permissionReasonUnanswered), nil
	}
}

// subagentSender is the child's acp.UpdateSender: progress goes to the task's
// output sink, permission requests to the relay, questions nowhere.
type subagentSender struct {
	out   io.Writer
	relay *permissionRelay
	// onUsage hears what the child's model calls have spent so far, whenever it
	// changes: the launcher hands it to the task row (bgtask.Pool.SetAgentUsage).
	// Set before the child starts and never changed.
	onUsage func(inputTokens, outputTokens int)

	mu       sync.Mutex
	line     strings.Builder
	toolName map[string]string
	usage    childUsage
}

// childUsage counts the child's tokens the way its task row shows them. Input is
// summed from token_usage, one figure per completed call. Output comes from
// turn_progress, which already folds the provider's exact figures with an estimate
// of the call in flight, so the row moves while a long call streams; a turn is told
// from the next by its start, and the output of the turns that ended is kept.
type childUsage struct {
	input      int
	outputDone int
	turn       string
	turnOutput int
}

func (u childUsage) output() int { return u.outputDone + u.turnOutput }

func newSubagentSender(out io.Writer, relay *permissionRelay) *subagentSender {
	return &subagentSender{out: out, relay: relay, toolName: map[string]string{}}
}

// SendSessionUpdate renders the child's stream as compact log lines. Text is
// flushed per line so the operator can follow the child in the Tasks panel.
func (s *subagentSender) SendSessionUpdate(_ string, update interface{}) error {
	s.mu.Lock()
	counted, changed := s.count(update)
	s.render(update)
	s.mu.Unlock()
	if changed && s.onUsage != nil {
		s.onUsage(counted.input, counted.output())
	}
	return nil
}

// count folds a usage update into the child's account. Callers hold mu.
func (s *subagentSender) count(update interface{}) (childUsage, bool) {
	before := s.usage
	switch u := update.(type) {
	case acp.TokenUsageUpdate:
		s.usage.input += max(0, u.InputTokens)
	case acp.TurnProgressUpdate:
		if u.StartedAt != s.usage.turn {
			s.usage.outputDone += s.usage.turnOutput
			s.usage.turn, s.usage.turnOutput = u.StartedAt, 0
		}
		s.usage.turnOutput = max(0, u.OutputTokens)
	default:
		return s.usage, false
	}
	return s.usage, s.usage.input != before.input || s.usage.output() != before.output()
}

// render writes the update to the task log. Callers hold mu.
func (s *subagentSender) render(update interface{}) {
	switch u := update.(type) {
	case acp.MessageChunkUpdate:
		if u.Content.Type != acp.ContentTypeText || u.Content.Text == "" {
			return
		}
		s.line.WriteString(u.Content.Text)
		s.flushLines(false)
	case acp.ToolCallUpdate:
		// A provider that streams the call's name before its arguments
		// announces the same call twice (the pending row, then the complete
		// call); the log names it once.
		if _, seen := s.toolName[u.ToolCallID]; seen && u.ToolCallID != "" {
			s.toolName[u.ToolCallID] = u.Title
			return
		}
		s.flushLines(true)
		s.toolName[u.ToolCallID] = u.Title
		_, _ = fmt.Fprintf(s.out, "→ %s\n", u.Title)
	case acp.ToolCallStatusUpdate:
		name := s.toolName[u.ToolCallID]
		switch u.Status {
		case "completed":
			s.flushLines(true)
			_, _ = fmt.Fprintf(s.out, "✓ %s\n", name)
		case "failed", "cancelled":
			s.flushLines(true)
			_, _ = fmt.Fprintf(s.out, "✗ %s (%s)\n", name, u.Status)
		}
	}
}

// flushLines writes complete lines of buffered assistant text; force writes a
// trailing partial line too (before a tool marker, and at the end).
func (s *subagentSender) flushLines(force bool) {
	text := s.line.String()
	if text == "" {
		return
	}
	idx := strings.LastIndexByte(text, '\n')
	if idx < 0 && !force {
		return
	}
	var complete, rest string
	if force {
		complete, rest = text, ""
	} else {
		complete, rest = text[:idx+1], text[idx+1:]
	}
	for _, ln := range strings.Split(strings.TrimRight(complete, "\n"), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		_, _ = fmt.Fprintf(s.out, "[assistant] %s\n", ln)
	}
	s.line.Reset()
	s.line.WriteString(rest)
}

func (s *subagentSender) Flush() {
	s.mu.Lock()
	s.flushLines(true)
	s.mu.Unlock()
}

// ProviderRecovery writes the reconnect the child's turn is about to make into
// the task log, after the text the child had written: which failure cut the
// call and when the run tries again (issue #389). Without it a run that waits
// out a provider outage reads as a hung one for minutes.
func (s *subagentSender) ProviderRecovery(err error, delay time.Duration, attempt, budget int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLines(true)
	_, _ = fmt.Fprintf(s.out, "↻ provider failed (%v); reconnecting in %s (attempt %d of %d)\n", err, humanDuration(delay), attempt, budget)
}

func (s *subagentSender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return s.relay.Request(ctx, params)
}

func (s *subagentSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return nil, fmt.Errorf("a subagent cannot ask the user questions; finish with a report instead")
}

// subagentHandle is the pool's view of a child run: Stop cancels the child,
// Wait blocks until the run settled, and there is no OS process behind it.
// A run that failed hands its error to the pool through Wait, so the task
// record says why (the drawer row, the console line, the memory_run update).
type subagentHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	exit   int
	err    error
}

func (h *subagentHandle) Wait() (int, error) {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exit, h.err
}

func (h *subagentHandle) Stop(time.Duration) error {
	h.cancel()
	return nil
}

func (h *subagentHandle) PID() int                    { return 0 }
func (h *subagentHandle) ProcessStartedAt() time.Time { return time.Time{} }

// subagentRun is the bookkeeping of one child run: a spawn from the parent's
// side, a system child such as the memory subagent (def nil, system true), or
// a scheduled run from the daemon's.
type subagentRun struct {
	// name is what the run is called in logs and reports: the definition
	// name of a spawn, the system agent's name, the job id (or its
	// definition) of a scheduled run.
	name string
	// def is the definition the run is made under; nil for a system child and
	// for a scheduled run without one.
	def *subagents.Definition
	// system marks a run the runtime started on its own behalf: no
	// SubagentStart/SubagentStop hooks, no limiter, no permission relay.
	system  bool
	childID string
	taskID  string
	prompt  string
	// out is the task's output sink; the parent writes where the memory
	// report went into it.
	out io.Writer
	// parentMode is the mode the spawning turn was admitted in; the parent's
	// hook runner is keyed by it.
	parentMode string
	handle     *subagentHandle
	sender     *subagentSender
	report     string
	startedAt  time.Time
	turns      int
	status     string // end_turn | cancelled | failed | ...
	err        error
	// resumable marks a run spawn_agent started, the only kind its parent can
	// continue with resume: a scheduled run's parent is a job session that
	// never takes a turn, and the memory child is the runtime's own.
	resumable bool
	// created says the child session exists, so its transcript is there to
	// resume; a run that failed to create it has nothing to continue.
	created bool
	// prior is how many messages the child's transcript held when this run
	// started, compaction summaries not counted: zero for a new child, the
	// earlier runs' for a resumed one. The run's turns and its report are read
	// past them (runMessages).
	prior int
	mu    sync.Mutex
}

// unfinished reports a run that ended without its report: an error, a stop
// or a timeout. Callers hold mu.
func (r *subagentRun) unfinished() bool {
	return r.err != nil || r.status == "cancelled"
}

// offersResume reports whether the run's report tells the parent to continue
// it: a spawn_agent run whose child exists. Callers hold mu.
func (r *subagentRun) offersResume() bool {
	return r.resumable && r.created
}

// displayName is what logs and reports call the run: the name it was given,
// or its definition's when a caller built the run from a definition alone.
func (r *subagentRun) displayName() string {
	if r == nil {
		return ""
	}
	if r.name != "" {
		return r.name
	}
	if r.def != nil {
		return r.def.Name
	}
	return ""
}

// subagentDepth is how deep this agent's session sits in a spawn tree.
func (a *Agent) subagentDepth() int {
	if a.subagent == nil {
		return 0
	}
	return a.subagent.Depth
}

// canSpawn reports whether this session may spawn at all: the feature is on, a
// runtime is wired, and the depth limit leaves room - or a spawn allowlist the
// session's own definition declared extends it exactly one generation past
// the cap.
func (a *Agent) canSpawn() bool {
	if a.cfg == nil || !a.cfg.Subagents.ResolvedEnabled() || a.subagentRuntime == nil {
		return false
	}
	depth := a.subagentDepth()
	maxDepth := a.cfg.Subagents.EffectiveMaxDepth()
	if depth < maxDepth {
		return true
	}
	return depth == maxDepth && len(a.spawnAllowlist()) > 0
}

// spawnAllowlist is the set of subagent names this session's own definition
// lets it delegate to (already scope-filtered at spawn time), or nil for an
// ordinary, unrestricted session.
func (a *Agent) spawnAllowlist() []string {
	if a.subagent == nil {
		return nil
	}
	return a.subagent.Spawns
}

// canSpawnInMode adds the mode rule to canSpawn: ask mode is read-only and
// delegates nothing, so spawn_agent is neither offered nor honoured there.
func (a *Agent) canSpawnInMode(mode string) bool {
	return mode != "ask" && a.canSpawn()
}

// applySubagentEnv wires the spawn hook and the depth into a tool env. mode
// is the mode the turn was admitted in: the hook decides the child's mode and
// the parent tool set from that snapshot, never from the live session mode,
// which a concurrent session/set_mode can flip while the turn runs.
func (a *Agent) applySubagentEnv(env *tools.Env, mode string) {
	env.SubagentDepth = a.subagentDepth()
	env.WakeableSession = a.subagent == nil
	if a.canSpawnInMode(mode) {
		env.SpawnAgent = func(ctx context.Context, req tooling.SpawnRequest) (string, error) {
			return a.spawnSubagentInMode(ctx, req, mode)
		}
	} else {
		env.SpawnAgent = nil
	}
}

// subagentDefinitions loads the definitions visible for this session's cwd.
func (a *Agent) subagentDefinitions() []*subagents.Definition {
	loader := subagents.NewLoader(a.cfg.Subagents.SearchDirs(), a.cfg.Subagents.ResolvedProjectTrust())
	loader.Log = a.log
	return loader.Load(a.state.GetCWD(), a.cfg.Paths.Home)
}

// subagentCatalogBlock renders the Subagents section for the system prompt, or
// nothing when this session cannot spawn.
func (a *Agent) subagentCatalogBlock() string {
	if !a.canSpawn() {
		return ""
	}
	cwd := a.state.GetCWD()
	defs := a.subagentDefinitions()
	if allow := a.spawnAllowlist(); len(allow) > 0 {
		defs = subagents.DefinitionsMatching(defs, allow)
	}
	entries := subagents.BuildCatalog(defs, a.cfg.Subagents.ResolvedProjectTrust(),
		subagents.CanonicalWorkspace(cwd), subagents.NewTrustStore(a.cfg.Paths.Home))
	return subagents.PromptBlock(entries)
}

// subagentRoleBlock renders the child's role section: a delegate's preamble
// for a spawned child, a scheduled job's for a run the scheduler started.
func (a *Agent) subagentRoleBlock() string {
	if a.subagent == nil {
		return ""
	}
	var b strings.Builder
	if sched := a.subagent.Scheduler; sched != nil {
		fmt.Fprintf(&b, "You are running the scheduled job **%s**", sched.JobID)
		switch sched.Trigger {
		case "cron":
			if !sched.FireSlot.IsZero() {
				fmt.Fprintf(&b, ", started by the scheduler on its cron schedule at %s", sched.FireSlot.UTC().Format("2006-01-02 15:04 UTC"))
			} else {
				b.WriteString(", started by the scheduler on its cron schedule")
			}
		case "manual":
			b.WriteString(", started by hand through the scheduler")
		}
		b.WriteString(". The run is unattended: nobody watches it and nobody answers questions, so work from the instruction below and from the workspace, and when something blocks you, say so and stop. ")
		b.WriteString("Your final message is kept as the run's record together with the transcript, so finish with a concise report of what you did, what you found (with file paths), and what remains.")
	} else {
		fmt.Fprintf(&b, "You are running as the subagent **%s**, spawned by a parent agent to complete one self-contained task. ", a.subagent.Name)
		b.WriteString("You see nothing of the parent's conversation: work from the task below and from the workspace. ")
		b.WriteString("You cannot ask the user questions; if something blocks you, say so in your report. ")
		b.WriteString("Only your final message reaches the parent, so finish with a concise report of what you did, what you found (with file paths), and what remains.")
	}
	if role := strings.TrimSpace(a.subagent.Role); role != "" {
		b.WriteString("\n\n")
		b.WriteString(role)
	}
	return b.String()
}

// subagentAllows reports whether a child may call a tool. Ordinary sessions
// allow everything (their mode set is applied elsewhere).
func (a *Agent) subagentAllows(name string) bool {
	if a.subagent == nil {
		return true
	}
	for _, n := range a.subagent.Tools {
		if n == name {
			return true
		}
	}
	return false
}

// parentToolNames lists every tool name this session could call right now,
// MCP tools included: the set a child's effective set is intersected with.
func (a *Agent) parentToolNames(mode string) []string {
	defs := a.currentToolDefinitions(mode)
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names
}

// spawnSubagent is the Env.SpawnAgent hook. See docs/plans/subagents.md 3.3
// for the order of decisions; every refusal names the knob that applies.
func (a *Agent) spawnSubagent(ctx context.Context, req tooling.SpawnRequest) (string, error) {
	return a.spawnSubagentInMode(ctx, req, a.state.EffectiveMode())
}

// spawnSubagentInMode is spawnSubagent for a turn pinned to mode (see
// applySubagentEnv).
func (a *Agent) spawnSubagentInMode(ctx context.Context, req tooling.SpawnRequest, mode string) (string, error) {
	rt := a.subagentRuntime
	if rt == nil {
		return "", fmt.Errorf("subagents are not available in this session")
	}
	if mode == "ask" {
		return "", fmt.Errorf("subagents cannot be spawned from an ask-mode turn: ask mode is read-only and delegates nothing")
	}
	cfg := a.cfg
	if !cfg.Subagents.ResolvedEnabled() {
		return "", fmt.Errorf("subagents are disabled (subagents.enable is false)")
	}
	maxDepth := cfg.Subagents.EffectiveMaxDepth()
	depth := a.subagentDepth()
	spawnAllow := a.spawnAllowlist()
	// Below the cap any session spawns; at the cap only a definition with a
	// spawn allowlist still may (and only the names on it, enforced below);
	// past it nobody does.
	if depth > maxDepth || (depth == maxDepth && len(spawnAllow) == 0) {
		return "", fmt.Errorf("this session cannot spawn subagents: subagents.max_depth is %d and this session already runs at depth %d", maxDepth, depth)
	}
	if len(req.Prompt) > subagents.MaxPromptBytes {
		return "", fmt.Errorf("spawn_agent: prompt is %d bytes, the limit is %d", len(req.Prompt), subagents.MaxPromptBytes)
	}
	// A resumed run continues a finished child of this session in its own
	// session (issue #389); everything below decides it like a new spawn,
	// against the definition as it is now.
	var resumed *resumeTarget
	if ref := strings.TrimSpace(req.Resume); ref != "" {
		target, err := a.findResumeTarget(ref, req.Agent)
		if err != nil {
			return "", err
		}
		resumed = target
	}

	defs := a.subagentDefinitions()
	spawnable := defs
	if len(spawnAllow) > 0 {
		spawnable = subagents.DefinitionsMatching(defs, spawnAllow)
	}
	def := subagents.FindByName(spawnable, req.Agent)
	if def == nil {
		if len(spawnAllow) > 0 && subagents.FindByName(defs, req.Agent) != nil {
			return "", fmt.Errorf("subagent %q may only delegate to %s; %q is not on its spawns allowlist",
				a.subagent.Name, strings.Join(spawnAllow, ", "), req.Agent)
		}
		return "", fmt.Errorf("unknown subagent %q; available: %s", req.Agent, strings.Join(subagents.VisibleNames(spawnable), ", "))
	}
	cwd := a.state.GetCWD()
	workspace := subagents.CanonicalWorkspace(cwd)
	policy := cfg.Subagents.ResolvedProjectTrust()
	store := subagents.NewTrustStore(cfg.Paths.Home)
	if subagents.Decide(def, policy, workspace, store) == subagents.TrustNeedsApproval {
		return "", fmt.Errorf("subagent %q comes from a project file (%s) that is not approved for workspace %s; "+
			"ask the user to approve it on the machine running coddy: `coddy agents trust %s --cwd %s` there, "+
			"or POST /coddy/subagents/%s/trust with body {\"cwd\": %q}; "+
			"or to set subagents.project_trust to allow for this checkout, then try again",
			def.Name, def.Path, workspace, def.Name, workspace, def.Name, workspace)
	}

	// The parent mode is the mode this turn was admitted in, not the live
	// session mode: a set_mode landing mid-turn must not hand a plan turn an
	// agent child. A read-only parent (plan, ask) forces its own mode on the
	// child; only an agent-mode parent lets the definition pick.
	parentMode := mode
	childMode := parentMode
	if def.Mode != "" && parentMode != "plan" && parentMode != "ask" {
		childMode = def.Mode
	}
	childPerm := subagents.NarrowPermissionMode(effectivePermMode(a.state, cfg), def.PermissionMode)
	childDepth := a.subagentDepth() + 1

	// The child's spawn allowlist, already narrowed by scope: a project
	// definition's spawns is ignored here so a checked-in file cannot widen
	// the depth guard. It decides both which names the child may delegate
	// to (via SubagentMeta.Spawns below) and whether it gets spawn_agent at
	// the boundary depth at all.
	childSpawns := def.EffectiveSpawns()

	exclusions := append([]string(nil), subagentMandatoryExclusions...)
	if childDepth > maxDepth || (childDepth == maxDepth && len(childSpawns) == 0) {
		exclusions = append(exclusions, tools.ToolSpawnAgent)
	}
	effective := subagents.EffectiveTools(a.parentToolNames(parentMode), ToolSetForMode(childMode), def, exclusions)
	if len(effective) == 0 {
		return "", fmt.Errorf("subagent %q would have no tools at all: its allowlist %v leaves nothing of this session's tool set (after the denylist and the mandatory exclusions); fix the definition or pick another subagent",
			def.Name, def.Tools)
	}
	connectMCP := false
	for _, n := range effective {
		if strings.Contains(n, "__") {
			connectMCP = true
			break
		}
	}

	// The child inherits the parent's effective model; a definition may name
	// a configured one instead. An unknown id falls back to the parent's and
	// is noted in the task log as well as the agent log.
	model := a.state.EffectiveModelID(cfg)
	unknownModel := ""
	switch {
	case req.Model != "":
		// The model chose the child's model itself: an id the configuration
		// does not know is its mistake to correct, not a silent fallback.
		if cfg.FindModelEntry(req.Model) == nil {
			return "", fmt.Errorf("spawn_agent: unknown model %q (configured: %s)", req.Model, strings.Join(configuredModels(cfg), ", "))
		}
		model = req.Model
	case resumed != nil && resumed.model != "" && cfg.FindModelEntry(resumed.model) != nil:
		// A resumed child goes on with the model that wrote its transcript.
		model = resumed.model
	case def.Model != "":
		if cfg.FindModelEntry(def.Model) != nil {
			model = def.Model
		} else {
			unknownModel = def.Model
			a.log.Warn("subagent definition names an unknown model; the parent's model is used", "agent", def.Name, "model", def.Model, "using", model)
		}
	}
	// The child's reasoning level: the call's, then the definition's, then
	// its model's default. The call's must be one the model offers; the
	// definition's is dropped with a warning when it is not.
	reasoning := ""
	choices := cfg.ReasoningChoicesFor(cfg.FindModelEntry(model))
	switch {
	case req.Reasoning != "":
		if req.Reasoning != config.ReasoningDefault {
			if !containsString(choices, req.Reasoning) {
				return "", fmt.Errorf("spawn_agent: reasoning %q is not offered by model %q (offered: %s, default)", req.Reasoning, model, strings.Join(choices, ", "))
			}
			reasoning = req.Reasoning
		}
	case def.Reasoning != "" && def.Reasoning != config.ReasoningDefault:
		if containsString(choices, def.Reasoning) {
			reasoning = def.Reasoning
		} else {
			a.log.Warn("subagent definition names a reasoning level its model does not offer; the default is used", "agent", def.Name, "reasoning", def.Reasoning, "model", model)
		}
	}

	parentID := a.state.GetID()
	childID := session.NewSessionID()
	if resumed != nil {
		childID = resumed.childID
	}
	background := req.Background || def.Background

	// SubagentStart hooks in the parent may refuse the spawn or hand the child
	// context, which is prepended to its task.
	prompt := req.Prompt
	if reason, contextText, blocked := a.runSubagentStartHooks(ctx, mode, def.Name, childID, req.Prompt, background); blocked {
		return "", fmt.Errorf("spawn of subagent %q blocked by hook: %s", def.Name, reason)
	} else if contextText != "" {
		prompt = contextText + "\n\n" + req.Prompt
	}

	// The cap is process-wide and follows the live configuration, not the
	// snapshot this turn started with: a limit saved through the settings
	// while an older turn was running must not be written back by that turn.
	limitCfg := cfg
	if live, ok := rt.(interface{ Cfg() *config.Config }); ok {
		if c := live.Cfg(); c != nil {
			limitCfg = c
		}
	}
	subagentLimiter.SetLimit(limitCfg.Subagents.EffectiveMaxConcurrent())
	release, ok := subagentLimiter.TryAcquire()
	if !ok {
		return "", fmt.Errorf("cannot start subagent %q: subagents.max_concurrent (%d) runs are already in flight; wait for one with background_wait or background_list, then try again",
			def.Name, cfg.Subagents.EffectiveMaxConcurrent())
	}

	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())
	pool := a.backgroundPool(sd)

	// Only a session somebody can wake registers a wake: a child and a
	// scheduled run are sealed when their turn returns, and a process with
	// no waker (coddy -p) has nobody to start the turn.
	notify := req.NotifyOnFinish && background && a.subagent == nil && pool.CanWake()
	label := firstLine(req.Description)
	if label == "" {
		label = firstLine(req.Prompt)
	}
	label = capRunes("agent "+def.Name+": "+label, maxTaskLabelRunes)
	timeout := subagents.ResolveTimeoutSeconds(req.TimeoutSeconds, def.TimeoutSeconds, req.ExpectedSeconds, cfg.Subagents.EffectiveDefaultTimeoutSeconds())

	arbiter := acquireArbiter(parentID)
	relay := &permissionRelay{
		parent:              a.server,
		parentSessionID:     parentID,
		childSessionID:      childID,
		agentName:           def.Name,
		broker:              a.detachedPermissions,
		childPermissionMode: childPerm,
		turnCtx:             ctx,
		arbiter:             arbiter,
	}
	modelNote := ""
	if unknownModel != "" {
		modelNote = fmt.Sprintf("model %q is not configured; using the parent's model %q", unknownModel, model)
	}
	if resumed != nil && req.Model == "" && resumed.model != "" && cfg.FindModelEntry(resumed.model) == nil {
		// The model that wrote the transcript was removed from the
		// configuration since: the run goes on with another, and says so.
		note := fmt.Sprintf("model %q the child ran on is not configured any more; the resumed run uses %q", resumed.model, model)
		a.log.Warn("resumed subagent's model is not configured any more", "agent", def.Name, "model", resumed.model, "using", model)
		if modelNote != "" {
			modelNote += "\n"
		}
		modelNote += note
	}
	snap, run, err := a.launchChildRun(ctx, rt, childLaunch{
		spec: session.SubagentSpec{
			ID:                childID,
			ParentSessionID:   parentID,
			Name:              def.Name,
			CWD:               cwd,
			Mode:              childMode,
			PermissionMode:    childPerm,
			SelectedModelID:   model,
			SelectedReasoning: reasoning,
			Title:             label,
			Role:              def.Role,
			Tools:             effective,
			Spawns:            childSpawns,
			Depth:             childDepth,
			MaxTurns:          def.MaxTurns,
			ConnectMCP:        connectMCP,
			ClientMCPServers:  a.parentSessionMCPDeclarations(),
			Resume:            resumed != nil,
		},
		def:             def,
		prompt:          prompt,
		parentMode:      mode,
		label:           label,
		timeoutSeconds:  timeout,
		expectedSeconds: req.ExpectedSeconds,
		detached:        background,
		notify:          notify,
		toolCallID:      a.currentToolCallID,
		relay:           relay,
		modelNote:       modelNote,
		cleanup: func() {
			releaseArbiter(parentID)
			release()
		},
	})
	if err != nil {
		if errors.Is(err, bgtask.ErrPoolFull) {
			return "", fmt.Errorf("cannot start subagent %q: %w; that is the per-session tools.background.max_concurrent limit, wait for a task with background_wait or background_list, then try again", def.Name, err)
		}
		if errors.Is(err, bgtask.ErrDraining) {
			return "", fmt.Errorf("cannot start subagent %q: %w", def.Name, err)
		}
		return "", err
	}

	if !background {
		// Wait for the task, not merely for the run: the handle closes when
		// the child's goroutine is done, but the pool records the terminal
		// status on its supervisor goroutine right after, and the envelope
		// carries that verdict. A cancelled parent turn already cancelled the
		// derived child context, so the task settles on its own; waiting on a
		// non-cancellable context only keeps the verdict final.
		final, err := pool.Wait(context.WithoutCancel(ctx), parentID, snap.ID, 0)
		if err != nil {
			final, _ = pool.Get(parentID, snap.ID)
		}
		return formatForegroundResult(run, final), nil
	}

	var b strings.Builder
	started := "Started"
	if resumed != nil {
		started = "Resumed"
	}
	fmt.Fprintf(&b, "%s subagent %s as background task %s (child session %s).\n", started, def.Name, snap.ID, childID)
	fmt.Fprintf(&b, "Hard timeout %s.\n", humanSecondsAgent(snap.TimeoutSeconds))
	switch {
	case snap.NotifyOnFinish:
		b.WriteString("You will be woken with the outcome when it finishes, so you can end your turn now.")
	case req.NotifyOnFinish:
		// Nothing here will start that turn - a child's transcript closes
		// with its turn, and coddy -p has no waker - so the run is real and
		// only the notice is not.
		b.WriteString("Nothing will wake you when it finishes here: follow it with background_list or background_output, and collect the report with background_wait before you end your turn.")
	default:
		b.WriteString("Keep working; follow it with background_list or background_output, and collect the report with background_wait.")
	}
	return b.String(), nil
}

// childLaunch is what a launcher decided about a child run before the pool is
// involved: the session to build, the task to register and the parent-side
// bookkeeping. spawnSubagentInMode fills it for a definition the model
// chose; the memory runtime fills it for the system child of a user turn.
type childLaunch struct {
	spec session.SubagentSpec
	// def is the definition behind a spawn_agent child; nil for a system child.
	def *subagents.Definition
	// prompt is the child's task text.
	prompt string
	// parentMode is the mode the launching turn was admitted in.
	parentMode string
	// label is the task label and the child session title.
	label string
	// timeoutSeconds is the run's hard limit; expectedSeconds the estimate.
	timeoutSeconds, expectedSeconds int
	// detached runs the child on a context that outlives the launching tool
	// call (a background spawn, every memory run).
	detached bool
	// notify wakes the parent when the task finishes (background spawns only).
	notify bool
	// toolCallID links the task to the transcript row that started it.
	toolCallID string
	// relay forwards the child's permission prompts; nil denies them all.
	relay *permissionRelay
	// modelNote is written into the task log when the definition named a
	// model that is not configured.
	modelNote string
	// system marks the runtime's own child (see subagentRun.system).
	system bool
	// cleanup runs once with the run's finish: what the launcher acquired
	// (a limiter slot, an arbiter, an in-flight slot) is released here.
	cleanup func()
}

// launchChildRun registers the child's task with the pool and starts the run
// goroutine. The callback hands the handle back at once and does everything
// that can block (session creation, MCP dialing, the turn itself) on that
// goroutine, so the pool's Stop and timeout reach a child that is still being
// created. The task id is known before anything starts. A refused launch has
// already run the cleanup when this returns.
func (a *Agent) launchChildRun(ctx context.Context, rt SubagentRuntime, l childLaunch) (bgtask.Snapshot, *subagentRun, error) {
	parentID := strings.TrimSpace(l.spec.ParentSessionID)
	childID := strings.TrimSpace(l.spec.ID)
	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())
	pool := a.backgroundPool(sd)

	base := ctx
	if l.detached {
		base = context.WithoutCancel(ctx)
	}
	runCtx, cancel := context.WithCancel(base)
	handle := &subagentHandle{cancel: cancel, done: make(chan struct{})}
	if l.relay != nil {
		l.relay.childCtx = runCtx
	}
	run := &subagentRun{
		def:        l.def,
		name:       l.spec.Name,
		system:     l.system,
		resumable:  !l.system,
		childID:    childID,
		prompt:     l.prompt,
		parentMode: l.parentMode,
		handle:     handle,
		startedAt:  time.Now(),
	}

	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			// A resumed run that never took the child over - another run of
			// it still holds the session - owns nothing of it: that run
			// settles and retires the child itself.
			run.mu.Lock()
			owned := run.created || !l.spec.Resume
			run.mu.Unlock()
			if owned {
				// Work the child launched settles before its transcript is
				// sealed, and its records leave the pool's memory with it:
				// the bundle keeps them, and a retired session is not coming
				// back for them.
				pool.StopSession(childID)
				pool.ReleaseSession(childID)
				rt.RetireSubagentSession(childID)
			}
			if l.cleanup != nil {
				l.cleanup()
			}
			cancel()
		})
	}

	agentInfo := &bgtask.AgentInfo{Name: l.spec.Name, SessionID: childID, System: l.system, Model: l.spec.SelectedModelID}
	spec := bgtask.Spec{
		SessionID:       parentID,
		Kind:            bgtask.KindAgent,
		Label:           l.label,
		CWD:             l.spec.CWD,
		ToolCallID:      l.toolCallID,
		ExpectedSeconds: l.expectedSeconds,
		TimeoutSeconds:  l.timeoutSeconds,
		NotifyOnFinish:  l.notify,
		Agent:           agentInfo,
	}
	snap, err := pool.Launch(spec, func(taskID string, out io.Writer) (bgtask.Handle, error) {
		run.taskID = taskID
		run.out = out
		if l.relay != nil {
			// Assigned before the run goroutine is started below, so it is
			// visible there without a lock; a detached prompt names the task
			// it belongs to.
			l.relay.taskID = taskID
		}
		run.sender = newSubagentSender(out, l.relay)
		run.sender.onUsage = func(in, outTokens int) { pool.SetAgentUsage(parentID, taskID, in, outTokens) }
		verb := "starting"
		if l.spec.Resume {
			verb = "resuming"
		}
		_, _ = fmt.Fprintf(out, "subagent %s (task %s, session %s) %s\n", l.spec.Name, taskID, childID, verb)
		if l.modelNote != "" {
			_, _ = fmt.Fprintln(out, l.modelNote)
		}
		childSpec := l.spec
		childSpec.TaskID = taskID
		go a.executeSubagentRun(runCtx, rt, run, childSpec, out, finish)
		return handle, nil
	})
	if err != nil {
		finish()
		return snap, run, err
	}
	return snap, run, nil
}

// executeSubagentRun is executeChildRun for a spawn: the parent's logger, and
// the parent's SubagentStop hooks see the outcome before the report is sealed.
// A system child is not a delegation, so no hook describes it.
func (a *Agent) executeSubagentRun(ctx context.Context, rt SubagentRuntime, run *subagentRun, spec session.SubagentSpec, out io.Writer, finish func()) {
	var onStop func(status, report string, turns int)
	if !run.system {
		onStop = func(status, report string, turns int) {
			a.runSubagentStopHooks(context.WithoutCancel(ctx), run.parentMode, run.displayName(), run.childID, run.taskID, status, report, turns)
		}
	}
	executeChildRun(ctx, rt, run, spec, out, finish, a.log, onStop)
}

// executeChildRun creates the child session, drives its one turn and records
// the outcome. It runs on its own goroutine; the pool supervises through the
// handle, and a Stop or timeout that lands while the session is still being
// created cancels the creation through the run context. onStop, when set,
// sees the outcome before the report is written to the sink.
func executeChildRun(ctx context.Context, rt SubagentRuntime, run *subagentRun, spec session.SubagentSpec, out io.Writer, finish func(), log *slog.Logger, onStop func(status, report string, turns int)) {
	if log == nil {
		log = slog.Default()
	}
	exit := 1
	var st *session.State
	defer func() {
		if r := recover(); r != nil {
			run.mu.Lock()
			run.status = "failed"
			run.err = fmt.Errorf("subagent panicked: %v", r)
			run.mu.Unlock()
			log.Error("subagent run panicked", "agent", run.displayName(), "session", run.childID, "panic", r)
		}
		run.sender.Flush()
		_, _ = io.WriteString(out, formatSubagentReport(run, st))
		finish()
		run.handle.mu.Lock()
		run.handle.exit = exit
		if run.status == "failed" && run.err != nil {
			run.handle.err = run.err
		}
		run.handle.mu.Unlock()
		close(run.handle.done)
	}()

	created, err := rt.CreateSubagentSession(ctx, spec)
	if err != nil {
		run.mu.Lock()
		run.status = "failed"
		run.err = fmt.Errorf("%s: %w", errCreateChildSession, err)
		if ctx.Err() != nil {
			run.status = "cancelled"
		}
		run.mu.Unlock()
		return
	}
	st = created
	run.mu.Lock()
	run.created = true
	run.prior = messagesBesidesSummaries(st.GetMessages())
	run.mu.Unlock()

	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: run.prompt}}
	res, err := rt.RunSubagentTurn(ctx, run.childID, prompt, run.sender)

	run.mu.Lock()
	defer run.mu.Unlock()
	// A resumed child's transcript opens with the earlier runs; this run's
	// rounds and its report are what it wrote past its own prompt.
	written := runMessages(st.GetMessages(), run.prior)
	run.turns = assistantRounds(written)
	run.report = lastAssistantPlainText(written)
	switch {
	case err != nil:
		run.status = "failed"
		run.err = err
		if ctx.Err() != nil {
			run.status = "cancelled"
		}
	case res != nil && res.StopReason == acp.StopReasonCancelled:
		run.status = "cancelled"
	case res != nil && res.StopReason == acp.StopReasonRefused:
		run.status = "failed"
		run.err = fmt.Errorf("the subagent stopped without finishing (%s)", res.StopReason)
		if strings.TrimSpace(run.report) == "" {
			run.err = fmt.Errorf("the subagent produced no final message (stop reason %s)", res.StopReason)
		}
	case res != nil && res.StopReason == acp.StopReasonMaxTurns:
		run.status = "failed"
		run.err = fmt.Errorf("the subagent reached max_turns without a final answer")
		if strings.TrimSpace(run.report) != "" {
			// The text is the last one it wrote on the way, not its answer.
			run.err = fmt.Errorf("the subagent reached max_turns before its final answer; the report is the last text it wrote, not a conclusion")
		}
	case strings.TrimSpace(run.report) == "":
		run.status = "failed"
		run.err = fmt.Errorf("the subagent produced no final message")
	default:
		run.status = "end_turn"
		if res != nil {
			run.status = string(res.StopReason)
		}
		exit = 0
	}
	// SubagentStop hooks in the parent see the outcome before the report is
	// sealed, so a foreground parent reads a report the hook already saw.
	if onStop != nil {
		onStop(run.status, run.report, run.turns)
	}
}

// errCreateChildSession opens the error of a run whose child session could
// not be created: a run like that left no transcript to resume, and the wake
// it causes must not offer one (WakeInstruction).
const errCreateChildSession = "create subagent session"

// resumeTarget is the earlier run of this session a spawn_agent call
// continues: the child session that keeps its transcript and what the task
// rows recorded about it.
type resumeTarget struct {
	childID string
	// model is the model the child's latest run worked on.
	model string
}

// findResumeTarget resolves the run a spawn_agent resume names - a task id or
// a child session id - among this session's own agent tasks: those the pool
// holds and those the bundle recorded for an earlier process. Only a finished
// delegation to the named subagent qualifies. A run of the child still in
// flight is refused, and so is the runtime's own memory child, whose rows say
// so; a child of another session is never among these rows at all.
func (a *Agent) findResumeTarget(ref, agentName string) (*resumeTarget, error) {
	parentID := a.state.GetID()
	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())
	rows := a.backgroundPool(sd).SessionTasks(parentID, sd)
	childID := ""
	for _, row := range rows {
		if row.Kind != bgtask.KindAgent || row.Agent == nil || strings.TrimSpace(row.Agent.SessionID) == "" {
			continue
		}
		if row.ID == ref || row.Agent.SessionID == ref {
			childID = row.Agent.SessionID
			break
		}
	}
	if childID == "" {
		return nil, fmt.Errorf("spawn_agent: no subagent run of this session matches resume %q; pass the task id (bg_...) or the child session id (sess_...) an earlier spawn_agent result named", ref)
	}
	var latest *bgtask.Snapshot
	for i := range rows {
		row := &rows[i]
		if row.Kind != bgtask.KindAgent || row.Agent == nil || row.Agent.SessionID != childID {
			continue
		}
		if row.Agent.System {
			return nil, fmt.Errorf("spawn_agent: %s is a %s run the runtime started on its own, not a subagent this session delegated to; it cannot be resumed", ref, row.Agent.Name)
		}
		if !row.Status.Finished() {
			return nil, fmt.Errorf("spawn_agent: subagent %s is still running as task %s; wait for it with background_wait or stop it with background_stop before you resume it", row.Agent.Name, row.ID)
		}
		if latest == nil || row.StartedAt.After(latest.StartedAt) {
			latest = row
		}
	}
	if name := latest.Agent.Name; name != agentName {
		return nil, fmt.Errorf("spawn_agent: %s is a run of subagent %q, not %q; pass agent %q to resume it", ref, name, agentName, name)
	}
	// The transcript's paths and results belong to the workspace the child
	// worked in, and trust and the tool set are decided for this session's
	// workspace now: a child of another workspace is not continued here.
	if was, now := strings.TrimSpace(latest.CWD), strings.TrimSpace(a.state.GetCWD()); was != "" && now != "" &&
		!session.SameDirectory(was, now) {
		return nil, fmt.Errorf("spawn_agent: subagent %s worked in %s and this session works in %s now; its transcript belongs to that workspace, so start a new subagent for the task here", latest.Agent.Name, was, now)
	}
	// A run whose child session was never created left no transcript, and a
	// child deleted since has none any more.
	onDisk, err := session.ChildTranscriptOnDisk(sd, childID, latest.Agent.Name)
	if err != nil {
		return nil, fmt.Errorf("spawn_agent: cannot read the transcript of subagent %s (session %s): %w", latest.Agent.Name, childID, err)
	}
	if !onDisk {
		return nil, fmt.Errorf("spawn_agent: the transcript of subagent %s (session %s) is not on disk: its run never got its session, or the session was deleted since; start a new subagent for the task", latest.Agent.Name, childID)
	}
	return &resumeTarget{childID: childID, model: latest.Agent.Model}, nil
}

// resumeHint tells the parent how to go on with a run that ended without its
// report: its transcript keeps the work it did, so the same child continues
// it rather than a new one starting over on the same task from an empty
// context (issue #389). Callers hold run.mu.
func resumeHint(run *subagentRun) string {
	var b strings.Builder
	if llm.IsTransientProviderError(run.err) {
		b.WriteString("The run was cut by a failure of the model provider, not by its task. ")
	}
	fmt.Fprintf(&b, "Its transcript keeps the work it did: to go on with this task, call spawn_agent with resume=%q and agent %q and a prompt that says what to do now, instead of starting a new subagent.",
		run.taskID, run.displayName())
	return b.String()
}

// runMessages is what one run wrote into a child's transcript: everything
// after the run's own prompt. prior is how many messages other than
// compaction summaries the transcript held when the run started. A
// compaction during the run inserts summaries and removes nothing, so the
// messages that are not summaries keep their order, the first prior of them
// are the earlier runs', and the next one is this run's prompt, wherever the
// summaries landed and whatever the prompt says. A run that never got its
// prompt in wrote nothing.
func runMessages(msgs []llm.Message, prior int) []llm.Message {
	seen := 0
	for i, m := range msgs {
		if m.CompactionSummary {
			continue
		}
		if seen < prior {
			seen++
			continue
		}
		if m.Role == llm.RoleUser {
			return msgs[i+1:]
		}
		// Not the prompt a run starts with: keep it, it is the run's.
		return msgs[i:]
	}
	return nil
}

// messagesBesidesSummaries counts the messages of a transcript that are not
// compaction summaries: the measure runMessages reads a run's part by.
func messagesBesidesSummaries(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if !m.CompactionSummary {
			n++
		}
	}
	return n
}

// parentSessionMCPDeclarations returns the ACP client-supplied MCP declarations
// of this session, so a child can redial them.
func (a *Agent) parentSessionMCPDeclarations() []config.MCPServerConfig {
	if st := sessionStatePtr(a.state); st != nil {
		return st.SessionMCPDeclarations()
	}
	return nil
}

// formatSubagentReport renders the block the sink and the parent both read.
func formatSubagentReport(run *subagentRun, st *session.State) string {
	run.mu.Lock()
	defer run.mu.Unlock()
	var b strings.Builder
	b.WriteString("\n=== subagent report ===\n")
	fmt.Fprintf(&b, "agent: %s | task: %s | session: %s | outcome: %s | turns: %d | duration: %s\n",
		run.displayName(), run.taskID, run.childID, run.status, run.turns, humanSecondsAgent(int(time.Since(run.startedAt).Round(time.Second)/time.Second)))
	if run.err != nil {
		fmt.Fprintf(&b, "error: %v\n", run.err)
	}
	if run.offersResume() && run.unfinished() {
		b.WriteString(resumeHint(run))
		b.WriteString("\n")
	}
	b.WriteString("--- report ---\n")
	report := strings.TrimSpace(run.report)
	if report == "" && st != nil {
		report = strings.TrimSpace(lastAssistantPlainText(runMessages(st.GetMessages(), run.prior)))
	}
	if report == "" {
		report = "(the subagent produced no final message)"
	}
	b.WriteString(report)
	b.WriteString("\n")
	return b.String()
}

// formatForegroundResult is the spawn_agent tool result of a run the parent
// waited for: the report inside an envelope naming the task, the child session
// and the pool's verdict, CDATA-wrapped so child output cannot break it.
func formatForegroundResult(run *subagentRun, snap bgtask.Snapshot) string {
	run.mu.Lock()
	report := strings.TrimSpace(run.report)
	runErr := run.err
	runStatus := run.status
	turns := run.turns
	hint := ""
	if run.offersResume() {
		hint = resumeHint(run)
	}
	run.mu.Unlock()
	if report == "" {
		report = "(the subagent produced no final message)"
	}
	status := string(snap.Status)
	if status == "" {
		status = runStatus
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<subagent task=%q session=%q agent=%q status=%q turns=\"%d\">\n", run.taskID, run.childID, run.displayName(), status, turns)
	b.WriteString(wrapXMLCDATA(report))
	b.WriteString("\n</subagent>\n")
	if runErr != nil {
		fmt.Fprintf(&b, "The run ended with an error: %v\n", runErr)
	}
	if snap.Status != "" && snap.Status.Finished() && snap.Status != bgtask.StatusSucceeded {
		fmt.Fprintf(&b, "The subagent did not succeed (status %s); treat its report accordingly.\n", snap.Status)
		if hint != "" {
			b.WriteString(hint)
			b.WriteString("\n")
		}
	}
	b.WriteString("The user did not see this report: restate what matters in your own reply. ")
	fmt.Fprintf(&b, "The full transcript is session %s (Tasks panel → Show transcript).", run.childID)
	return b.String()
}

// maxTaskLabelRunes is the pool's label rule: a task label is one short line.
const maxTaskLabelRunes = 60

// firstLine trims text to its first line.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	return s
}

// capRunes truncates s to at most n runes, marking the cut with an ellipsis.
func capRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// assistantRounds counts the ReAct rounds of a transcript: one per assistant
// message, whether it carried tool calls or the final answer.
func assistantRounds(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant {
			n++
		}
	}
	return n
}

// humanSecondsAgent mirrors the shell package's rendering without importing it.
func humanSecondsAgent(seconds int) string {
	switch {
	case seconds < 0:
		return "0s"
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		if rem := seconds % 60; rem != 0 {
			return fmt.Sprintf("%dm%ds", seconds/60, rem)
		}
		return fmt.Sprintf("%dm", seconds/60)
	default:
		if rem := (seconds % 3600) / 60; rem != 0 {
			return fmt.Sprintf("%dh%dm", seconds/3600, rem)
		}
		return fmt.Sprintf("%dh", seconds/3600)
	}
}

// configuredModels lists the models[].model ids of the configuration.
func configuredModels(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Models))
	for i := range cfg.Models {
		out = append(out, cfg.Models[i].Model)
	}
	return out
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
