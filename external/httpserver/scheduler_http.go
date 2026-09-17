//go:build http && scheduler

package httpserver

import (
	"encoding/json"
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
	s.mux.HandleFunc("DELETE /coddy/scheduler/jobs/{job_id}/runs", s.coddySchedulerJobRunsClear)
}

func (s *Server) coddySchedulerWriteErr(w http.ResponseWriter, err error) {
	code := schedservice.HTTPErrStatus(err)
	if !schedservice.IsClientError(err) {
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
	return schedservice.NewService(s.activeCfg(), s.log, s.defaultCWD)
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
	outID := id
	if p.JobID != nil {
		if v := strings.TrimSpace(*p.JobID); v != "" {
			outID = v
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"object": "coddy.scheduler_job", "job_id": outID})
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
	ref, err := op.TriggerJobRun(id)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"object":         "coddy.scheduler_job_run_accepted",
		"job_id":         id,
		"status":         "accepted",
		"task_id":        ref.TaskID,
		"session_id":     ref.JobSessionID,
		"run_session_id": ref.RunSessionID,
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
	sessionID := ""
	if len(runs) > 0 {
		sessionID = runs[0].JobSessionID
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":     "coddy.scheduler_job_runs",
		"job_id":     id,
		"session_id": sessionID,
		"runs":       runs,
	})
}

func (s *Server) coddySchedulerJobRunsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.PathValue("job_id"))
	op := s.schedulerService()
	cleared, err := op.ClearJobRuns(id)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object":  "coddy.scheduler_job_runs_cleared",
		"job_id":  id,
		"cleared": cleared,
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
				"summary":     "Start one run of the job now",
				"description": "Starts a run the way the cron tick does: a background task of kind **agent** under the job's session (**`session_id`**), backed by the run session **`run_session_id`** that holds the transcript. Does not advance the cron **`.state`** checkpoint. **409** while the job is paused, while a run of it is in flight, while **scheduler.max_queue** runs are already going, or when the definition the job names is not trusted for its workspace.",
				"parameters":  jobIDParam,
				"responses": map[string]interface{}{
					"202": map[string]interface{}{
						"description": "Accepted: the run is registered and going.",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"object":         map[string]string{"type": "string"},
										"job_id":         map[string]string{"type": "string"},
										"status":         map[string]string{"type": "string", "example": "accepted"},
										"task_id":        map[string]string{"type": "string", "description": "The background task under the job session."},
										"session_id":     map[string]string{"type": "string", "description": "The job session: poll GET /coddy/sessions/{session_id}/background-tasks for the run rows."},
										"run_session_id": map[string]string{"type": "string", "description": "The run session: GET /coddy/sessions/{run_session_id}/messages is its transcript."},
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
				"summary":     "Stop the run of the job that is in flight",
				"description": "Stops the run's background task; the run is recorded as **stopped** in the job's history. **cancelled** is false when the job was not running.",
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
				"summary":     "List the runs of a job",
				"description": "The runs of the job, newest first: the run in flight and the finished ones **scheduler.retain_sessions** kept. Each row is the run's background task seen from the job: **`task_id`** under the job session (**`job_session_id`**, also the envelope's **`session_id`**), and **`session_id`** the run session whose transcript **`GET /coddy/sessions/{session_id}/messages`** serves (read-only). The same rows are what **`GET /coddy/sessions/{job_session_id}/background-tasks`** lists, which is what the runs panel polls.",
				"parameters": append(append([]interface{}{}, jobIDParam...), map[string]interface{}{
					"name":        "limit",
					"in":          "query",
					"schema":      map[string]string{"type": "integer"},
					"description": "Max rows (default 50, capped 100 server-side)",
				}),
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Run rows envelope",
						"content":     jsonApp("#/components/schemas/SchedulerRunsEnvelope"),
					},
					"404": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
			"delete": map[string]interface{}{
				"summary":     "Clear the finished runs of a job",
				"description": "Removes every finished run of the job - the task record and the run's transcript - and answers how many went. A run in flight stays. This is what the runs panel's Clear does; **`DELETE /coddy/sessions/{job_session_id}/background-tasks`** would leave the transcripts behind.",
				"parameters":  jobIDParam,
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Cleared count",
						"content": map[string]interface{}{
							"application/json": map[string]interface{}{
								"schema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"object":  map[string]string{"type": "string"},
										"job_id":  map[string]string{"type": "string"},
										"cleared": map[string]string{"type": "integer"},
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
	}
}

func openAPISchedulerSchemas() map[string]interface{} {
	return map[string]interface{}{
		"SchedulerInfoDoc": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"enabled":         map[string]string{"type": "boolean"},
				"dir":             map[string]string{"type": "string"},
				"timeout":         map[string]string{"type": "string"},
				"max_queue":       map[string]string{"type": "integer"},
				"runs_active":     map[string]string{"type": "integer"},
				"retain_sessions": map[string]string{"type": "integer"},
			},
		},
		"SchedulerJobListRow": map[string]interface{}{"$ref": "#/components/schemas/SchedulerJobFull"},
		"SchedulerJobFull": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id":      map[string]string{"type": "string"},
				"description": map[string]string{"type": "string"},
				"schedule":    map[string]string{"type": "string"},
				"paused":      map[string]string{"type": "boolean"},
				"cwd":         map[string]string{"type": "string"},
				"model":       map[string]string{"type": "string"},
				"mode":        map[string]string{"type": "string"},
				"agent": map[string]interface{}{
					"type":        "string",
					"description": "Subagent definition the run is made under (role, tool allowlist, model, permission narrowing); empty runs a general agent.",
				},
				"permission_mode": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"ask", "accept_edits", "bypass"},
					"description": "What a run may do without asking; empty is bypass. Under ask or accept_edits a gated call is denied, nobody being there to answer.",
				},
				"body":                    map[string]string{"type": "string"},
				"last_scheduled_slot_utc": map[string]string{"type": "string"},
				"next_run_utc":            map[string]string{"type": "string"},
				"running": map[string]interface{}{
					"type":        "boolean",
					"description": "True while a run of the job is in flight in this process.",
				},
				"session_id": map[string]interface{}{
					"type":        "string",
					"description": "The job session every run is a child of: GET /coddy/sessions/{session_id}/background-tasks lists the runs. Empty until the first run.",
				},
				"last_run": map[string]interface{}{"$ref": "#/components/schemas/SchedulerRunRow"},
			},
		},
		"SchedulerJobCreateDoc": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id":          map[string]string{"type": "string"},
				"description":     map[string]string{"type": "string"},
				"schedule":        map[string]string{"type": "string"},
				"paused":          map[string]string{"type": "boolean"},
				"cwd":             map[string]string{"type": "string"},
				"model":           map[string]string{"type": "string"},
				"mode":            map[string]string{"type": "string"},
				"agent":           map[string]string{"type": "string"},
				"permission_mode": map[string]interface{}{"type": "string", "enum": []interface{}{"ask", "accept_edits", "bypass"}},
				"body":            map[string]string{"type": "string"},
			},
			"required": []interface{}{"job_id", "description", "schedule", "body"},
		},
		"SchedulerJobPatchDoc": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id": map[string]interface{}{
					"type":        "string",
					"description": "New job id (renames the on-disk job file and its .state sidecar when different from the path job_id; the run history follows).",
				},
				"description":     map[string]string{"type": "string"},
				"schedule":        map[string]string{"type": "string"},
				"paused":          map[string]string{"type": "boolean"},
				"cwd":             map[string]string{"type": "string"},
				"model":           map[string]string{"type": "string"},
				"mode":            map[string]string{"type": "string"},
				"agent":           map[string]string{"type": "string"},
				"permission_mode": map[string]interface{}{"type": "string", "enum": []interface{}{"ask", "accept_edits", "bypass"}},
				"body":            map[string]string{"type": "string"},
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
				"task_id":         map[string]string{"type": "string", "description": "The background task under the job session."},
				"session_id":      map[string]string{"type": "string", "description": "The run session: its transcript is GET /coddy/sessions/{session_id}/messages (read-only)."},
				"job_session_id":  map[string]string{"type": "string"},
				"trigger":         map[string]interface{}{"type": "string", "enum": []interface{}{"cron", "manual"}},
				"status":          map[string]interface{}{"type": "string", "enum": []interface{}{"running", "succeeded", "failed", "timed_out", "stopped", "orphaned"}},
				"running":         map[string]string{"type": "boolean"},
				"started_at":      map[string]string{"type": "string"},
				"ended_at":        map[string]string{"type": "string"},
				"elapsed_seconds": map[string]string{"type": "integer"},
				"error":           map[string]string{"type": "string"},
				"label":           map[string]string{"type": "string"},
			},
		},
		"SchedulerRunsEnvelope": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"object":     map[string]string{"type": "string"},
				"job_id":     map[string]string{"type": "string"},
				"session_id": map[string]string{"type": "string", "description": "The job session; empty when the job never ran."},
				"runs": map[string]interface{}{
					"type":  "array",
					"items": map[string]interface{}{"$ref": "#/components/schemas/SchedulerRunRow"},
				},
			},
		},
	}
}
