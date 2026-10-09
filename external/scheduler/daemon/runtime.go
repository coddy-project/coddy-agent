//go:build scheduler

package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
)

// Runtime is the scheduler daemon's view of the runs of this process: what
// starts a run, what stops it, what knows whether a job is running, and the
// run history each job keeps in its job session. It implements
// schedservice.Runtime, which is how the HTTP handlers and the tools reach it.
type Runtime struct {
	ctx        context.Context
	cfg        func() *config.Config
	mgr        *session.Manager
	pool       *bgtask.Pool
	log        *slog.Logger
	processCWD string

	mu      sync.Mutex
	running map[string]*runningEntry
	slots   chan struct{}

	// skipped remembers the digest of each project job the daemon last
	// reported as not runnable, so the log says it once per content, not
	// once a minute.
	skipped map[string]string

	// historyMu serialises the sweeps over a job session's runs (retention
	// after a run, Clear from the API, the delete of a job), so two of them
	// never drop the same run at once and a Clear reports what it removed.
	historyMu sync.Mutex
}

// runningEntry is one reserved job: the run's ids once the launch returned,
// and whether a cancel arrived before that. A job is reserved before anything
// exists to stop, so a cancel that lands in that window is kept and applied to
// the task the moment it is registered.
type runningEntry struct {
	ref             schedservice.RunRef
	cancelRequested bool
}

// NewRuntime builds the runtime of one daemon. ctx is the daemon's lifetime:
// every run derives from it, so stopping the daemon stops its runs.
func NewRuntime(ctx context.Context, cfg func() *config.Config, mgr *session.Manager, pool *bgtask.Pool, log *slog.Logger, processCWD string) *Runtime {
	if pool == nil {
		pool = bgtask.Default()
	}
	if log == nil {
		log = slog.Default()
	}
	maxQueue := 10
	if c := cfg(); c != nil && c.Scheduler.MaxQueue > 0 {
		maxQueue = c.Scheduler.MaxQueue
	}
	return &Runtime{
		ctx:        ctx,
		cfg:        cfg,
		mgr:        mgr,
		pool:       pool,
		log:        log,
		processCWD: processCWD,
		running:    map[string]*runningEntry{},
		skipped:    map[string]string{},
		slots:      make(chan struct{}, maxQueue),
	}
}

var _ schedservice.Runtime = (*Runtime)(nil)

// jobCWD is where a run of the job works: a project job inside its
// workspace (an escape refused), a user job against the process cwd.
func jobCWD(processCWD string, snap storage.JobSnapshot) (string, error) {
	if snap.Ref.IsProject() {
		return storage.ProjectCWD(snap.Ref.Workspace, snap.FM.CWD)
	}
	return resolveJobCWD(processCWD, snap.FM)
}

func resolveJobCWD(processCWD string, fm *storage.JobFrontmatter) (string, error) {
	base := strings.TrimSpace(processCWD)
	if base == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		base = wd
	}
	raw := ""
	if fm != nil {
		raw = strings.TrimSpace(fm.CWD)
	}
	if raw == "" {
		return filepath.Clean(base), nil
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	return filepath.Clean(filepath.Join(base, raw)), nil
}

// runLabel names a run by its job and how it started, the way the Tasks panel
// and the run session's title show it.
func runLabel(jobID, trigger string, fireSlot, now time.Time) string {
	when := now.UTC()
	if trigger == schedservice.TriggerCron && !fireSlot.IsZero() {
		when = fireSlot.UTC()
	}
	return fmt.Sprintf("%s · %s %s", jobID, trigger, when.Format("2006-01-02 15:04 UTC"))
}

// resolveDefinition loads the definition a job names for the job's cwd and
// decides its trust, the way spawn_agent does. nil, nil for a job that names
// none.
func (r *Runtime) resolveDefinition(cfg *config.Config, fm *storage.JobFrontmatter, cwd string) (*subagents.Definition, error) {
	name := strings.TrimSpace(fm.Agent)
	if name == "" {
		return nil, nil
	}
	loader := subagents.NewLoader(cfg.Subagents.SearchDirs(), cfg.Subagents.ResolvedProjectTrust())
	loader.Log = r.log
	defs := loader.Load(cwd, cfg.Paths.Home)
	def := subagents.FindByName(defs, name)
	if def == nil {
		return nil, fmt.Errorf("%w: unknown subagent %q; available: %s", schedservice.ErrRunRefused, name, strings.Join(subagents.VisibleNames(defs), ", "))
	}
	workspace := subagents.CanonicalWorkspace(cwd)
	policy := cfg.Subagents.ResolvedProjectTrust()
	store := subagents.NewTrustStore(cfg.Paths.Home)
	if subagents.Decide(def, policy, workspace, store) == subagents.TrustNeedsApproval {
		return nil, fmt.Errorf("%w: subagent %q comes from a project file (%s) that is not approved for workspace %s; "+
			"approve it with `coddy agents trust %s --cwd %s`, or POST /coddy/subagents/%s/trust with body {\"cwd\": %q}, "+
			"or set subagents.project_trust to allow for this checkout",
			schedservice.ErrRunRefused, def.Name, def.Path, workspace, def.Name, workspace, def.Name, workspace)
	}
	return def, nil
}

