//go:build scheduler

package schedservice

// SchedulerInfo is the envelope object returned with GET /coddy/scheduler/jobs.
type SchedulerInfo struct {
	Enabled        bool   `json:"enabled"`
	Dir            string `json:"dir"`
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
	Agent                string `json:"agent,omitempty"`
	PermissionMode       string `json:"permission_mode,omitempty"`
	Body                 string `json:"body,omitempty"`
	LastScheduledSlotUTC string `json:"last_scheduled_slot_utc,omitempty"`
	NextRunUTC           string `json:"next_run_utc,omitempty"`
	Running              bool   `json:"running"`
	// SessionID is the job session every run of the job is a child of: what
	// the runs panel polls for its task rows. Empty until the first run.
	SessionID string `json:"session_id,omitempty"`
	// LastRun is the newest run of the job, in flight or finished.
	LastRun *SchedulerRunEntry `json:"last_run,omitempty"`
}

// JobsListResponse is GET /coddy/scheduler/jobs.
type JobsListResponse struct {
	Scheduler SchedulerInfo  `json:"scheduler"`
	Jobs      []SchedulerJob `json:"jobs"`
}

// SchedulerJobCreate is POST /coddy/scheduler/jobs.
type SchedulerJobCreate struct {
	JobID          string `json:"job_id"`
	Description    string `json:"description"`
	Schedule       string `json:"schedule"`
	Paused         bool   `json:"paused"`
	CWD            string `json:"cwd,omitempty"`
	Model          string `json:"model,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Agent          string `json:"agent,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Body           string `json:"body"`
}

// SchedulerJobPatch is PATCH /coddy/scheduler/jobs/{job_id}.
// JobID, when set to a value different from the path job_id, renames the job file and sidecars.
type SchedulerJobPatch struct {
	JobID          *string `json:"job_id"`
	Description    *string `json:"description"`
	Schedule       *string `json:"schedule"`
	Paused         *bool   `json:"paused"`
	CWD            *string `json:"cwd"`
	Model          *string `json:"model"`
	Mode           *string `json:"mode"`
	Agent          *string `json:"agent"`
	PermissionMode *string `json:"permission_mode"`
	Body           *string `json:"body"`
}

// SchedulerRunEntry is one run of a job: a row of
// GET /coddy/scheduler/jobs/{job_id}/runs and the last_run of a job row. It is
// the run's background task seen from the job: the task under the job session
// and the run session that holds the transcript.
type SchedulerRunEntry struct {
	// TaskID is the background task of the run, under JobSessionID.
	TaskID string `json:"task_id"`
	// SessionID is the run session: the transcript a client opens.
	SessionID string `json:"session_id"`
	// JobSessionID is the job session the task belongs to.
	JobSessionID string `json:"job_session_id"`
	// Trigger is cron or manual.
	Trigger string `json:"trigger,omitempty"`
	// Status is the task's status: running, succeeded, failed, timed_out,
	// stopped or orphaned.
	Status string `json:"status"`
	// Running reports a run still in flight.
	Running bool `json:"running"`
	// StartedAt and EndedAt are RFC3339 UTC; EndedAt is empty while running.
	StartedAt string `json:"started_at,omitempty"`
	EndedAt   string `json:"ended_at,omitempty"`
	// ElapsedSeconds is the run's duration so far, or in total.
	ElapsedSeconds int `json:"elapsed_seconds"`
	// Error is the run's error text when it ended with one.
	Error string `json:"error,omitempty"`
	// Label is the task label: the job and how the run started.
	Label string `json:"label,omitempty"`
}
