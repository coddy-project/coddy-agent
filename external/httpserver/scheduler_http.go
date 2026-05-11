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
		"object":    "coddy.scheduler_job_cancel",
		"job_id":    id,
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

func mergeOpenAPISchedulerDoc(doc *map[string]interface{}) {
	if doc == nil {
		return
	}
	pathsAny, ok := (*doc)["paths"].(map[string]interface{})
	if ok {
		for k, v := range openAPISchedulerPaths() {
			pathsAny[k] = v
		}
	}
	compAny, ok := (*doc)["components"].(map[string]interface{})
	if !ok {
		return
	}
	schemasAny, ok := compAny["schemas"].(map[string]interface{})
	if !ok {
		return
	}
	for k, v := range openAPISchedulerSchemas() {
		schemasAny[k] = v
	}
}

func openAPISchedulerPaths() map[string]interface{} {
	jobIDParam := []interface{}{
		map[string]interface{}{
			"name":        "job_id",
			"in":          "path",
			"required":    true,
			"schema":      map[string]string{"type": "string"},
			"description": "Scheduler job basename (filename without .md under scheduler.dir).",
		},
	}
	jobRef := "#/components/schemas/SchedulerJobFull"
	jobCreateRef := "#/components/schemas/SchedulerJobCreateDoc"
	jobPatchRef := "#/components/schemas/SchedulerJobPatchDoc"
	jsonApp := func(ref string) map[string]interface{} {
		return map[string]interface{}{
			"application/json": map[string]interface{}{
				"schema": map[string]interface{}{"$ref": ref},
			},
		}
	}
	return map[string]interface{}{
		"/coddy/scheduler/jobs": map[string]interface{}{
			"get": map[string]interface{}{
				"summary": "List scheduler jobs and status envelope",
				"description": "Requires coddy compiled with **`scheduler`** support. Missing tag yields **404** at runtime on these paths; this OpenAPI fragment is emitted only when the feature is compiled in. " +
					"Optional **`include_body`** (default false) attaches each job markdown instruction **`body`** to list rows.",
				"parameters": []interface{}{
					map[string]interface{}{
						"name":        "include_body",
						"in":          "query",
						"schema":      map[string]string{"type": "boolean"},
						"description": "Include heavy markdown instruction bodies.",
					},
				},
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "`SchedulerJobsListEnvelope` (`scheduler` + `jobs`).",
						"content":     jsonApp("#/components/schemas/SchedulerJobsListEnvelope"),
					},
					"503": errorResponseRef(),
					"500": errorResponseRef(),
				},
			},
			"post": map[string]interface{}{
				"summary":     "Create scheduler job",
				"requestBody": map[string]interface{}{"required": true, "content": jsonApp(jobCreateRef)},
				"responses": map[string]interface{}{
					"201": map[string]interface{}{
						"description": "Created",
						"headers": map[string]interface{}{
							"Location": map[string]interface{}{
								"description": "`/coddy/scheduler/jobs/{job_id}`",
								"schema":      map[string]string{"type": "string"},
							},
						},
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"object": map[string]string{"type": "string"},
										"job_id": map[string]string{"type": "string"},
									},
								},
							},
						},
					},
					"400": errorResponseRef(),
					"409": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
		},
		"/coddy/scheduler/jobs/{job_id}": map[string]interface{}{
			"get": map[string]interface{}{
				"summary":    "Get one scheduler job",
				"parameters": jobIDParam,
				"responses": map[string]interface{}{
					"200": map[string]interface{}{"description": "Full scheduler job JSON", "content": jsonApp(jobRef)},
					"400": errorResponseRef(),
					"404": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
			"put": map[string]interface{}{
				"summary":     "Replace scheduler job file",
				"parameters":  jobIDParam,
				"requestBody": map[string]interface{}{"required": true, "content": jsonApp(jobCreateRef)},
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Replaced",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"object": map[string]string{"type": "string"},
										"job_id": map[string]string{"type": "string"},
									},
								},
							},
						},
					},
					"400": errorResponseRef(),
					"404": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
			"patch": map[string]interface{}{
				"summary":     "Patch scheduler job file",
				"parameters":  jobIDParam,
				"requestBody": map[string]interface{}{"required": true, "content": jsonApp(jobPatchRef)},
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Patched",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"object": map[string]string{"type": "string"},
										"job_id": map[string]string{"type": "string"},
									},
								},
							},
						},
					},
					"400": errorResponseRef(),
					"404": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
			"delete": map[string]interface{}{
				"summary":    "Delete scheduler job markdown and sidecars",
				"parameters": jobIDParam,
				"responses": map[string]interface{}{
					"204": map[string]interface{}{"description": "Removed"},
					"400": errorResponseRef(),
					"404": errorResponseRef(),
					"409": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
		},
		"/coddy/scheduler/jobs/{job_id}/pause": map[string]interface{}{
			"post": map[string]interface{}{
				"summary":    "Pause scheduler job execution",
				"parameters": jobIDParam,
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Paused (`paused` YAML true)",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type":       "object",
									"properties": map[string]interface{}{"object": map[string]string{"type": "string"}, "job_id": map[string]string{"type": "string"}},
								},
							},
						},
					},
					"404": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
		},
		"/coddy/scheduler/jobs/{job_id}/resume": map[string]interface{}{
			"post": map[string]interface{}{
				"summary":    "Resume scheduler job execution",
				"parameters": jobIDParam,
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Resumed",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type":       "object",
									"properties": map[string]interface{}{"object": map[string]string{"type": "string"}, "job_id": map[string]string{"type": "string"}},
								},
							},
						},
					},
					"404": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
		},
		"/coddy/scheduler/jobs/{job_id}/run": map[string]interface{}{
			"post": map[string]interface{}{
				"summary":    "Trigger asynchronous scheduler-backed agent run once",
				"parameters": jobIDParam,
				"responses": map[string]interface{}{
					"202": map[string]interface{}{
						"description": "Accepted (runs in-process). Does not mutate cron *.state checkpoints.",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"object": map[string]string{"type": "string"},
										"job_id": map[string]string{"type": "string"},
										"status": map[string]string{"type": "string", "example": "accepted"},
									},
								},
							},
						},
					},
					"404": errorResponseRef(),
					"409": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
		},
		"/coddy/scheduler/jobs/{job_id}/cancel": map[string]interface{}{
			"post": map[string]interface{}{
				"summary":     "Cancel tracked scheduler run or clear orphan lock",
				"description": "**cancelled** is true when an in-process run received **context.Cancel**, or when a stale **basename.lock** was removed because no run is tracked (crash recovery).",
				"parameters":  jobIDParam,
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Cancellation or lock cleanup result",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"object":    map[string]string{"type": "string"},
										"job_id":    map[string]string{"type": "string"},
										"cancelled": map[string]string{"type": "boolean"},
									},
								},
							},
						},
					},
					"404": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
		},
		"/coddy/scheduler/jobs/{job_id}/runs": map[string]interface{}{
			"get": map[string]interface{}{
				"summary":     "List persisted scheduler run sessions for job",
				"description": "Returns metadata keyed by **`session_id`**. Inspect transcripts via existing **`GET /coddy/sessions/{id}/messages`** after selecting a **`session_id`**. Scheduler runs omit default composer lists unless **`include_scheduler=true`** on **`GET /coddy/sessions**`.",
				"parameters": append(append([]interface{}{}, jobIDParam...), map[string]interface{}{
					"name":        "limit",
					"in":          "query",
					"schema":      map[string]string{"type": "integer"},
					"description": "Max rows (default 50, capped 100 server-side)",
				}),
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Run metadata envelope",
						"content":     jsonApp("#/components/schemas/SchedulerRunsEnvelope"),
					},
					"404": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
		},
	}
}

