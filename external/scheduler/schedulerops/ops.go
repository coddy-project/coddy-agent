//go:build scheduler

package schedulerops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sched "github.com/EvilFreelancer/coddy-agent/external/scheduler/lib"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

var (
	ErrSchedulerDisabled       = errors.New("scheduler is disabled in configuration")
	ErrInvalidJobID            = errors.New("invalid job_id")
	ErrJobNotFound             = errors.New("scheduler job not found")
	ErrJobBusy                 = errors.New("scheduler job is running or locked")
	ErrJobExists               = errors.New("scheduler job already exists")
	ErrJobPaused               = errors.New("scheduler job is paused")
	ErrLauncherNotConfigured   = errors.New("scheduler manual launcher not wired")
)

func jobIDFromMDPath(abs string) string {
	return strings.TrimSuffix(filepath.Base(abs), ".md")
}

// Ops centralizes scheduler REST and tool operations.
type Ops struct {
	Cfg        *config.Config
	Log        *slog.Logger
	ProcessCWD string
}

func NewOps(cfg *config.Config, log *slog.Logger, processCWD string) *Ops {
	return &Ops{Cfg: cfg, Log: log, ProcessCWD: processCWD}
}

func (o *Ops) slog() *slog.Logger {
	if o == nil || o.Log == nil {
		return slog.Default()
	}
	return o.Log
}

func (o *Ops) requireEnabled() error {
	if o == nil || o.Cfg == nil || !o.Cfg.SchedulerEffectiveEnabled() {
		return ErrSchedulerDisabled
	}
	return nil
}

// ValidateJobID ensures id is a single path segment safe for {job_id}.md under scheduler.dir.
func ValidateJobID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidJobID
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return ErrInvalidJobID
	}
	if strings.HasPrefix(id, ".") {
		return ErrInvalidJobID
	}
	if filepath.Base(id) != id {
		return ErrInvalidJobID
	}
	return nil
}

func (o *Ops) jobAbsPath(jobID string) (string, error) {
	if err := ValidateJobID(jobID); err != nil {
		return "", err
	}
	roots := o.Cfg.SchedulerScanRoots()
	if len(roots) == 0 || strings.TrimSpace(roots[0]) == "" {
		return "", fmt.Errorf("scheduler.dir is empty")
	}
	return filepath.Join(filepath.Clean(roots[0]), jobID+".md"), nil
}

func lockOrTracked(abs string) bool {
	if _, err := os.Stat(sched.LockPath(abs)); err == nil {
		return true
	}
	return IsTrackedJob(abs)
}

// HTTPErrStatus maps domain errors to HTTP status codes for /coddy/scheduler handlers.
func HTTPErrStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrSchedulerDisabled):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrInvalidJobID):
		return http.StatusBadRequest
	case errors.Is(err, ErrJobNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrJobBusy):
		return http.StatusConflict
	case errors.Is(err, ErrJobExists):
		return http.StatusConflict
	case errors.Is(err, ErrJobPaused):
		return http.StatusConflict
	case errors.Is(err, ErrLauncherNotConfigured):
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// SchedulerInfo is the envelope object returned with GET /coddy/scheduler/jobs.
type SchedulerInfo struct {
	Enabled           bool   `json:"enabled"`
	Dir               string `json:"dir"`
	PollInterval      string `json:"poll_interval"`
	Timeout           string `json:"timeout"`
	MaxQueue          int    `json:"max_queue"`
	RunsActive        int    `json:"runs_active"`
	RetainSessions int `json:"retain_sessions"`
}

// SchedulerJob is the wire shape for one task.
type SchedulerJob struct {
	JobID                string `json:"job_id"`
	Description          string `json:"description,omitempty"`
	Schedule             string `json:"schedule"`
	Paused               bool   `json:"paused"`
	CWD                  string `json:"cwd,omitempty"`
	Model                string `json:"model,omitempty"`
	Mode                 string `json:"mode,omitempty"`
	Body                 string `json:"body,omitempty"`
	LastScheduledSlotUTC string `json:"last_scheduled_slot_utc,omitempty"`
	NextRunUTC           string `json:"next_run_utc,omitempty"`
	Running              bool   `json:"running"`
}

// JobsListResponse is GET /coddy/scheduler/jobs.
type JobsListResponse struct {
	Scheduler SchedulerInfo  `json:"scheduler"`
	Jobs      []SchedulerJob `json:"jobs"`
}

