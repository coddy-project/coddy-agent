package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// ScheduledRunSpec describes one unattended run of a scheduler job: the same
// thing a spawn_agent call starts - a task of kind agent in the pool, backed by
// a child session - with the scheduler in the parent's place. The daemon
// decides everything here (the job session it hangs under, the definition and
// its trust, the mode, the permission mode) before calling RunScheduledJob.
type ScheduledRunSpec struct {
	// JobID is the scheduler job (the file basename under scheduler.dir).
	JobID string
	// JobSessionID is the job session the run is a child of; it is live in
	// the manager when this is called.
	JobSessionID string
	// JobSessionDir is that session's bundle directory, where the pool keeps
	// the run's task record and output log.
	JobSessionDir string
	// RunSessionID is the pre-minted id of the run session, so the caller can
	// answer with it before the run has done anything.
	RunSessionID string
	// Label is the task label and the run session's title: the job and how
	// the run started.
	Label string
	// Trigger is "cron" or "manual"; FireSlot the committed UTC minute of a
	// cron fire, zero for a manual run.
	Trigger  string
	FireSlot time.Time
	// CWD is the job's resolved working directory.
	CWD string
	// Mode is agent, plan or ask; anything else is agent.
	Mode string
	// Model is the job's model override; empty follows the definition, then
	// the configuration.
	Model string
	// PermissionMode is what the run may do without asking: ask, accept_edits
	// or bypass. Empty is bypass, the unattended default. A definition can
	// only narrow it.
	PermissionMode string
	// Instruction is the job body, the run's one prompt.
	Instruction string
	// TimeoutSeconds is scheduler.timeout; the pool still caps it.
	TimeoutSeconds int
	// Definition is the subagent definition the job named, already resolved
	// and trusted by the caller; nil for a general run.
	Definition *subagents.Definition
	// MCPServerNames are the configured MCP servers the trust gate admits for
	// CWD; they decide whether the run dials MCP at all under a definition.
	MCPServerNames []string
}

