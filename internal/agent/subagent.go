package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
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
// prompt path, and retire it afterwards. session.Manager implements it; a
// surface that has no manager (a scheduler run) leaves it unset and the
// spawn_agent tool answers that subagents are unavailable.
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

// Mandatory exclusions from every child's tool set: a child cannot ask the
// user, cannot rewrite the agent's own configuration, and cannot leave plan
// mode on the operator's behalf.
var subagentMandatoryExclusions = []string{
	"question",
	"config_set", "config_changes", "config_commit", "config_revert", "config_rollback",
	"plan_exit",
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
// sender while the parent turn that spawned the child is still alive, and
// fails closed afterwards. It is created per spawn, so a child spawned by a
// later turn carries that turn's context.
type permissionRelay struct {
	parent           acp.UpdateSender
	parentSessionID  string
	parentSessionDir string
	agentName        string
	// childPermissionMode is the child's effective mode, stamped on every
	// forwarded request so a sender never mistakes it for the parent's.
	childPermissionMode string
	turnCtx             context.Context
	childCtx            context.Context
	arbiter             *permissionArbiter
}

func deniedPermission() *acp.PermissionResult {
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}
}

// Request forwards one permission prompt. The parent context ending, the child
// being stopped, or the caller's own context ending all resolve as a denial
// without leaving a goroutine parked on the transport.
func (r *permissionRelay) Request(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	if r == nil || r.parent == nil || r.turnCtx.Err() != nil {
		return deniedPermission(), nil
	}
	r.arbiter.waiting.Add(1)
	select {
	case r.arbiter.slot <- struct{}{}:
		r.arbiter.waiting.Add(-1)
	case <-r.turnCtx.Done():
		r.arbiter.waiting.Add(-1)
		return deniedPermission(), nil
	case <-r.childCtx.Done():
		r.arbiter.waiting.Add(-1)
		return deniedPermission(), nil
	case <-ctx.Done():
		r.arbiter.waiting.Add(-1)
		return deniedPermission(), nil
	}
	defer func() { <-r.arbiter.slot }()
	if r.turnCtx.Err() != nil {
		return deniedPermission(), nil
	}

	params.SessionID = r.parentSessionID
	params.EffectivePermissionMode = r.childPermissionMode
	title := strings.TrimSpace(params.ToolCall.Title)
	if title == "" {
		title = "Run a tool"
	}
	params.ToolCall.Title = fmt.Sprintf("[subagent %s] %s", r.agentName, title)

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
	// A forwarded prompt the parent never answers leaves the HTTP bridge's
	// pending_permission.json in the parent bundle; the permission route could
	// never clear it because the tool call belongs to the child, so the relay
	// clears it itself on every path that gives up.
	abandon := func() *acp.PermissionResult {
		cancel()
		if r.parentSessionDir != "" {
			_ = session.ClearPendingPermission(r.parentSessionDir)
		}
		return deniedPermission()
	}
	select {
	case out := <-done:
		if out.err != nil || out.res == nil {
			return abandon(), nil
		}
		return out.res, nil
	case <-r.turnCtx.Done():
		return abandon(), nil
	case <-r.childCtx.Done():
		return abandon(), nil
	case <-ctx.Done():
		return abandon(), nil
	}
}

// subagentSender is the child's acp.UpdateSender: progress goes to the task's
// output sink, permission requests to the relay, questions nowhere.
type subagentSender struct {
	out   io.Writer
	relay *permissionRelay

	mu       sync.Mutex
	line     strings.Builder
	toolName map[string]string
}

func newSubagentSender(out io.Writer, relay *permissionRelay) *subagentSender {
	return &subagentSender{out: out, relay: relay, toolName: map[string]string{}}
}

