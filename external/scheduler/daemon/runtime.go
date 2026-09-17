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
	running map[string]schedservice.RunRef
	slots   chan struct{}
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
		running:    map[string]schedservice.RunRef{},
		slots:      make(chan struct{}, maxQueue),
	}
}

var _ schedservice.Runtime = (*Runtime)(nil)

func canonicalJobPath(jobPath string) string {
	abs := storage.CanonicalSchedulerJobPath(jobPath)
	if abs == "" {
		abs = filepath.Clean(jobPath)
	}
	return abs
}

func jobIDFromMDPath(abs string) string {
	return strings.TrimSuffix(filepath.Base(abs), ".md")
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
	loader := subagents.NewLoader(cfg.Subagents.Dirs, cfg.Subagents.ResolvedProjectTrust())
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

// StartRun implements schedservice.Runtime. The job is reserved (running mark
// and max_queue slot) under the mutex before anything is launched, so a cron
// tick and a manual run cannot both start it; the reservation is released by
// the watcher that sees the run finish, whatever the exit path, and by the
// failure paths below.
func (r *Runtime) StartRun(_ context.Context, req schedservice.RunRequest) (schedservice.RunRef, error) {
	if req.Frontmatter == nil {
		return schedservice.RunRef{}, fmt.Errorf("scheduler run: job frontmatter is required")
	}
	if req.Frontmatter.Paused {
		return schedservice.RunRef{}, schedservice.ErrJobPaused
	}
	cfg := r.cfg()
	if cfg == nil {
		return schedservice.RunRef{}, fmt.Errorf("scheduler run: configuration is required")
	}
	abs := canonicalJobPath(req.JobPath)
	jobID := jobIDFromMDPath(abs)
	cwd, err := resolveJobCWD(r.processCWD, req.Frontmatter)
	if err != nil {
		return schedservice.RunRef{}, fmt.Errorf("scheduler run cwd: %w", err)
	}
	trigger := strings.TrimSpace(req.Trigger)
	if trigger == "" {
		trigger = schedservice.TriggerManual
	}

	// Trust is decided before anything is reserved: a refused definition
	// starts nothing and holds nothing.
	def, err := r.resolveDefinition(cfg, req.Frontmatter, cwd)
	if err != nil {
		return schedservice.RunRef{}, err
	}

	r.mu.Lock()
	if _, busy := r.running[abs]; busy {
		r.mu.Unlock()
		return schedservice.RunRef{}, schedservice.ErrJobBusy
	}
	select {
	case r.slots <- struct{}{}:
	default:
		r.mu.Unlock()
		return schedservice.RunRef{}, fmt.Errorf("%w (scheduler.max_queue is %d)", schedservice.ErrQueueSaturated, cap(r.slots))
	}
	placeholder := schedservice.RunRef{JobID: jobID, Trigger: trigger, StartedAt: time.Now().UTC()}
	r.running[abs] = placeholder
	r.mu.Unlock()

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			r.mu.Lock()
			delete(r.running, abs)
			r.mu.Unlock()
			<-r.slots
		})
	}

	jobSessionID, jobSessionDir, err := r.ensureJobSession(abs, jobID, cwd)
	if err != nil {
		release()
		return schedservice.RunRef{}, err
	}

	// The cron checkpoint is committed before the run starts, so the next
	// tick (a minute away) never treats the same slot as due while the run
	// is being created.
	if req.UpdateState {
		if werr := storage.WriteJobState(storage.StatePath(abs), req.FireSlot); werr != nil {
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
		JobSessionID:   jobSessionID,
		JobSessionDir:  jobSessionDir,
		RunSessionID:   runID,
		Label:          runLabel(jobID, trigger, req.FireSlot, now),
		Trigger:        trigger,
		FireSlot:       req.FireSlot,
		CWD:            cwd,
		Mode:           req.Frontmatter.Mode,
		Model:          req.Frontmatter.Model,
		PermissionMode: req.Frontmatter.PermissionMode,
		Instruction:    req.Body,
		TimeoutSeconds: int(timeout / time.Second),
		Definition:     def,
		MCPServerNames: mcpServerNames(cfg, cwd, r.log),
	}
	snap, err := agent.RunScheduledJob(r.ctx, cfg, r.mgr, r.pool, r.log, spec)
	if err != nil {
		release()
		return schedservice.RunRef{}, err
	}
	ref := schedservice.RunRef{
		JobID:        jobID,
		JobSessionID: jobSessionID,
		TaskID:       snap.ID,
		RunSessionID: runID,
		Trigger:      trigger,
		StartedAt:    snap.StartedAt.UTC(),
	}
	r.mu.Lock()
	r.running[abs] = ref
	r.mu.Unlock()
	r.log.Info("scheduler_run_spawn", "job_id", jobID, "session_id", runID, "task_id", snap.ID, "trigger", trigger)
	go r.watch(abs, ref, release)
	return ref, nil
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
func (r *Runtime) watch(abs string, ref schedservice.RunRef, release func()) {
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
		if perr := r.retain(abs, ref.JobSessionID, c.SchedulerRetainSessionsEffective()); perr != nil {
			r.log.Warn("scheduler_run_retain", "job_id", ref.JobID, "error", perr)
		}
	}
	// Nothing of a finished run stays in memory between runs: the records
	// come back from the bundle when the panel asks for them.
	r.pool.ReleaseSession(ref.JobSessionID)
	r.mgr.ForgetLiveSession(ref.JobSessionID)
	release()
}