// noteSkipped logs once per content that a job is not run because it is not
// trusted.
func (r *Runtime) noteSkipped(snap storage.JobSnapshot, state schedservice.TrustState, reason string) {
	key := snap.Ref.Key()
	r.mu.Lock()
	seen := r.skipped[key] == snap.Digest+string(state)
	r.skipped[key] = snap.Digest + string(state)
	r.mu.Unlock()
	if !seen {
		r.log.Info("scheduler project job not run", "job", snap.Ref.ID, "workspace", snap.Ref.Workspace, "trust", string(state), "reason", reason)
	}
}

// StartRun implements schedservice.Runtime. The job is reserved (running mark
// and max_queue slot) under the mutex before anything is launched, so a cron
// tick and a manual run cannot both start it; the reservation is released by
// the watcher that sees the run finish, whatever the exit path, and by the
// failure paths below.
func (r *Runtime) StartRun(_ context.Context, req schedservice.RunRequest) (schedservice.RunRef, error) {
	snap := req.Snapshot
	if snap.FM == nil {
		return schedservice.RunRef{}, fmt.Errorf("scheduler run: job frontmatter is required")
	}
	fm := snap.FM
	if fm.Paused {
		return schedservice.RunRef{}, schedservice.ErrJobPaused
	}
	cfg := r.cfg()
	if cfg == nil {
		return schedservice.RunRef{}, fmt.Errorf("scheduler run: configuration is required")
	}
	// Trust is decided again on the very snapshot that runs, before anything
	// is reserved: a receipt revoked or a user job created since the caller
	// looked stops the run here.
	if state, reason := schedservice.Decide(cfg, snap); state != schedservice.TrustTrusted {
		return schedservice.RunRef{}, fmt.Errorf("%w: job %q is %s: %s", schedservice.ErrJobUntrusted, snap.Ref.ID, state, reason)
	}
	ref := snap.Ref
	key := ref.Key()
	jobID := ref.ID
	cwd, err := jobCWD(r.processCWD, snap)
	if err != nil {
		return schedservice.RunRef{}, fmt.Errorf("scheduler run cwd: %w", err)
	}
	trigger := strings.TrimSpace(req.Trigger)
	if trigger == "" {
		trigger = schedservice.TriggerManual
	}

	// Trust is decided before anything is reserved: a refused definition
	// starts nothing and holds nothing.
	def, err := r.resolveDefinition(cfg, fm, cwd)
	if err != nil {
		return schedservice.RunRef{}, err
	}

	r.mu.Lock()
	if _, busy := r.running[key]; busy {
		r.mu.Unlock()
		return schedservice.RunRef{}, schedservice.ErrJobBusy
	}
	select {
	case r.slots <- struct{}{}:
	default:
		r.mu.Unlock()
		return schedservice.RunRef{}, fmt.Errorf("%w (scheduler.max_queue is %d)", schedservice.ErrQueueSaturated, cap(r.slots))
	}
	entry := &runningEntry{ref: schedservice.RunRef{JobID: jobID, Trigger: trigger, StartedAt: time.Now().UTC()}}
	r.running[key] = entry
	r.mu.Unlock()

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			r.mu.Lock()
			delete(r.running, key)
			r.mu.Unlock()
			<-r.slots
		})
	}

	jobSessionID, jobSessionDir, err := r.ensureJobSession(ref, cwd)
	if err != nil {
		release()
		return schedservice.RunRef{}, err
	}

	// The cron checkpoint is committed before the run starts, so the next
	// tick (a minute away) never treats the same slot as due while the run
	// is being created.
	if req.UpdateState {
		if werr := storage.WriteJobState(ref.StatePath, req.FireSlot); werr != nil {
			release()
			return schedservice.RunRef{}, fmt.Errorf("scheduler run state write: %w", werr)
		}
	}

	timeout := 30 * time.Minute
	if d, perr := time.ParseDuration(strings.TrimSpace(cfg.Scheduler.Timeout)); perr == nil && d > 0 {
		timeout = d
	}
	runID := session.NewSessionID()
	now := time.Now().UTC()
	spec := agent.ScheduledRunSpec{
		JobID:          jobID,
		JobWorkspace:   ref.Workspace,
		JobSessionID:   jobSessionID,
		JobSessionDir:  jobSessionDir,
		RunSessionID:   runID,
		Label:          runLabel(jobID, trigger, req.FireSlot, now),
		Trigger:        trigger,
		FireSlot:       req.FireSlot,
		CWD:            cwd,
		Mode:           fm.Mode,
		Model:          fm.Model,
		PermissionMode: fm.PermissionMode,
		Instruction:    snap.Body,
		TimeoutSeconds: int(timeout / time.Second),
		Definition:     def,
		MCPServerNames: mcpServerNames(cfg, cwd, r.log),
	}
	task, err := agent.RunScheduledJob(r.ctx, cfg, r.mgr, r.pool, r.log, spec)
	if err != nil {
		release()
		return schedservice.RunRef{}, err
	}
	runRef := schedservice.RunRef{
		JobID:        jobID,
		JobSessionID: jobSessionID,
		TaskID:       task.ID,
		RunSessionID: runID,
		Trigger:      trigger,
		StartedAt:    task.StartedAt.UTC(),
	}
	r.mu.Lock()
	entry.ref = runRef
	cancelled := entry.cancelRequested
	r.mu.Unlock()
	r.log.Info("scheduler_run_spawn", "job_id", jobID, "session_id", runID, "task_id", task.ID, "trigger", trigger)
	if cancelled {
		// A cancel that arrived while the job was reserved but its task did
		// not exist yet is honoured now, the task being the first thing there
		// is to stop.
		go func() { _, _ = r.pool.Stop(runRef.JobSessionID, runRef.TaskID) }()
	}
	go r.watch(runRef, release)
	return runRef, nil
}