// SendSessionUpdate renders the child's stream as compact log lines. Text is
// flushed per line so the operator can follow the child in the Tasks panel.
func (s *subagentSender) SendSessionUpdate(_ string, update interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch u := update.(type) {
	case acp.MessageChunkUpdate:
		if u.Content.Type != acp.ContentTypeText || u.Content.Text == "" {
			return nil
		}
		s.line.WriteString(u.Content.Text)
		s.flushLines(false)
	case acp.ToolCallUpdate:
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
	return nil
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

func (s *subagentSender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return s.relay.Request(ctx, params)
}

func (s *subagentSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return nil, fmt.Errorf("a subagent cannot ask the user questions; finish with a report instead")
}

// subagentHandle is the pool's view of a child run: Stop cancels the child,
// Wait blocks until the run settled, and there is no OS process behind it.
type subagentHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	exit   int
}

func (h *subagentHandle) Wait() (int, error) {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exit, nil
}

func (h *subagentHandle) Stop(time.Duration) error {
	h.cancel()
	return nil
}

func (h *subagentHandle) PID() int                    { return 0 }
func (h *subagentHandle) ProcessStartedAt() time.Time { return time.Time{} }

// subagentRun is the bookkeeping of one spawn from the parent's side.
type subagentRun struct {
	def       *subagents.Definition
	childID   string
	taskID    string
	prompt    string
	handle    *subagentHandle
	sender    *subagentSender
	report    string
	startedAt time.Time
	turns     int
	status    string // end_turn | cancelled | failed | ...
	err       error
	mu        sync.Mutex
}

// subagentDepth is how deep this agent's session sits in a spawn tree.
func (a *Agent) subagentDepth() int {
	if a.subagent == nil {
		return 0
	}
	return a.subagent.Depth
}

// canSpawn reports whether this session may spawn at all: the feature is on, a
// runtime is wired, and the depth limit leaves room.
func (a *Agent) canSpawn() bool {
	if a.cfg == nil || !a.cfg.Subagents.ResolvedEnabled() || a.subagentRuntime == nil {
		return false
	}
	return a.subagentDepth() < a.cfg.Subagents.EffectiveMaxDepth()
}

// applySubagentEnv wires the spawn hook and the depth into a tool env.
func (a *Agent) applySubagentEnv(env *tools.Env) {
	env.SubagentDepth = a.subagentDepth()
	if a.canSpawn() {
		env.SpawnAgent = a.spawnSubagent
	} else {
		env.SpawnAgent = nil
	}
}

// subagentDefinitions loads the definitions visible for this session's cwd.
func (a *Agent) subagentDefinitions() []*subagents.Definition {
	loader := subagents.NewLoader(a.cfg.Subagents.Dirs, a.cfg.Subagents.ResolvedProjectTrust())
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
	entries := subagents.BuildCatalog(a.subagentDefinitions(), a.cfg.Subagents.ResolvedProjectTrust(),
		subagents.CanonicalWorkspace(cwd), subagents.NewTrustStore(a.cfg.Paths.Home))
	return subagents.PromptBlock(entries)
}

// subagentRoleBlock renders the child's role section.
func (a *Agent) subagentRoleBlock() string {
	if a.subagent == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are running as the subagent **%s**, spawned by a parent agent to complete one self-contained task. ", a.subagent.Name)
	b.WriteString("You see nothing of the parent's conversation: work from the task below and from the workspace. ")
	b.WriteString("You cannot ask the user questions; if something blocks you, say so in your report. ")
	b.WriteString("Only your final message reaches the parent, so finish with a concise report of what you did, what you found (with file paths), and what remains.")
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
	rt := a.subagentRuntime
	if rt == nil {
		return "", fmt.Errorf("subagents are not available in this session")
	}
	cfg := a.cfg
	if !cfg.Subagents.ResolvedEnabled() {
		return "", fmt.Errorf("subagents are disabled (subagents.enabled is false)")
	}
	maxDepth := cfg.Subagents.EffectiveMaxDepth()
	if a.subagentDepth() >= maxDepth {
		return "", fmt.Errorf("this session cannot spawn subagents: subagents.max_depth is %d and this session already runs at depth %d", maxDepth, a.subagentDepth())
	}
	if len(req.Prompt) > subagents.MaxPromptBytes {
		return "", fmt.Errorf("spawn_agent: prompt is %d bytes, the limit is %d", len(req.Prompt), subagents.MaxPromptBytes)
	}

	defs := a.subagentDefinitions()
	def := subagents.FindByName(defs, req.Agent)
	if def == nil {
		return "", fmt.Errorf("unknown subagent %q; available: %s", req.Agent, strings.Join(subagents.VisibleNames(defs), ", "))
	}
	cwd := a.state.GetCWD()
	workspace := subagents.CanonicalWorkspace(cwd)
	policy := cfg.Subagents.ResolvedProjectTrust()
	store := subagents.NewTrustStore(cfg.Paths.Home)
	if subagents.Decide(def, policy, workspace, store) == subagents.TrustNeedsApproval {
		return "", fmt.Errorf("subagent %q comes from a project file (%s) that is not approved for this workspace; "+
			"ask the user to approve it with `coddy agents trust %s` (or POST /coddy/subagents/%s/trust), "+
			"or to set subagents.project_trust to allow for this checkout, then try again",
			def.Name, def.Path, def.Name, def.Name)
	}

	// A read-only parent (plan, ask) forces its own mode on the child; only
	// an agent-mode parent lets the definition pick. Ask mode never offers
	// spawn_agent in the first place, so the ask branch is defence in depth.
	parentMode := a.state.GetMode()
	childMode := parentMode
	if def.Mode != "" && parentMode != "plan" && parentMode != "ask" {
		childMode = def.Mode
	}
	childPerm := subagents.NarrowPermissionMode(effectivePermMode(a.state, cfg), def.PermissionMode)
	childDepth := a.subagentDepth() + 1

	exclusions := append([]string(nil), subagentMandatoryExclusions...)
	if childDepth >= maxDepth {
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
	if def.Model != "" {
		if cfg.FindModelEntry(def.Model) != nil {
			model = def.Model
		} else {
			unknownModel = def.Model
			a.log.Warn("subagent definition names an unknown model; the parent's model is used", "agent", def.Name, "model", def.Model, "using", model)
		}
	}

	subagentLimiter.SetLimit(cfg.Subagents.EffectiveMaxConcurrent())
	release, ok := subagentLimiter.TryAcquire()
	if !ok {
		return "", fmt.Errorf("cannot start subagent %q: subagents.max_concurrent (%d) runs are already in flight; wait for one with background_wait or background_list, then try again",
			def.Name, cfg.Subagents.EffectiveMaxConcurrent())
	}

	background := req.Background || def.Background
	notify := req.NotifyOnFinish && background && a.subagentDepth() == 0
	label := firstLine(req.Description)
	if label == "" {
		label = firstLine(req.Prompt)
	}
	label = capRunes("agent "+def.Name+": "+label, maxTaskLabelRunes)
	timeout := subagents.ResolveTimeoutSeconds(req.TimeoutSeconds, def.TimeoutSeconds, req.ExpectedSeconds, cfg.Subagents.EffectiveDefaultTimeoutSeconds())

	parentID := a.state.GetID()
	childID := session.NewSubagentSessionID()
	sd := strings.TrimSpace(a.state.GetPersistedSessionDir())
	pool := a.backgroundPool(sd)

	base := ctx
	if background {
		base = context.WithoutCancel(ctx)
	}
	runCtx, cancel := context.WithCancel(base)
	handle := &subagentHandle{cancel: cancel, done: make(chan struct{})}
	arbiter := acquireArbiter(parentID)
	relay := &permissionRelay{
		parent:              a.server,
		parentSessionID:     parentID,
		parentSessionDir:    sd,
		agentName:           def.Name,
		childPermissionMode: childPerm,
		turnCtx:             ctx,
		childCtx:            runCtx,
		arbiter:             arbiter,
	}
	run := &subagentRun{def: def, childID: childID, prompt: req.Prompt, handle: handle, startedAt: time.Now()}

	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			// Work the child launched settles before its transcript is sealed.
			pool.StopSession(childID)
			rt.RetireSubagentSession(childID)
			releaseArbiter(parentID)
			release()
			cancel()
		})
	}

	spec := bgtask.Spec{
		SessionID:       parentID,
		Kind:            bgtask.KindAgent,
		Label:           label,
		CWD:             cwd,
		ToolCallID:      a.currentToolCallID,
		ExpectedSeconds: req.ExpectedSeconds,
		TimeoutSeconds:  timeout,
		NotifyOnFinish:  notify,
		Agent:           &bgtask.AgentInfo{Name: def.Name, SessionID: childID},
	}
	// The callback hands the handle back at once and does everything that can
	// block (session creation, MCP dialing, the turn itself) on the run
	// goroutine, so the pool's Stop and timeout reach a child that is still
	// being created. The task id is known before anything starts.
	snap, err := pool.Launch(spec, func(taskID string, out io.Writer) (bgtask.Handle, error) {
		run.taskID = taskID
		run.sender = newSubagentSender(out, relay)
		_, _ = fmt.Fprintf(out, "subagent %s (task %s, session %s) starting\n", def.Name, taskID, childID)
		if unknownModel != "" {
			_, _ = fmt.Fprintf(out, "model %q is not configured; using the parent's model %q\n", unknownModel, model)
		}
		childSpec := session.SubagentSpec{
			ID:               childID,
			ParentSessionID:  parentID,
			Name:             def.Name,
			TaskID:           taskID,
			CWD:              cwd,
			Mode:             childMode,
			PermissionMode:   childPerm,
			SelectedModelID:  model,
			Title:            label,
			Role:             def.Role,
			Tools:            effective,
			Depth:            childDepth,
			MaxTurns:         def.MaxTurns,
			ConnectMCP:       connectMCP,
			ClientMCPServers: a.parentSessionMCPDeclarations(),
		}
		go a.executeSubagentRun(runCtx, rt, run, childSpec, out, finish)
		return handle, nil
	})
	if err != nil {
		finish()
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
	fmt.Fprintf(&b, "Started subagent %s as background task %s (child session %s).\n", def.Name, snap.ID, childID)
	fmt.Fprintf(&b, "Hard timeout %s.\n", humanSecondsAgent(snap.TimeoutSeconds))
	if snap.NotifyOnFinish {
		b.WriteString("You will be woken with the outcome when it finishes, so you can end your turn now.")
	} else {
		b.WriteString("Keep working; follow it with background_list or background_output, and collect the report with background_wait.")
	}
	return b.String(), nil
}

