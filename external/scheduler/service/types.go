//go:build scheduler

package schedservice

// SchedulerInfo is the envelope object returned with GET /coddy/scheduler/jobs.
type SchedulerInfo struct {
	Enabled        bool   `json:"enabled"`
	Dir            string `json:"dir"`
	PollInterval   string `json:"poll_interval"`
	Timeout        string `json:"timeout"`
	MaxQueue       int    `json:"max_queue"`
	RunsActive     int    `json:"runs_active"`
	RetainSessions int    `json:"retain_sessions"`
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
