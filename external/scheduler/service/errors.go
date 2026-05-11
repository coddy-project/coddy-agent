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
	ErrJobNotFound           = errors.New("scheduler job not found")
	ErrJobBusy               = errors.New("scheduler job is running or locked")
	ErrJobExists             = errors.New("scheduler job already exists")
	ErrJobPaused             = errors.New("scheduler job is paused")
	ErrLauncherNotConfigured = errors.New("scheduler manual launcher not wired")
)

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
