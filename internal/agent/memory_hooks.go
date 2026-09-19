//go:build memory

package agent

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"

	"github.com/EvilFreelancer/coddy-agent/external/memory"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// registerMemoryChildTools gives the memory subagent its tools: the six
// coddy_memory_* tools over the two note roots of the session's cwd, added to
// this Agent's registry when the session is the system memory child. An
// ordinary session never gets them, so the main model never sees a memory
// tool; the child's own allowlist decides which of the six it may call.
func (a *Agent) registerMemoryChildTools() {
	if a.subagent == nil || a.subagent.Kind != session.SubagentKindMemory {
		return
	}
	tools, err := memory.Tools(a.cfg, a.state.GetCWD())
	if err != nil {
		a.log.Warn("memory subagent tools unavailable", "error", err)
		return
	}
	for _, t := range tools {
		a.registry.Register(t)
	}
}

// runMemoryBeforeTurn starts the memory subagent of this user turn as a
// system task of the pool and waits a bounded time for its report. mode is
// the snapshot Run captured: an ask-mode turn gets a recall-only child.
func (a *Agent) runMemoryBeforeTurn(ctx context.Context, userText, mode string) {
	if a.cfg == nil || !a.cfg.Memory.Enabled || a.subagent != nil {
		return
	}
	parentID := a.state.GetID()
	skip := func(reason string) {
		a.log.Warn("memory run skipped", "session_id", parentID, "reason", reason)
		a.sendMemoryRun(acp.MemoryRunUpdate{Status: "skipped", Reason: reason})
	}
	rt := a.subagentRuntime
	if rt == nil {
		skip("this surface has no session manager to run a memory subagent in")
		return
	}
	release, reason := acquireMemorySlot(parentID)
	if release == nil {
		skip(reason)
		return
	}

	cfg := a.cfg
	readOnly := mode == string(session.ModeAsk)
	model := a.state.EffectiveModelID(cfg)
	if m := strings.TrimSpace(cfg.Memory.Model); m != "" && cfg.FindModelEntry(m) != nil {
		model = m
	} else if m != "" {
		a.log.Warn("memory.model is not configured; the session's model runs the memory subagent", "model", m, "using", model)
	}
	// A cut addendum is said at every launch that reads it: the run is one
	// per turn already, and a silent cut is what the cap must not be.
	addendum, cut := cfg.Memory.EffectiveAdditionalPrompt()
	if cut {
		a.log.Warn("memory.additional_prompt is longer than additional_prompt_max_chars; the memory subagent reads the first characters only",
			"session_id", parentID, "max_chars", cfg.Memory.AdditionalPromptMaxChars, "chars", utf8.RuneCountInString(cfg.Memory.AdditionalPrompt))
	}
	childID := session.NewSessionID()
	label := memoryTaskLabel(userText)
	// The deadline is taken before the launch: the child is created on the
	// run goroutine, inside the wait, and the wait never outlasts the run's
	// own timeout.
	wait := time.Duration(min(cfg.Memory.EffectiveWaitSeconds(), cfg.Memory.EffectiveTimeoutSeconds())) * time.Second
	deadline := time.Now().Add(wait)

	snap, run, err := a.launchChildRun(ctx, rt, childLaunch{
		spec: session.SubagentSpec{
			ID:              childID,
			ParentSessionID: parentID,
			Name:            session.SubagentKindMemory,
			CWD:             a.state.GetCWD(),
			Mode:            string(session.ModeAgent),
			PermissionMode:  effectivePermMode(a.state, cfg),
			SelectedModelID: model,
			Title:           label,
			Role:            addendum,
			Tools:           memory.ToolNames(readOnly),
			Depth:           a.subagentDepth() + 1,
			MaxTurns:        cfg.Memory.EffectiveMaxTurns(),
			Kind:            session.SubagentKindMemory,
			PromptTemplate:  memory.PromptTemplate(readOnly),
			MaxTokens:       cfg.Memory.CopilotMaxTokens,
			FallbackModels:  memoryFallbackModels(cfg.Memory.FallbackModels, a.state.EffectiveModelID(cfg)),
		},
		prompt:         memory.TaskMessage(userText),
		parentMode:     mode,
		label:          label,
		timeoutSeconds: cfg.Memory.EffectiveTimeoutSeconds(),
		detached:       true,
		system:         true,
		cleanup:        release,
	})
	if err != nil {
		skip(err.Error())
		return
	}
	mr := &memoryTurnRun{
		parentID:  parentID,
		taskID:    snap.ID,
		childID:   childID,
		run:       run,
		pool:      a.backgroundPool(strings.TrimSpace(a.state.GetPersistedSessionDir())),
		startedAt: time.Now(),
	}
	a.memoryRun = mr
	a.log.Info("memory run started", "session_id", parentID, "task", snap.ID, "child", childID, "model", model, "wait", wait)
	a.sendMemoryRun(acp.MemoryRunUpdate{Status: "started", TaskID: snap.ID, ChildSessionID: childID})
	go a.watchMemoryRun(mr, rt, cfg.Memory.EffectiveKeepRuns())

	if wait <= 0 {
		return
	}
	// A Stop of the turn ends the wait through ctx; the run itself is
	// detached and goes on.
	_, _ = mr.pool.Wait(ctx, parentID, snap.ID, time.Until(deadline))
	a.deliverMemoryReport("first request")
}