func openAPISchedulerSchemas() map[string]interface{} {
	return map[string]interface{}{
		"SchedulerInfoDoc": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"enabled":         map[string]string{"type": "boolean"},
				"dir":             map[string]string{"type": "string"},
				"poll_interval":   map[string]string{"type": "string"},
				"timeout":         map[string]string{"type": "string"},
				"max_queue":       map[string]string{"type": "integer"},
				"runs_active":     map[string]string{"type": "integer"},
				"retain_sessions": map[string]string{"type": "integer"},
			},
		},
		"SchedulerJobListRow": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id":                  map[string]string{"type": "string"},
				"description":             map[string]string{"type": "string"},
				"schedule":                map[string]string{"type": "string"},
				"paused":                  map[string]string{"type": "boolean"},
				"cwd":                     map[string]string{"type": "string"},
				"model":                   map[string]string{"type": "string"},
				"mode":                    map[string]string{"type": "string"},
				"body":                    map[string]string{"type": "string"},
				"last_scheduled_slot_utc": map[string]string{"type": "string"},
				"next_run_utc":            map[string]string{"type": "string"},
				"running": map[string]interface{}{
					"type":        "boolean",
					"description": "True while this process tracks an in-flight agent run for the job (not merely presence of basename.lock).",
				},
			},
		},
		"SchedulerJobFull": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id":                  map[string]string{"type": "string"},
				"description":             map[string]string{"type": "string"},
				"schedule":                map[string]string{"type": "string"},
				"paused":                  map[string]string{"type": "boolean"},
				"cwd":                     map[string]string{"type": "string"},
				"model":                   map[string]string{"type": "string"},
				"mode":                    map[string]string{"type": "string"},
				"body":                    map[string]string{"type": "string"},
				"last_scheduled_slot_utc": map[string]string{"type": "string"},
				"next_run_utc":            map[string]string{"type": "string"},
				"running": map[string]interface{}{
					"type":        "boolean",
					"description": "True while this process tracks an in-flight agent run for the job (not merely presence of basename.lock).",
				},
			},
		},
		"SchedulerJobCreateDoc": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id":      map[string]string{"type": "string"},
				"description": map[string]string{"type": "string"},
				"schedule":    map[string]string{"type": "string"},
				"paused":      map[string]string{"type": "boolean"},
				"cwd":         map[string]string{"type": "string"},
				"model":       map[string]string{"type": "string"},
				"mode":        map[string]string{"type": "string"},
				"body":        map[string]string{"type": "string"},
			},
			"required": []interface{}{"job_id", "description", "schedule", "body"},
		},
		"SchedulerJobPatchDoc": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"description": map[string]string{"type": "string"},
				"schedule":    map[string]string{"type": "string"},
				"paused":      map[string]string{"type": "boolean"},
				"cwd":         map[string]string{"type": "string"},
				"model":       map[string]string{"type": "string"},
				"mode":        map[string]string{"type": "string"},
				"body":        map[string]string{"type": "string"},
			},
		},
		"SchedulerJobsListEnvelope": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"scheduler": map[string]interface{}{"$ref": "#/components/schemas/SchedulerInfoDoc"},
				"jobs": map[string]interface{}{
					"type":  "array",
					"items": map[string]interface{}{"$ref": "#/components/schemas/SchedulerJobListRow"},
				},
			},
		},
		"SchedulerRunRow": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"session_id": map[string]string{"type": "string"},
				"started_at": map[string]string{"type": "string"},
				"ended_at":   map[string]string{"type": "string"},
				"status":     map[string]string{"type": "string"},
			},
		},
		"SchedulerRunsEnvelope": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"object": map[string]string{"type": "string"},
				"job_id": map[string]string{"type": "string"},
				"runs": map[string]interface{}{
					"type":  "array",
					"items": map[string]interface{}{"$ref": "#/components/schemas/SchedulerRunRow"},
				},
			},
		},
	}
}