// SchedulerJobCreate is POST /coddy/scheduler/jobs.
type SchedulerJobCreate struct {
	JobID       string `json:"job_id"`
	Description string `json:"description"`
	Schedule    string `json:"schedule"`
	Paused      bool   `json:"paused"`
	CWD         string `json:"cwd,omitempty"`
	Model       string `json:"model,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Body        string `json:"body"`
}

// SchedulerJobPatch is PATCH /coddy/scheduler/jobs/{job_id}.
type SchedulerJobPatch struct {
	Description *string `json:"description"`
	Schedule    *string `json:"schedule"`
	Paused      *bool   `json:"paused"`
	CWD         *string `json:"cwd"`
	Model       *string `json:"model"`
	Mode        *string `json:"mode"`
	Body        *string `json:"body"`
}

// SchedulerRunEntry is one row of GET /coddy/scheduler/jobs/{job_id}/runs.
type SchedulerRunEntry struct {
	SessionID string `json:"session_id"`
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	Status    string `json:"status,omitempty"`
}

func (o *Ops) buildSchedulerInfo() SchedulerInfo {
	c := o.Cfg
	return SchedulerInfo{
		Enabled:           c.SchedulerEffectiveEnabled(),
		Dir:               strings.TrimSpace(c.Scheduler.Dir),
		PollInterval:      strings.TrimSpace(c.Scheduler.PollInterval),
		Timeout:           strings.TrimSpace(c.Scheduler.Timeout),
		MaxQueue:          c.Scheduler.MaxQueue,
		RunsActive:        TrackedJobRunCount(),
		RetainSessions: c.SchedulerRetainSessionsEffective(),
	}
}

func (o *Ops) jobFromPath(abs string, now time.Time, includeBody bool) (SchedulerJob, error) {
	fm, body, err := sched.ParseJobFile(abs)
	if err != nil {
		return SchedulerJob{}, err
	}
	sch, err := sched.ParseCronUTC(fm.Schedule)
	if err != nil {
		return SchedulerJob{}, err
	}
	last, _ := sched.ReadJobState(sched.StatePath(abs))
	next := sched.NextScheduledUTC(sch, last)
	out := SchedulerJob{
		JobID:       jobIDFromMDPath(abs),
		Description: strings.TrimSpace(fm.Description),
		Schedule:    strings.TrimSpace(fm.Schedule),
		Paused:      fm.Paused,
		CWD:         strings.TrimSpace(fm.CWD),
		Model:       strings.TrimSpace(fm.Model),
		Mode:        strings.TrimSpace(fm.Mode),
		Running:     lockOrTracked(abs),
	}
	if includeBody {
		out.Body = body
	}
	if !last.IsZero() {
		out.LastScheduledSlotUTC = last.UTC().Format(time.RFC3339)
	}
	out.NextRunUTC = next.UTC().Format(time.RFC3339)
	if next.After(now) {
		// keep literal next time; clients may compare to now
	}
	return out, nil
}

// ListJobs returns scheduler envelope plus job summaries.
func (o *Ops) ListJobs(includeBody bool) (*JobsListResponse, error) {
	if err := o.requireEnabled(); err != nil {
		return nil, err
	}
	paths, err := sched.ListFlatJobMarkdownFiles(o.Cfg.SchedulerScanRoots())
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
func (o *Ops) GetJob(jobID string) (SchedulerJob, error) {
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

// CreateJob writes a new *.md job file.
func (o *Ops) CreateJob(in SchedulerJobCreate) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	if err := ValidateJobID(in.JobID); err != nil {
		return err
	}
	abs, err := o.jobAbsPath(in.JobID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return ErrJobExists
	} else if !os.IsNotExist(err) {
		return err
	}
	fm := &sched.JobFrontmatter{
		Description: strings.TrimSpace(in.Description),
		Schedule:    strings.TrimSpace(in.Schedule),
		Paused:      in.Paused,
		CWD:         strings.TrimSpace(in.CWD),
		Model:       strings.TrimSpace(in.Model),
		Mode:        strings.TrimSpace(in.Mode),
	}
	if _, err := sched.ParseCronUTC(fm.Schedule); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJobID, err)
	}
	data, err := sched.FormatJobMarkdown(fm, in.Body)
	if err != nil {
		return err
	}
	if _, err := sched.ParseJobFromBytes(data); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

// ReplaceJob overwrites an existing job file.
func (o *Ops) ReplaceJob(jobID string, in SchedulerJobCreate) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	if strings.TrimSpace(in.JobID) != "" && strings.TrimSpace(in.JobID) != jobID {
		return ErrInvalidJobID
	}
	abs, err := o.jobAbsPath(jobID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return ErrJobNotFound
		}
		return err
	}
	fm := &sched.JobFrontmatter{
		Description: strings.TrimSpace(in.Description),
		Schedule:    strings.TrimSpace(in.Schedule),
		Paused:      in.Paused,
		CWD:         strings.TrimSpace(in.CWD),
		Model:       strings.TrimSpace(in.Model),
		Mode:        strings.TrimSpace(in.Mode),
	}
	if _, err := sched.ParseCronUTC(fm.Schedule); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJobID, err)
	}
	data, err := sched.FormatJobMarkdown(fm, in.Body)
	if err != nil {
		return err
	}
	if _, err := sched.ParseJobFromBytes(data); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

// PatchJob merges fields into an existing job file.
func (o *Ops) PatchJob(jobID string, p SchedulerJobPatch) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	abs, err := o.jobAbsPath(jobID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return ErrJobNotFound
		}
		return err
	}
	fm, body, err := sched.ParseJobFile(abs)
	if err != nil {
		return err
	}
	if p.Description != nil {
		fm.Description = strings.TrimSpace(*p.Description)
	}
	if p.Schedule != nil {
		fm.Schedule = strings.TrimSpace(*p.Schedule)
	}
	if p.Paused != nil {
		fm.Paused = *p.Paused
	}
	if p.CWD != nil {
		fm.CWD = strings.TrimSpace(*p.CWD)
	}
	if p.Model != nil {
		fm.Model = strings.TrimSpace(*p.Model)
	}
	if p.Mode != nil {
		fm.Mode = strings.TrimSpace(*p.Mode)
	}
	if p.Body != nil {
		body = strings.TrimRight(*p.Body, "\n")
	}
	if _, err := sched.ParseCronUTC(fm.Schedule); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJobID, err)
	}
	data, err := sched.FormatJobMarkdown(fm, body)
	if err != nil {
		return err
	}
	if _, err := sched.ParseJobFromBytes(data); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

// DeleteJob removes job markdown and sidecars when idle.
func (o *Ops) DeleteJob(jobID string) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	abs, err := o.jobAbsPath(jobID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return ErrJobNotFound
		}
		return err
	}
	if lockOrTracked(abs) {
		return ErrJobBusy
	}
	_ = os.Remove(sched.LockPath(abs))
	_ = os.Remove(sched.StatePath(abs))
	if err := os.Remove(abs); err != nil {
		return err
	}
	return nil
}

// PauseJob sets paused:true in frontmatter without starting a run.
func (o *Ops) PauseJob(jobID string) error {
	v := true
	return o.PatchJob(jobID, SchedulerJobPatch{Paused: &v})
}

// ResumeJob sets paused:false in frontmatter.
func (o *Ops) ResumeJob(jobID string) error {
	v := false
	return o.PatchJob(jobID, SchedulerJobPatch{Paused: &v})
}

// TriggerJobRun starts an asynchronous scheduler run without advancing cron last-fire state.
func (o *Ops) TriggerJobRun(jobID string) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	abs, err := o.jobAbsPath(jobID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return ErrJobNotFound
		}
		return err
	}
	if lockOrTracked(abs) {
		return ErrJobBusy
	}
	fm, body, err := sched.ParseJobFile(abs)
	if err != nil {
		return err
	}
	if fm.Paused {
		return ErrJobPaused
	}
	if LaunchManualJob == nil {
		return ErrLauncherNotConfigured
	}
	logRef := o.slog()
	proc := strings.TrimSpace(o.ProcessCWD)
	fmCopy := *fm
	go func(f sched.JobFrontmatter, instruction string) {
		ff := f
		_ = LaunchManualJob(context.Background(), o.Cfg, logRef, proc, abs, &ff, instruction)
	}(fmCopy, body)
	return nil
}

// CancelJobRun asks the active run for this job path to cancel.
func (o *Ops) CancelJobRun(jobID string) (cancelled bool, err error) {
	if err := o.requireEnabled(); err != nil {
		return false, err
	}
	abs, err := o.jobAbsPath(jobID)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return false, ErrJobNotFound
		}
		return false, err
	}
	return CancelTrackedRun(abs), nil
}

// ListJobRuns returns persisted scheduler run sessions for jobID from sessions.dir.
func (o *Ops) ListJobRuns(jobID string, limit int) ([]SchedulerRunEntry, error) {
	if err := o.requireEnabled(); err != nil {
		return nil, err
	}
	absJob, err := o.jobAbsPath(jobID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absJob); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrJobNotFound
		}
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	fs := &session.FileStore{Root: o.Cfg.ResolvedSessionsRoot()}
	if fs.Root == "" {
		return nil, fmt.Errorf("sessions root empty")
	}
	de, err := os.ReadDir(fs.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return []SchedulerRunEntry{}, nil
		}
		return nil, err
	}
	jobID = strings.TrimSpace(jobID)
	var entries []SchedulerRunEntry
	for _, ent := range de {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		snap, err := fs.ReadSnapshot(ent.Name())
		if err != nil {
			continue
		}
		m := snap.Meta
		if !m.SchedulerRun || strings.TrimSpace(m.SchedulerJobID) != jobID {
			continue
		}
		entries = append(entries, SchedulerRunEntry{
			SessionID: m.ID,
			StartedAt: strings.TrimSpace(m.SchedulerStartedAt),
			EndedAt:   strings.TrimSpace(m.SchedulerEndedAt),
			Status:    strings.TrimSpace(m.SchedulerStopStatus),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StartedAt > entries[j].StartedAt
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

// DecodeSchedulerJobPatch reads PATCH JSON (empty body yields zero patch).
func DecodeSchedulerJobPatch(r io.Reader) (SchedulerJobPatch, error) {
	var p SchedulerJobPatch
	dec := json.NewDecoder(r)
	if err := dec.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return SchedulerJobPatch{}, nil
		}
		return SchedulerJobPatch{}, err
	}
	return p, nil
}
