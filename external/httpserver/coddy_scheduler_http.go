//go:build http && scheduler

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
)

func (s *Server) registerSchedulerRoutes() {
	s.mux.HandleFunc("GET /coddy/scheduler/jobs", s.coddySchedulerJobsList)
	s.mux.HandleFunc("POST /coddy/scheduler/jobs", s.coddySchedulerJobsPost)
	s.mux.HandleFunc("GET /coddy/scheduler/jobs/{job_id}", s.coddySchedulerJobGet)
	s.mux.HandleFunc("PUT /coddy/scheduler/jobs/{job_id}", s.coddySchedulerJobPut)
	s.mux.HandleFunc("PATCH /coddy/scheduler/jobs/{job_id}", s.coddySchedulerJobPatchHTTP)
	s.mux.HandleFunc("DELETE /coddy/scheduler/jobs/{job_id}", s.coddySchedulerJobDelete)
	s.mux.HandleFunc("POST /coddy/scheduler/jobs/{job_id}/pause", s.coddySchedulerJobPause)
	s.mux.HandleFunc("POST /coddy/scheduler/jobs/{job_id}/resume", s.coddySchedulerJobResume)
	s.mux.HandleFunc("POST /coddy/scheduler/jobs/{job_id}/run", s.coddySchedulerJobRunPost)
	s.mux.HandleFunc("POST /coddy/scheduler/jobs/{job_id}/cancel", s.coddySchedulerJobCancelPost)
	s.mux.HandleFunc("GET /coddy/scheduler/jobs/{job_id}/runs", s.coddySchedulerJobRunsGet)
}

func (s *Server) coddySchedulerWriteErr(w http.ResponseWriter, err error) {
	code := schedservice.HTTPErrStatus(err)
	if code == http.StatusInternalServerError && !errors.Is(err, schedservice.ErrSchedulerDisabled) &&
		!errors.Is(err, schedservice.ErrJobNotFound) && !errors.Is(err, schedservice.ErrInvalidJobID) &&
		!errors.Is(err, schedservice.ErrJobBusy) && !errors.Is(err, schedservice.ErrJobExists) &&
		!errors.Is(err, schedservice.ErrJobPaused) {
		s.log.Error("coddy_scheduler", "error", err)
	}
	msg := err.Error()
	if code == http.StatusInternalServerError {
		msg = "internal error"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{"message": msg},
	})
}

func (s *Server) schedulerService() *schedservice.Service {
	return schedservice.NewService(s.cfg, s.log, s.defaultCWD)
}

func (s *Server) coddySchedulerJobsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	op := s.schedulerService()
	includeBody := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_body")), "true")
	out, err := op.ListJobs(includeBody)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) coddySchedulerJobsPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body schedservice.SchedulerJobCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.coddySchedulerWriteErr(w, fmt.Errorf("%w: %v", schedservice.ErrInvalidJobID, err))
		return
	}
	op := s.schedulerService()
	if err := op.CreateJob(body); err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	loc := "/coddy/scheduler/jobs/" + url.PathEscape(strings.TrimSpace(body.JobID))
	w.Header().Set("Location", loc)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.scheduler_job", "job_id": strings.TrimSpace(body.JobID)})
}

func (s *Server) coddySchedulerJobGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	op := s.schedulerService()
	job, err := op.GetJob(id)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

func (s *Server) coddySchedulerJobPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	var body schedservice.SchedulerJobCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.coddySchedulerWriteErr(w, fmt.Errorf("%w: %v", schedservice.ErrInvalidJobID, err))
		return
	}
	op := s.schedulerService()
	if err := op.ReplaceJob(id, body); err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.scheduler_job", "job_id": id})
}

func (s *Server) coddySchedulerJobPatchHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	p, err := schedservice.DecodeSchedulerJobPatch(r.Body)
	if err != nil {
		s.coddySchedulerWriteErr(w, fmt.Errorf("%w: %v", schedservice.ErrInvalidJobID, err))
		return
	}
	op := s.schedulerService()
	if err := op.PatchJob(id, p); err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.scheduler_job", "job_id": id})
}

func (s *Server) coddySchedulerJobDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	op := s.schedulerService()
	if err := op.DeleteJob(id); err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) coddySchedulerJobPause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	op := s.schedulerService()
	if err := op.PauseJob(id); err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.scheduler_job", "job_id": id})
}

func (s *Server) coddySchedulerJobResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	op := s.schedulerService()
	if err := op.ResumeJob(id); err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.scheduler_job", "job_id": id})
}

func (s *Server) coddySchedulerJobRunPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	op := s.schedulerService()
	if err := op.TriggerJobRun(id); err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.scheduler_job_run_accepted",
		"job_id": id,
		"status": "accepted",
	}); err != nil {
		s.log.Error("coddy_scheduler_encode", "error", err)
	}
}

func (s *Server) coddySchedulerJobCancelPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	op := s.schedulerService()
	cancelled, err := op.CancelJobRun(id)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":     "coddy.scheduler_job_cancel",
		"job_id":     id,
		"cancelled": cancelled,
	})
}

func (s *Server) coddySchedulerJobRunsGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	op := s.schedulerService()
	runs, err := op.ListJobRuns(id, limit)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.scheduler_job_runs",
		"job_id": id,
		"runs":   runs,
	})
}
