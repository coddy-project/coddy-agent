//go:build scheduler

package schedservice

import (
	"context"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// Trigger values a run carries: how it was started.
const (
	TriggerCron   = "cron"
	TriggerManual = "manual"
)

// RunRequest is what the service hands the daemon to start one run of a job.
type RunRequest struct {
	// JobPath is the canonical absolute path of the job's markdown file.
	JobPath string
	// Frontmatter and Body are the parsed job file.
	Frontmatter *storage.JobFrontmatter
	Body        string
	// Trigger is TriggerCron or TriggerManual.
	Trigger string
	// FireSlot is the committed UTC minute of a cron fire; zero for a manual
	// run.
	FireSlot time.Time
	// UpdateState says the cron checkpoint of the job is advanced to FireSlot
	// before the run starts. A manual run leaves the cron timing alone.
	UpdateState bool
}

// RunRef identifies a run of a job that this process started.
type RunRef struct {
	JobID        string    `json:"job_id"`
	JobSessionID string    `json:"session_id"`
	TaskID       string    `json:"task_id"`
	RunSessionID string    `json:"run_session_id"`
	Trigger      string    `json:"trigger"`
	StartedAt    time.Time `json:"started_at"`
}

// Runtime is what the scheduler daemon lends the service: starting, stopping
// and observing the runs of this process, and the run history kept in the job
// sessions. The daemon registers its runtime when it starts and withdraws it
// when it stops; the HTTP handlers and the tools reach the daemon through
// nothing else, and a process with no daemon answers ErrLauncherNotConfigured.
type Runtime interface {
	// StartRun starts one run of a job and returns as soon as its task is
	// registered. ErrJobBusy while a run of the job is in flight,
	// ErrQueueSaturated when scheduler.max_queue runs are already going,
	// ErrRunRefused (with the reason) when the job's definition may not run.
	StartRun(ctx context.Context, req RunRequest) (RunRef, error)
	// CancelRun stops the run of the job that is in flight; false when none is.
	CancelRun(jobPath string) bool
	// RunningRun reports the run of the job in flight, if any.
	RunningRun(jobPath string) (RunRef, bool)
	// RunningCount reports how many runs are in flight across every job.
	RunningCount() int
	// Pool is the background task pool the runs are tasks of: what the run
	// rows are read from, so the service and the daemon never disagree on
	// which pool holds a run in flight.
	Pool() *bgtask.Pool
	// ClearRuns removes every finished run of a job - task record and
	// transcript - and reports how many went.
	ClearRuns(jobPath string) (int, error)
	// DeleteJobHistory removes the job session with every run under it, for a
	// job being deleted.
	DeleteJobHistory(jobPath string) error
}

var (
	runtimeMu sync.RWMutex
	runtime   Runtime
)

// SetRuntime installs the daemon's runtime, or withdraws it with nil.
func SetRuntime(rt Runtime) {
	runtimeMu.Lock()
	runtime = rt
	runtimeMu.Unlock()
}

// CurrentRuntime returns the daemon's runtime, or nil when no daemon runs in
// this process.
func CurrentRuntime() Runtime {
	runtimeMu.RLock()
	defer runtimeMu.RUnlock()
	return runtime
}