// memoryFallbackModels is the chain behind the memory model: the configured
// fallbacks, then the session's own model as the last resort.
func memoryFallbackModels(configured []string, sessionModel string) []string {
	out := make([]string, 0, len(configured)+1)
	for _, m := range configured {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	if sessionModel = strings.TrimSpace(sessionModel); sessionModel != "" {
		out = append(out, sessionModel)
	}
	return out
}

// watchMemoryRun waits for the task to settle, on its own goroutine, and then
// applies the retention rule to the session's finished memory runs. It never
// touches the turn's sender: a run that settles after the turn returned has
// no stream to report to (the HTTP bridge's response is gone by then), so the
// finished update goes out from the loop's own goroutine, at delivery or at
// the turn's end, or not at all. The drawer is the record either way.
func (a *Agent) watchMemoryRun(mr *memoryTurnRun, rt SubagentRuntime, keep int) {
	if _, err := mr.pool.Wait(context.Background(), mr.parentID, mr.taskID, 0); err == nil {
		mr.mu.Lock()
		if mr.turnOver {
			mr.settled = true
		}
		mr.mu.Unlock()
	}
	a.pruneMemoryRuns(mr.pool, mr.parentID, rt, keep)
}

// memoryRunSnapshots lists the finished memory runs of a session, the live
// pool and the records of earlier processes merged, oldest first.
func memoryRunSnapshots(pool *bgtask.Pool, sessionID, sessionDir string) []bgtask.Snapshot {
	seen := map[string]bool{}
	var out []bgtask.Snapshot
	add := func(snap bgtask.Snapshot) {
		if seen[snap.ID] || !snap.SystemTask() || snap.Agent.Name != session.SubagentKindMemory || !snap.Status.Finished() {
			return
		}
		seen[snap.ID] = true
		out = append(out, snap)
	}
	for _, snap := range pool.List(sessionID) {
		add(snap)
	}
	for _, snap := range bgtask.LoadPersisted(sessionDir) {
		add(snap)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// pruneMemoryRuns removes the finished memory runs of a session beyond the
// newest keep, task record and child bundle alike. keep 0 keeps everything.
// A child that is still live or still owns a task is left alone. The run of
// the turn in flight is never among the pruned: turns are serialised by the
// turn lock, so it is the newest run of the session and stays inside the
// kept tail, and a record an earlier process left behind is older than any
// run this one started.
func (a *Agent) pruneMemoryRuns(pool *bgtask.Pool, sessionID string, rt SubagentRuntime, keep int) {
	if keep <= 0 || pool == nil {
		return
	}
	sessionDir := strings.TrimSpace(a.state.GetPersistedSessionDir())
	runs := memoryRunSnapshots(pool, sessionID, sessionDir)
	if len(runs) <= keep {
		return
	}
	remover, _ := rt.(interface {
		RemoveRetiredChild(childID string, pool *bgtask.Pool) error
	})
	for _, snap := range runs[:len(runs)-keep] {
		if remover != nil && snap.Agent != nil && snap.Agent.SessionID != "" {
			if err := remover.RemoveRetiredChild(snap.Agent.SessionID, pool); err != nil {
				a.log.Warn("memory run retention: child bundle kept", "session_id", sessionID, "task", snap.ID, "child", snap.Agent.SessionID, "error", err)
				continue
			}
		}
		if err := pool.Forget(sessionID, snap.ID); err != nil {
			a.log.Warn("memory run retention: task record kept", "session_id", sessionID, "task", snap.ID, "error", err)
		}
	}
}