// executeSubagentRun creates the child session, drives its one turn and
// records the outcome. It runs on its own goroutine; the pool supervises
// through the handle, and a Stop or timeout that lands while the session is
// still being created cancels the creation through the run context.
func (a *Agent) executeSubagentRun(ctx context.Context, rt SubagentRuntime, run *subagentRun, spec session.SubagentSpec, out io.Writer, finish func()) {
	exit := 1
	var st *session.State
	defer func() {
		if r := recover(); r != nil {
			run.mu.Lock()
			run.status = "failed"
			run.err = fmt.Errorf("subagent panicked: %v", r)
			run.mu.Unlock()
			a.log.Error("subagent run panicked", "agent", run.def.Name, "session", run.childID, "panic", r)
		}
		run.sender.Flush()
		_, _ = io.WriteString(out, formatSubagentReport(run, st))
		finish()
		run.handle.mu.Lock()
		run.handle.exit = exit
		run.handle.mu.Unlock()
		close(run.handle.done)
	}()

	created, err := rt.CreateSubagentSession(ctx, spec)
	if err != nil {
		run.mu.Lock()
		run.status = "failed"
		run.err = fmt.Errorf("create subagent session: %w", err)
		if ctx.Err() != nil {
			run.status = "cancelled"
		}
		run.mu.Unlock()
		return
	}
	st = created

	prompt := []acp.ContentBlock{{Type: acp.ContentTypeText, Text: run.prompt}}
	res, err := rt.RunSubagentTurn(ctx, run.childID, prompt, run.sender)

	run.mu.Lock()
	defer run.mu.Unlock()
	run.turns = assistantRounds(st.GetMessages())
	run.report = lastAssistantPlainText(st.GetMessages())
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
	default:
		run.status = "end_turn"
		if res != nil {
			run.status = string(res.StopReason)
		}
		exit = 0
	}
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
		run.def.Name, run.taskID, run.childID, run.status, run.turns, humanSecondsAgent(int(time.Since(run.startedAt).Round(time.Second)/time.Second)))
	if run.err != nil {
		fmt.Fprintf(&b, "error: %v\n", run.err)
	}
	b.WriteString("--- report ---\n")
	report := strings.TrimSpace(run.report)
	if report == "" && st != nil {
		report = strings.TrimSpace(lastAssistantPlainText(st.GetMessages()))
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
	turns := run.turns
	run.mu.Unlock()
	if report == "" {
		report = "(the subagent produced no final message)"
	}
	status := string(snap.Status)
	if status == "" {
		status = run.status
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<subagent task=%q session=%q agent=%q status=%q turns=\"%d\">\n", run.taskID, run.childID, run.def.Name, status, turns)
	b.WriteString(wrapXMLCDATA(report))
	b.WriteString("\n</subagent>\n")
	if runErr != nil {
		fmt.Fprintf(&b, "The run ended with an error: %v\n", runErr)
	}
	if snap.Status != "" && snap.Status.Finished() && snap.Status != bgtask.StatusSucceeded {
		fmt.Fprintf(&b, "The subagent did not succeed (status %s); treat its report accordingly.\n", snap.Status)
	}
	b.WriteString("The user did not see this report: restate what matters in your own reply. ")
	fmt.Fprintf(&b, "The full transcript is session %s (Tasks panel → Open transcript).", run.childID)
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