// mcpServerNames lists the configured MCP servers the trust gate admits for
// cwd: the names a definition's allowlist is probed with.
func mcpServerNames(cfg *config.Config, cwd string, log *slog.Logger) []string {
	var names []string
	for _, srv := range session.EffectiveMCPServers(cfg, cwd, log) {
		if n := strings.TrimSpace(srv.Name); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// watch waits for the run to settle, logs the outcome, applies retention,
// lets the job session go and releases the reservation - in that order, so a
// run of the same job cannot start while the job session is being released.
//
// The wait is deliberately not bound to the daemon's context: a stopped daemon
// cancels its runs, which is what settles them, and the bookkeeping below has
// to run for a run that settled that way too. The pool's own hard timeout is
// what guarantees every task settles, so the wait cannot hang for good.
func (r *Runtime) watch(ref schedservice.RunRef, release func()) {
	snap, err := r.pool.Wait(context.Background(), ref.JobSessionID, ref.TaskID, 0)
	status := string(snap.Status)
	if err != nil || status == "" {
		status = "unknown"
	}
	attrs := []any{"job_id", ref.JobID, "session_id", ref.RunSessionID, "task_id", ref.TaskID, "status", status}
	if snap.Error != "" {
		errStr := snap.Error
		if len(errStr) > 200 {
			errStr = errStr[:200] + "..."
		}
		attrs = append(attrs, "error", errStr)
	}
	r.log.Info("scheduler_run_finish", attrs...)

	if c := r.cfg(); c != nil {
		if perr := r.retain(ref.JobSessionID, c.SchedulerRetainSessionsEffective()); perr != nil {
			r.log.Warn("scheduler_run_retain", "job_id", ref.JobID, "error", perr)
		}
	}
	// Nothing of a finished run stays in memory between runs: the records
	// come back from the bundle when the panel asks for them.
	r.pool.ReleaseSession(ref.JobSessionID)
	r.mgr.ForgetLiveSession(ref.JobSessionID)
	release()
}

// CancelRun implements schedservice.Runtime. A job reserved whose task is
// not registered yet is cancelled too: the request is kept on the reservation
// and applied to the task as soon as StartRun has it.
func (r *Runtime) CancelRun(job storage.JobRef) bool {
	r.mu.Lock()
	entry, ok := r.running[job.Key()]
	if !ok {
		r.mu.Unlock()
		return false
	}
	ref := entry.ref
	if ref.TaskID == "" {
		entry.cancelRequested = true
		r.mu.Unlock()
		return true
	}
	r.mu.Unlock()
	if _, err := r.pool.Stop(ref.JobSessionID, ref.TaskID); err != nil {
		return false
	}
	return true
}

// RunningRun implements schedservice.Runtime.
func (r *Runtime) RunningRun(job storage.JobRef) (schedservice.RunRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.running[job.Key()]
	if !ok {
		return schedservice.RunRef{}, false
	}
	return entry.ref, true
}

// RunningCount implements schedservice.Runtime.
func (r *Runtime) RunningCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running)
}

