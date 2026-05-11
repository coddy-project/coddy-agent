//go:build scheduler

package schedservice

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
)

func (o *Service) buildSchedulerInfo() SchedulerInfo {
	c := o.Cfg
	return SchedulerInfo{
		Enabled:        c.SchedulerEffectiveEnabled(),
		Dir:            strings.TrimSpace(c.Scheduler.Dir),
		PollInterval:   strings.TrimSpace(c.Scheduler.PollInterval),
		Timeout:        strings.TrimSpace(c.Scheduler.Timeout),
		MaxQueue:       c.Scheduler.MaxQueue,
		RunsActive:     TrackedJobRunCount(),
		RetainSessions: c.SchedulerRetainSessionsEffective(),
	}
}

func (o *Service) jobFromPath(abs string, now time.Time, includeBody bool) (SchedulerJob, error) {
	fm, body, err := storage.ParseJobFile(abs)
	if err != nil {
		return SchedulerJob{}, err
	}
	sch, err := storage.ParseCronUTC(fm.Schedule)
	if err != nil {
		return SchedulerJob{}, err
	}
	last, _ := storage.ReadJobState(storage.StatePath(abs))
	next := storage.NextScheduledDisplayUTC(sch, last, now)
	_ = CleanupStaleSchedulerLock(abs, StaleLockGraceFromConfig(o.Cfg))
	out := SchedulerJob{
		JobID:       jobIDFromMDPath(abs),
		Description: strings.TrimSpace(fm.Description),
		Schedule:    strings.TrimSpace(fm.Schedule),
		Paused:      fm.Paused,
		CWD:         strings.TrimSpace(fm.CWD),
		Model:       strings.TrimSpace(fm.Model),
		Mode:        strings.TrimSpace(fm.Mode),
		Running:     IsTrackedJob(abs),
	}
	if includeBody {
		out.Body = body
	}
	if !last.IsZero() {
		out.LastScheduledSlotUTC = last.UTC().Format(time.RFC3339)
	}
	out.NextRunUTC = next.UTC().Format(time.RFC3339)
	return out, nil
}

// ListJobs returns scheduler envelope plus job summaries.
func (o *Service) ListJobs(includeBody bool) (*JobsListResponse, error) {
	if err := o.requireEnabled(); err != nil {
		return nil, err
	}
	paths, err := storage.ListFlatJobMarkdownFiles(o.Cfg.SchedulerScanRoots())
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	jobs := make([]SchedulerJob, 0, len(paths))
	for _, p := range paths {
		j, err := o.jobFromPath(p, now, includeBody)
		if err != nil {
			continue
		}
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].JobID < jobs[j].JobID })
	return &JobsListResponse{Scheduler: o.buildSchedulerInfo(), Jobs: jobs}, nil
}

// GetJob returns one job.
func (o *Service) GetJob(jobID string) (SchedulerJob, error) {
	if err := o.requireEnabled(); err != nil {
		return SchedulerJob{}, err
	}
	abs, err := o.jobAbsPath(jobID)
	if err != nil {
		return SchedulerJob{}, err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return SchedulerJob{}, ErrJobNotFound
		}
		return SchedulerJob{}, err
	}
	return o.jobFromPath(abs, time.Now().UTC(), true)
}