// CancelRun implements schedservice.Runtime.
func (r *Runtime) CancelRun(jobPath string) bool {
	abs := canonicalJobPath(jobPath)
	r.mu.Lock()
	ref, ok := r.running[abs]
	r.mu.Unlock()
	if !ok || ref.TaskID == "" {
		return false
	}
	if _, err := r.pool.Stop(ref.JobSessionID, ref.TaskID); err != nil {
		return false
	}
	return true
}

// RunningRun implements schedservice.Runtime.
func (r *Runtime) RunningRun(jobPath string) (schedservice.RunRef, bool) {
	abs := canonicalJobPath(jobPath)
	r.mu.Lock()
	defer r.mu.Unlock()
	ref, ok := r.running[abs]
	return ref, ok
}

// RunningCount implements schedservice.Runtime.
func (r *Runtime) RunningCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running)
}

// jobSessionID names the job session of a job, "" before its first run.
func (r *Runtime) jobSessionID(jobPath string) string {
	return schedservice.JobSessionIDFor(r.mgr.FileStore(), jobPath)
}

// ensureJobSession returns the live job session of a job, minting and
// recording its id on the first run.
func (r *Runtime) ensureJobSession(abs, jobID, cwd string) (id, dir string, err error) {
	id = r.jobSessionID(abs)
	if id == "" {
		id = session.NewSessionID()
		if werr := storage.WriteJobSessionID(storage.StatePath(abs), id); werr != nil {
			return "", "", fmt.Errorf("scheduler job session pointer: %w", werr)
		}
	} else if recorded, _ := storage.ReadJobSessionID(storage.StatePath(abs)); recorded != id {
		// Re-attached from the bundles: record the pointer so the walk is
		// not needed again.
		_ = storage.WriteJobSessionID(storage.StatePath(abs), id)
	}
	st, err := r.mgr.EnsureSchedulerJobSession(r.ctx, session.SchedulerJobSessionSpec{ID: id, JobID: jobID, CWD: cwd})
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
		if err := r.mgr.DeleteSessionTree(snap.Agent.SessionID, r.pool); err != nil && !strings.Contains(err.Error(), "not found") {
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
func (r *Runtime) retain(abs, jobSessionID string, keep int) error {
	if keep < 0 {
		keep = 0
	}
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
	_ = abs
	return firstErr
}

// ClearRuns implements schedservice.Runtime.
func (r *Runtime) ClearRuns(jobPath string) (int, error) {
	jobSessionID := r.jobSessionID(jobPath)
	if jobSessionID == "" {
		return 0, nil
	}
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
func (r *Runtime) DeleteJobHistory(jobPath string) error {
	jobSessionID := r.jobSessionID(jobPath)
	if jobSessionID == "" {
		return nil
	}
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