// Pool implements schedservice.Runtime.
func (r *Runtime) Pool() *bgtask.Pool {
	return r.pool
}

// jobSessionID names the job session of a job, "" before its first run.
func (r *Runtime) jobSessionID(job storage.JobRef) string {
	return schedservice.JobSessionIDFor(r.mgr.FileStore(), job)
}

// ensureJobSession returns the live job session of a job, minting and
// recording its id on the first run.
func (r *Runtime) ensureJobSession(job storage.JobRef, cwd string) (id, dir string, err error) {
	id = r.jobSessionID(job)
	if id == "" {
		id = session.NewSessionID()
		if werr := storage.WriteJobSessionID(job.StatePath, id); werr != nil {
			return "", "", fmt.Errorf("scheduler job session pointer: %w", werr)
		}
	} else if recorded, _ := storage.ReadJobSessionID(job.StatePath); recorded != id {
		// Re-attached from the bundles: record the pointer so the walk is
		// not needed again.
		_ = storage.WriteJobSessionID(job.StatePath, id)
	}
	st, err := r.mgr.EnsureSchedulerJobSession(r.ctx, session.SchedulerJobSessionSpec{ID: id, JobID: job.ID, Workspace: job.Workspace, CWD: cwd})
	if err != nil {
		return "", "", fmt.Errorf("scheduler job session: %w", err)
	}
	return id, strings.TrimSpace(st.GetPersistedSessionDir()), nil
}

// runsOf lists the run tasks of a job session, newest first.
func (r *Runtime) runsOf(jobSessionID string) []bgtask.Snapshot {
	return schedservice.RunsOf(r.mgr.FileStore(), r.pool, jobSessionID)
}

// dropRun removes one finished run: its bundle with everything under it, then
// its task record. The pool is told where the job session's bundle is first:
// a job session released between runs is one the pool has let go of, and the
// record it holds on disk is what Forget has to reach.
func (r *Runtime) dropRun(jobSessionID string, snap bgtask.Snapshot) error {
	if snap.Agent != nil && strings.TrimSpace(snap.Agent.SessionID) != "" {
		// A bundle that is already gone is not an error: the tree walk finds
		// nothing under it and the removal of a missing path is a no-op.
		if err := r.mgr.DeleteSessionTree(snap.Agent.SessionID, r.pool); err != nil {
			return err
		}
	}
	if store := r.mgr.FileStore(); store != nil {
		r.pool.SetSessionDir(jobSessionID, store.SessionPath(jobSessionID))
	}
	if err := r.pool.Forget(jobSessionID, snap.ID); err != nil && !errors.Is(err, bgtask.ErrNotFound) {
		return err
	}
	return nil
}

// retain keeps the newest keep finished runs of a job and drops the rest.
func (r *Runtime) retain(jobSessionID string, keep int) error {
	if keep < 0 {
		keep = 0
	}
	r.historyMu.Lock()
	defer r.historyMu.Unlock()
	var firstErr error
	finished := 0
	for _, snap := range r.runsOf(jobSessionID) {
		if !snap.Status.Finished() {
			continue
		}
		finished++
		if finished <= keep {
			continue
		}
		if err := r.dropRun(jobSessionID, snap); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ClearRuns implements schedservice.Runtime.
func (r *Runtime) ClearRuns(job storage.JobRef) (int, error) {
	jobSessionID := r.jobSessionID(job)
	if jobSessionID == "" {
		return 0, nil
	}
	r.historyMu.Lock()
	defer r.historyMu.Unlock()
	cleared := 0
	var firstErr error
	for _, snap := range r.runsOf(jobSessionID) {
		if !snap.Status.Finished() {
			continue
		}
		if err := r.dropRun(jobSessionID, snap); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		cleared++
	}
	return cleared, firstErr
}

// DeleteJobHistory implements schedservice.Runtime.
func (r *Runtime) DeleteJobHistory(job storage.JobRef) error {
	jobSessionID := r.jobSessionID(job)
	if jobSessionID == "" {
		return nil
	}
	r.historyMu.Lock()
	defer r.historyMu.Unlock()
	store := r.mgr.FileStore()
	if store == nil || !store.HasPersistedSnapshot(jobSessionID) {
		r.mgr.ForgetLiveSession(jobSessionID)
		return nil
	}
	if err := r.mgr.DeleteSessionTree(jobSessionID, r.pool); err != nil {
		return err
	}
	r.pool.ReleaseSession(jobSessionID)
	return nil
}
