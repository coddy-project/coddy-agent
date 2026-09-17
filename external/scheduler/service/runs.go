//go:build scheduler

package schedservice

import (
	"context"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// jobRunning reports whether this process has a run of the job in flight.
func jobRunning(abs string) bool {
	rt := CurrentRuntime()
	if rt == nil {
		return false
	}
	_, ok := rt.RunningRun(abs)
	return ok
}

// existingJobPath resolves jobID to its canonical file path, refusing an id
// that is malformed or names no file.
func (o *Service) existingJobPath(jobID string) (string, error) {
	abs, err := o.jobAbsPath(jobID)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", ErrJobNotFound
		}
		return "", err
	}
	return abs, nil
}

// sessionStore is the bundle store the run history lives in.
func (o *Service) sessionStore() *session.FileStore {
	return &session.FileStore{Root: o.Cfg.ResolvedSessionsRoot()}
}

// legacyRunPrefix is how the scheduler shipped before the job session wrote
// its run bundles: top-level sessions whose ids began with sched_, marked
// schedulerRun with the job id like a job session is. They are runs, not a
// parent to hang new runs under, so the fallback walk never picks one.
const legacyRunPrefix = "sched_"

// JobSessionIDFor names the job session of a job: the pointer in its sidecar,
// or the bundle in the sessions root whose session.json says it belongs to the
// job when the pointer is missing (a sidecar deleted by hand, or written by the
// old scheduler). "" for a job that never ran. The walk is the fallback, never
// the first read: it costs one small file per session.
func JobSessionIDFor(store *session.FileStore, jobPath string) string {
	abs := storage.CanonicalSchedulerJobPath(jobPath)
	if abs == "" {
		abs = jobPath
	}
	id, _ := storage.ReadJobSessionID(storage.StatePath(abs))
	if id != "" && session.ValidateFolderSessionID(id) == nil {
		return id
	}
	if store == nil || store.Root == "" {
		return ""
	}
	jobID := jobIDFromMDPath(abs)
	entries, err := os.ReadDir(store.Root)
	if err != nil {
		return ""
	}
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") || strings.HasPrefix(ent.Name(), legacyRunPrefix) {
			continue
		}
		meta, err := store.ReadMeta(ent.Name())
		if err != nil {
			continue
		}
		if meta.SchedulerRun && !meta.IsSubagentRun() && strings.TrimSpace(meta.SchedulerJobID) == jobID {
			return ent.Name()
		}
	}
	return ""
}

// JobSessionID names the job session of a job, "" before its first run.
func (o *Service) JobSessionID(jobID string) (string, error) {
	if err := o.requireEnabled(); err != nil {
		return "", err
	}
	abs, err := o.existingJobPath(jobID)
	if err != nil {
		return "", err
	}
	return o.jobSessionIDOf(abs), nil
}

