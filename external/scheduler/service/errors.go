//go:build scheduler

package schedservice

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
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
	case errors.Is(err, ErrInvalidJobID), errors.Is(err, ErrInvalidJob):
		return http.StatusBadRequest
	case errors.Is(err, ErrJobNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrJobBusy), errors.Is(err, ErrJobExists), errors.Is(err, ErrJobPaused),
		errors.Is(err, ErrQueueSaturated), errors.Is(err, ErrRunRefused):
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
