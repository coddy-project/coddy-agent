//go:build scheduler

package schedservice

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
)

var (
	ErrSchedulerDisabled     = errors.New("scheduler is disabled in configuration")
	ErrInvalidJobID          = errors.New("invalid job_id")
	ErrInvalidJob            = errors.New("invalid scheduler job")
	ErrJobNotFound           = errors.New("scheduler job not found")
	ErrJobBusy               = errors.New("scheduler job is running")
	ErrJobExists             = errors.New("scheduler job already exists")
	ErrJobPaused             = errors.New("scheduler job is paused")
	ErrQueueSaturated        = errors.New("scheduler.max_queue runs are already in flight")
	ErrRunRefused            = errors.New("scheduler run refused")
	ErrLauncherNotConfigured = errors.New("the scheduler daemon is not running in this process")
	// ErrJobUntrusted refuses a run (or an approval) of a project job that is
	// not trusted: not approved, denied, in conflict with a user job, or
	// invalid.
	ErrJobUntrusted = errors.New("scheduler job is not trusted")
	// ErrDigestMismatch refuses an approval whose digest is not the job's
	// current one: the file changed after the client showed it.
	ErrDigestMismatch = errors.New("the job file changed since it was shown; reload it and approve again")
	// ErrTrustNotApplicable refuses an approval that has no meaning: a user
	// job, or a project job under scheduler.project_trust deny.
	ErrTrustNotApplicable = errors.New("this job takes no approval")
	// ErrWorkspaceUnknown refuses a request naming a workspace the daemon
	// does not scan, without a session in it.
	ErrWorkspaceUnknown = errors.New("workspace is not one the scheduler knows; open a session in it first")
)

// HTTPErrStatus maps domain errors to HTTP status codes for /coddy/scheduler handlers.
func HTTPErrStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrSchedulerDisabled):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrLauncherNotConfigured):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrInvalidJobID), errors.Is(err, ErrInvalidJob), errors.Is(err, ErrTrustNotApplicable),
		errors.Is(err, storage.ErrUnsafePath):
		return http.StatusBadRequest
	case errors.Is(err, ErrWorkspaceUnknown):
		return http.StatusForbidden
	case errors.Is(err, ErrJobNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrJobBusy), errors.Is(err, ErrJobExists), errors.Is(err, ErrJobPaused),
		errors.Is(err, ErrQueueSaturated), errors.Is(err, ErrRunRefused), errors.Is(err, ErrJobUntrusted),
		errors.Is(err, ErrDigestMismatch):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// IsClientError reports whether err is one of the domain errors a handler
// answers with its own status and message, so it is not logged as a failure.
func IsClientError(err error) bool {
	return err != nil && HTTPErrStatus(err) != http.StatusInternalServerError
}

// ValidateJobID ensures id is a single path segment safe for {job_id}.md in a
// job folder.
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
