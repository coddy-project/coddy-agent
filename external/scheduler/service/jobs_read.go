//go:build scheduler

package schedservice

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
)

// runningCount reports the runs in flight across every job of this process.
func runningCount() int {
	rt := CurrentRuntime()
	if rt == nil {
		return 0
	}
	return rt.RunningCount()
}

func (o *Service) buildSchedulerInfo(workspace string) SchedulerInfo {
	c := o.Cfg
	return SchedulerInfo{
		Enabled:        c.SchedulerEffectiveEnabled(),
		Dir:            c.SchedulerUserDir(),
		ProjectDir:     storage.ProjectDirName,
		ProjectTrust:   c.Scheduler.ResolvedProjectTrust(),
		Workspace:      workspace,
		Timeout:        strings.TrimSpace(c.Scheduler.Timeout),
		MaxQueue:       c.Scheduler.MaxQueue,
		RunsActive:     runningCount(),
		RetainSessions: c.SchedulerRetainSessionsEffective(),
	}
}

// jobRow builds the row of one job from one read of its file. A file that
// does not read as a job is still listed when it is a project job, so the
// operator sees why it does not run; a broken user job is skipped as before.
func (o *Service) jobRow(ref storage.JobRef, now time.Time, includeBody, includeRaw, scheduled bool) (SchedulerJob, bool) {
	snap, err := storage.ReadSnapshot(ref)
	if err != nil && !errors.Is(err, storage.ErrUnsafePath) {
		return SchedulerJob{}, false
	}
	out := SchedulerJob{
		JobID:     ref.ID,
		Scope:     ref.Scope,
		Workspace: ref.Workspace,
		Digest:    snap.Digest,
		Scheduled: scheduled,
	}
	if err != nil {
		out.Trust, out.TrustReason = TrustInvalid, err.Error()
		return out, ref.IsProject()
	}
	out.Trust, out.TrustReason = Decide(o.Cfg, snap)
	if snap.FM == nil || (snap.Err != nil && !ref.IsProject()) {
		return out, ref.IsProject()
	}
	fm := snap.FM
	out.Description = strings.TrimSpace(fm.Description)
	out.Schedule = strings.TrimSpace(fm.Schedule)
	out.Paused = fm.Paused
	out.CWD = strings.TrimSpace(fm.CWD)
	out.Model = strings.TrimSpace(fm.Model)
	out.Mode = strings.TrimSpace(fm.Mode)
	out.Agent = strings.TrimSpace(fm.Agent)
	out.PermissionMode = strings.TrimSpace(fm.PermissionMode)
	out.Running = jobRunning(ref)
	out.SessionID = o.jobSessionIDOf(ref)
	out.LastRun = o.lastRunOf(ref)
	if includeBody {
		out.Body = snap.Body
	}
	if includeRaw && ref.IsProject() {
		out.Raw = string(snap.Raw)
	}
	last, _ := storage.ReadJobState(ref.StatePath)
	if !last.IsZero() {
		out.LastScheduledSlotUTC = last.UTC().Format(time.RFC3339)
	}
	if sch, err := storage.ParseCronUTC(fm.Schedule); err == nil {
		out.NextRunUTC = storage.NextScheduledDisplayUTC(sch, last, now).UTC().Format(time.RFC3339)
	}
	return out, true
}

// ListOptions select what ListJobs returns.
type ListOptions struct {
	IncludeBody bool
	// Workspace is the canonical workspace the request named (its session or
	// a cwd listing); empty lists the user jobs only, the old contract.
	Workspace string
	// UserOnly narrows the list to the user jobs (scope=user).
	UserOnly bool
}

// ListJobs returns the scheduler envelope plus the user jobs and, when the
// request named a workspace, the project jobs of every workspace the daemon
// scans and of that one.
func (o *Service) ListJobs(opts ListOptions) (*JobsListResponse, error) {
	if err := o.requireEnabled(); err != nil {
		return nil, err
	}
	o.register()
	now := time.Now().UTC()
	jobs := []SchedulerJob{}
	userRefs, err := o.roots().ListUser()
	if err != nil {
		return nil, err
	}
	for _, ref := range userRefs {
		if row, ok := o.jobRow(ref, now, opts.IncludeBody, false, true); ok {
			jobs = append(jobs, row)
		}
	}
	ws := CanonicalWorkspace(opts.Workspace)
	if ws != "" && !opts.UserOnly {
		scan := ScanWorkspaces(o.Cfg, o.ProcessCWD)
		scanned := map[string]bool{}
		for _, w := range scan {
			scanned[w] = true
		}
		order := append([]string(nil), scan...)
		if !scanned[ws] {
			order = append(order, ws)
		}
		for _, w := range order {
			refs, err := o.roots().ListProject(w)
			if err != nil {
				continue
			}
			for _, ref := range refs {
				if row, ok := o.jobRow(ref, now, opts.IncludeBody, false, scanned[w]); ok {
					jobs = append(jobs, row)
				}
			}
		}
	}
	return &JobsListResponse{Scheduler: o.buildSchedulerInfo(ws), Jobs: jobs}, nil
}

// GetJob returns one job, with the raw file of a project job.
func (o *Service) GetJob(addr JobAddr) (SchedulerJob, error) {
	if err := o.requireEnabled(); err != nil {
		return SchedulerJob{}, err
	}
	ref, err := o.existingRef(addr)
	if err != nil {
		return SchedulerJob{}, err
	}
	scheduled := !ref.IsProject() || inScanSet(o.Cfg, o.ProcessCWD, ref.Workspace)
	row, ok := o.jobRow(ref, time.Now().UTC(), true, true, scheduled)
	if !ok {
		snap, rerr := storage.ReadSnapshot(ref)
		if rerr != nil {
			return SchedulerJob{}, rerr
		}
		if snap.Err != nil {
			return SchedulerJob{}, snap.Err
		}
		return SchedulerJob{}, ErrJobNotFound
	}
	return row, nil
}

// existingRef resolves an address to a job file that exists.
func (o *Service) existingRef(addr JobAddr) (storage.JobRef, error) {
	ref, err := o.ref(addr)
	if err != nil {
		return storage.JobRef{}, err
	}
	if _, err := os.Lstat(ref.Path); err != nil {
		if os.IsNotExist(err) {
			return storage.JobRef{}, ErrJobNotFound
		}
		return storage.JobRef{}, err
	}
	return ref, nil
}