// RunScheduledJob starts one run of a scheduler job as a background task of
// its job session and returns as soon as the task is registered; the run goes
// on under the pool's supervision. The run's tool set is resolved once its MCP
// clients are up (session.SubagentSpec.ResolveTools), its permission requests
// are denied unless its mode bypasses the gate, and every exit path - creation
// failure, stop, timeout, panic, completion - retires the run session, lets go
// of its pool entries and closes its clients.
func RunScheduledJob(ctx context.Context, cfg *config.Config, rt SubagentRuntime, pool *bgtask.Pool, log *slog.Logger, spec ScheduledRunSpec) (bgtask.Snapshot, error) {
	if cfg == nil {
		return bgtask.Snapshot{}, fmt.Errorf("scheduled run: configuration is required")
	}
	if rt == nil {
		return bgtask.Snapshot{}, fmt.Errorf("scheduled run: a session manager is required")
	}
	if pool == nil {
		pool = bgtask.Default()
	}
	if log == nil {
		log = slog.Default()
	}
	jobID := strings.TrimSpace(spec.JobID)
	jobSessionID := strings.TrimSpace(spec.JobSessionID)
	runID := strings.TrimSpace(spec.RunSessionID)
	if jobID == "" || jobSessionID == "" || runID == "" {
		return bgtask.Snapshot{}, fmt.Errorf("scheduled run: job id, job session and run session are required")
	}
	if err := session.ValidateFolderSessionID(runID); err != nil {
		return bgtask.Snapshot{}, fmt.Errorf("scheduled run: %w", err)
	}

	mode := strings.ToLower(strings.TrimSpace(spec.Mode))
	if !session.IsValidMode(mode) {
		mode = string(session.ModeAgent)
	}
	perm := strings.TrimSpace(spec.PermissionMode)
	if perm == "" {
		perm = config.PermModeBypass
	}
	def := spec.Definition
	name := jobID
	role := ""
	maxTurns := 0
	if def != nil {
		name = def.Name
		role = def.Role
		maxTurns = def.MaxTurns
		perm = subagents.NarrowPermissionMode(perm, def.PermissionMode)
	}
	if def != nil && def.Mode != "" && mode == string(session.ModeAgent) {
		// A job in agent mode takes the definition's mode; a read-only job
		// (plan, ask) keeps its own, as a read-only parent does for a spawn.
		mode = def.Mode
	}

	// The tool policy decides the set once the MCP names exist, and whether
	// MCP is dialed at all: a run under a definition that admits no tool of any
	// configured server never starts an MCP process.
	exclusions := append([]string(nil), subagentMandatoryExclusions...)
	if cfg.Subagents.EffectiveMaxDepth() <= 0 {
		exclusions = append(exclusions, tools.ToolSpawnAgent)
	}
	registryNames := registryToolNamesForMode(cfg, mode)
	resolve := func(mcpTools []string) []string {
		all := append(append([]string(nil), registryNames...), mcpTools...)
		if def != nil {
			return subagents.EffectiveTools(all, ToolSetForMode(mode), def, exclusions)
		}
		return subagents.EffectiveTools(all, ToolSetForMode(mode), nil, exclusions)
	}
	connectMCP := mode != string(session.ModeAsk)
	if connectMCP && def != nil {
		connectMCP = false
		for _, server := range spec.MCPServerNames {
			if def.Allows(strings.TrimSpace(server) + "__probe") {
				connectMCP = true
				break
			}
		}
	}

	model := strings.TrimSpace(spec.Model)
	unknownModel := ""
	if model == "" && def != nil && def.Model != "" {
		if cfg.FindModelEntry(def.Model) != nil {
			model = def.Model
		} else {
			unknownModel = def.Model
			log.Warn("scheduled run: the definition names an unknown model; the configured model is used", "job_id", jobID, "agent", def.Name, "model", def.Model)
		}
	}

	label := strings.TrimSpace(spec.Label)
	if label == "" {
		label = jobID
	}
	label = capRunes(label, maxTaskLabelRunes)

	pool.SetConfig(backgroundConfig(cfg))
	if dir := strings.TrimSpace(spec.JobSessionDir); dir != "" {
		pool.SetSessionDir(jobSessionID, dir)
	}

	runCtx, cancel := context.WithCancel(ctx)
	handle := &subagentHandle{cancel: cancel, done: make(chan struct{})}
	run := &subagentRun{name: name, def: def, childID: runID, prompt: spec.Instruction, parentMode: mode, handle: handle, startedAt: time.Now()}

	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			// Work the run launched settles before its transcript is sealed,
			// its records leave the pool's memory with it, and the session
			// closes its clients: nothing of a finished run stays in memory.
			pool.StopSession(runID)
			pool.ReleaseSession(runID)
			rt.RetireSubagentSession(runID)
			cancel()
		})
	}

	taskSpec := bgtask.Spec{
		SessionID:      jobSessionID,
		Kind:           bgtask.KindAgent,
		Label:          label,
		CWD:            spec.CWD,
		TimeoutSeconds: spec.TimeoutSeconds,
		NotifyOnFinish: false,
		// An empty model follows the config; the row names the one that runs.
		Agent: &bgtask.AgentInfo{Name: name, SessionID: runID, Model: session.ResolveModelID(cfg, model)},
	}
	snap, err := pool.Launch(taskSpec, func(taskID string, out io.Writer) (bgtask.Handle, error) {
		run.taskID = taskID
		// No relay: the scheduler has no parent chat to forward a prompt to,
		// so a request the gate raises under ask or accept_edits is denied.
		run.sender = newSubagentSender(out, nil)
		run.sender.onUsage = func(in, outTokens int) { pool.SetAgentUsage(jobSessionID, taskID, in, outTokens) }
		_, _ = fmt.Fprintf(out, "scheduled run of job %s (%s, task %s, session %s) starting\n", jobID, spec.Trigger, taskID, runID)
		if unknownModel != "" {
			_, _ = fmt.Fprintf(out, "model %q is not configured; using the configured model\n", unknownModel)
		}
		childSpec := session.SubagentSpec{
			ID:              runID,
			ParentSessionID: jobSessionID,
			Name:            name,
			TaskID:          taskID,
			CWD:             spec.CWD,
			Mode:            mode,
			PermissionMode:  perm,
			SelectedModelID: model,
			Title:           label,
			Role:            role,
			Depth:           0,
			MaxTurns:        maxTurns,
			ConnectMCP:      connectMCP,
			ResolveTools:    resolve,
			Scheduler: &session.SchedulerRunMeta{
				JobID:    jobID,
				Trigger:  strings.TrimSpace(spec.Trigger),
				FireSlot: spec.FireSlot,
			},
		}
		go executeChildRun(runCtx, rt, run, childSpec, out, finish, log, nil)
		return handle, nil
	})
	if err != nil {
		finish()
		return bgtask.Snapshot{}, err
	}
	return snap, nil
}

// registryToolNamesForMode lists the built-in tools a session of the mode may
// call: every registered definition, filtered by the mode's allowlist. The
// scheduled run's tool set is intersected with it the way a spawn intersects
// with its parent's names.
func registryToolNamesForMode(cfg *config.Config, mode string) []string {
	registry := tools.NewRegistryForEnvironment(cfg, platform.CurrentEnvironment())
	defs := FilterToolDefinitions(registry.AllToolDefinitions(), ToolSetForMode(mode))
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	return names
}

// lastAssistantPlainText returns the last assistant message with text, or ""
// when the transcript holds none: the report of a child run.
func lastAssistantPlainText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != llm.RoleAssistant {
			continue
		}
		if s := strings.TrimSpace(msgs[i].Content); s != "" {
			return msgs[i].Content
		}
	}
	return ""
}