// RunsOf lists the run tasks of a job session, newest first: what the pool
// still holds plus the records the job session's bundle kept, which is what a
// released job session has once its runs finished.
func RunsOf(store *session.FileStore, pool *bgtask.Pool, jobSessionID string) []bgtask.Snapshot {
	jobSessionID = strings.TrimSpace(jobSessionID)
	if jobSessionID == "" {
		return nil
	}
	if pool == nil {
		pool = bgtask.Default()
	}
	live := pool.List(jobSessionID)
	seen := make(map[string]bool, len(live))
	out := make([]bgtask.Snapshot, 0, len(live))
	for _, snap := range live {
		if snap.Kind != bgtask.KindAgent {
			continue
		}
		seen[snap.ID] = true
		out = append(out, snap)
	}
	if store != nil && store.Root != "" {
		for _, snap := range bgtask.LoadPersisted(store.SessionPath(jobSessionID)) {
			if seen[snap.ID] || snap.Kind != bgtask.KindAgent {
				continue
			}
			snap.SessionID = jobSessionID
			out = append(out, snap)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// RunEntryOf is the run row of one task of a job session.
func RunEntryOf(store *session.FileStore, snap bgtask.Snapshot, jobSessionID string) SchedulerRunEntry {
	now := time.Now()
	entry := SchedulerRunEntry{
		TaskID:         snap.ID,
		JobSessionID:   jobSessionID,
		Status:         string(snap.Status),
		Running:        !snap.Status.Finished(),
		StartedAt:      snap.StartedAt.UTC().Format(time.RFC3339),
		ElapsedSeconds: int(snap.Elapsed(now) / time.Second),
		Error:          snap.Error,
		Label:          snap.Label,
	}
	if snap.Agent != nil {
		entry.SessionID = strings.TrimSpace(snap.Agent.SessionID)
	}
	if snap.FinishedAt != nil {
		entry.EndedAt = snap.FinishedAt.UTC().Format(time.RFC3339)
	}
	if entry.SessionID != "" && store != nil {
		if meta, err := store.ReadMeta(entry.SessionID); err == nil {
			entry.Trigger = strings.TrimSpace(meta.SchedulerTrigger)
		}
	}
	return entry
}

// runRows lists the runs of a job by its path, newest first, at most limit.
func (o *Service) runRows(abs string, limit int) []SchedulerRunEntry {
	store := o.sessionStore()
	jobSessionID := JobSessionIDFor(store, abs)
	if jobSessionID == "" {
		return []SchedulerRunEntry{}
	}
	snaps := RunsOf(store, bgtask.Default(), jobSessionID)
	if limit > 0 && len(snaps) > limit {
		snaps = snaps[:limit]
	}
	out := make([]SchedulerRunEntry, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, RunEntryOf(store, snap, jobSessionID))
	}
	return out
}

// TriggerJobRun starts one manual run of a job and returns the ids the run
// answers to. It does not advance the cron checkpoint: the cron timing stays
// what the schedule says. ErrJobPaused for a paused job, ErrJobBusy while a
// run of the job is in flight, ErrQueueSaturated when scheduler.max_queue runs
// are already going, ErrLauncherNotConfigured when no daemon runs here.
func (o *Service) TriggerJobRun(jobID string) (RunRef, error) {
	if err := o.requireEnabled(); err != nil {
		return RunRef{}, err
	}
	abs, err := o.existingJobPath(jobID)
	if err != nil {
		return RunRef{}, err
	}
	fm, body, err := storage.ParseJobFile(abs)
	if err != nil {
		return RunRef{}, err
	}
	if fm.Paused {
		return RunRef{}, ErrJobPaused
	}
	rt := CurrentRuntime()
	if rt == nil {
		return RunRef{}, ErrLauncherNotConfigured
	}
	return rt.StartRun(context.Background(), RunRequest{
		JobPath:     abs,
		Frontmatter: fm,
		Body:        body,
		Trigger:     TriggerManual,
	})
}

// CancelJobRun stops the run of the job that is in flight. cancelled is false
// when the job was not running.
func (o *Service) CancelJobRun(jobID string) (cancelled bool, err error) {
	if err := o.requireEnabled(); err != nil {
		return false, err
	}
	abs, err := o.existingJobPath(jobID)
	if err != nil {
		return false, err
	}
	rt := CurrentRuntime()
	if rt == nil {
		return false, nil
	}
	return rt.CancelRun(abs), nil
}

// ListJobRuns returns the runs of a job, newest first: the run in flight and
// the finished ones the retention kept. It reads the job session's bundle, so
// it answers whether or not the daemon runs in this process.
func (o *Service) ListJobRuns(jobID string, limit int) ([]SchedulerRunEntry, error) {
	if err := o.requireEnabled(); err != nil {
		return nil, err
	}
	abs, err := o.existingJobPath(jobID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	return o.runRows(abs, limit), nil
}

// ClearJobRuns removes every finished run of a job, task records and
// transcripts, and reports how many went. A run in flight stays.
func (o *Service) ClearJobRuns(jobID string) (int, error) {
	if err := o.requireEnabled(); err != nil {
		return 0, err
	}
	abs, err := o.existingJobPath(jobID)
	if err != nil {
		return 0, err
	}
	rt := CurrentRuntime()
	if rt == nil {
		return 0, ErrLauncherNotConfigured
	}
	return rt.ClearRuns(abs)
}

// jobSessionIDOf names the job session of a job, "" before its first run.
func (o *Service) jobSessionIDOf(abs string) string {
	return JobSessionIDFor(o.sessionStore(), abs)
}

// lastRunOf returns the newest run of a job, nil when it never ran.
func (o *Service) lastRunOf(abs string) *SchedulerRunEntry {
	rows := o.runRows(abs, 1)
	if len(rows) == 0 {
		return nil
	}
	last := rows[0]
	return &last
}
