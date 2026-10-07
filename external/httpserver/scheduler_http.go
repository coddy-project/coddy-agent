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
	s.mux.HandleFunc("POST /coddy/scheduler/jobs/{job_id}/trust", s.coddySchedulerJobTrust)
	s.mux.HandleFunc("POST /coddy/scheduler/jobs/{job_id}/untrust", s.coddySchedulerJobUntrust)
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

// schedulerJobAddr resolves the job a per-job route names. Without scope it is
// the user job of that id. With scope=project it is a project job: of the
// workspace query when given (only a workspace the scheduler already scans,
// or the session's own), else of the session in X-Coddy-Session-ID (the
// server's cwd without the header) - the rule of every route that changes a
// workspace, never a raw cwd.
func (s *Server) schedulerJobAddr(w http.ResponseWriter, r *http.Request) (*schedservice.Service, schedservice.JobAddr, bool) {
	op := s.schedulerService()
	id := strings.TrimSpace(r.PathValue("job_id"))
	q := r.URL.Query()
	scope, err := schedservice.NormalizeScope(q.Get("scope"))
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return nil, schedservice.JobAddr{}, false
	}
	if scope != "project" {
		return op, schedservice.UserJob(id), true
	}
	cwd, ok := s.resolveSessionCWD(w, r)
	if !ok {
		return nil, schedservice.JobAddr{}, false
	}
	op = op.WithSession(cwd)
	addr, err := op.ProjectJob(id, q.Get("workspace"))
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return nil, schedservice.JobAddr{}, false
	}
	return op, addr, true
}

func (s *Server) coddySchedulerJobsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	op := s.schedulerService()
	q := r.URL.Query()
	opts := schedservice.ListOptions{
		IncludeBody: strings.EqualFold(strings.TrimSpace(q.Get("include_body")), "true"),
		UserOnly:    strings.EqualFold(strings.TrimSpace(q.Get("scope")), "user"),
	}
	// A session behind the request names the workspace and registers it with
	// the scheduler; a bare cwd (a new chat before its session exists) only
	// reads. Neither lists the user jobs alone, the contract older clients
	// rely on.
	if strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID")) != "" || strings.TrimSpace(q.Get("cwd")) != "" {
		cwd, ok := s.resolveListingCWD(w, r)
		if !ok {
			return
		}
		opts.Workspace = cwd
		if sid := strings.TrimSpace(r.Header.Get("X-Coddy-Session-ID")); sid != "" && s.sessionKnown(sid) {
			op = op.WithSession(cwd)
		}
	}
	out, err := op.ListJobs(opts)
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
	scope, err := schedservice.NormalizeScope(body.Scope)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	if scope == "project" {
		cwd, ok := s.resolveSessionCWD(w, r)
		if !ok {
			return
		}
		op = op.WithSession(cwd)
	}
	if err := op.CreateJob(body); err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	loc := "/coddy/scheduler/jobs/" + url.PathEscape(strings.TrimSpace(body.JobID))
	created := map[string]string{"object": "coddy.scheduler_job", "job_id": strings.TrimSpace(body.JobID), "scope": scope}
	if scope == "project" {
		loc += "?scope=project&workspace=" + url.QueryEscape(op.SessionWorkspace)
		created["workspace"] = op.SessionWorkspace
	}
	w.Header().Set("Location", loc)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created)
}

func (s *Server) coddySchedulerJobGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	job, err := op.GetJob(addr)
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
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	id := addr.ID
	var body schedservice.SchedulerJobCreate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.coddySchedulerWriteErr(w, fmt.Errorf("%w: %v", schedservice.ErrInvalidJobID, err))
		return
	}
	if err := op.ReplaceJob(addr, body); err != nil {
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
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	id := addr.ID
	p, err := schedservice.DecodeSchedulerJobPatch(r.Body)
	if err != nil {
		s.coddySchedulerWriteErr(w, fmt.Errorf("%w: %v", schedservice.ErrInvalidJobID, err))
		return
	}
	if err := op.PatchJob(addr, p); err != nil {
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
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	if err := op.DeleteJob(addr); err != nil {
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
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	id := addr.ID
	if err := op.PauseJob(addr); err != nil {
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
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	id := addr.ID
	if err := op.ResumeJob(addr); err != nil {
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
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	id := addr.ID
	ref, err := op.TriggerJobRun(addr)
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
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	id := addr.ID
	cancelled, err := op.CancelJobRun(addr)
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
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	id := addr.ID
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	runs, err := op.ListJobRuns(addr, limit)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	// The job session exists whether or not any run is left under it.
	sessionID, err := op.JobSessionID(addr)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
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
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	id := addr.ID
	cleared, err := op.ClearJobRuns(addr)
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

func (s *Server) coddySchedulerJobTrust(w http.ResponseWriter, r *http.Request) {
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	var body struct {
		Digest string `json:"digest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.coddySchedulerWriteErr(w, fmt.Errorf("%w: %v", schedservice.ErrInvalidJob, err))
		return
	}
	if err := op.TrustJob(addr, body.Digest); err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.scheduler_job_trust", "job_id": addr.ID, "workspace": addr.Workspace, "trusted": true, "digest": body.Digest,
	})
}

func (s *Server) coddySchedulerJobUntrust(w http.ResponseWriter, r *http.Request) {
	op, addr, ok := s.schedulerJobAddr(w, r)
	if !ok {
		return
	}
	removed, err := op.UntrustJob(addr)
	if err != nil {
		s.coddySchedulerWriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "coddy.scheduler_job_trust", "job_id": addr.ID, "workspace": addr.Workspace, "trusted": false, "removed": removed,
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
			"description": "Scheduler job basename (its *.md file name without the extension).",
		},
		map[string]interface{}{
			"name":        "scope",
			"in":          "query",
			"schema":      map[string]interface{}{"type": "string", "enum": []interface{}{"user", "project"}},
			"description": "user (default): the job in ${CODDY_HOME}/scheduler. project: the job in <workspace>/.coddy/scheduler, the workspace being the `workspace` parameter or the session in X-Coddy-Session-ID (the server's cwd without the header).",
		},
		map[string]interface{}{
			"name":        "workspace",
			"in":          "query",
			"schema":      map[string]string{"type": "string"},
			"description": "With scope=project: the workspace of the job, accepted only when it is one the scheduler already scans (its own cwd, a workspace with an approval, one a session asked about) or the session's own; 403 otherwise.",
		},
		map[string]interface{}{
			"name":        "X-Coddy-Session-ID",
			"in":          "header",
			"required":    false,
			"schema":      map[string]string{"type": "string"},
			"description": "The session whose workspace a project job belongs to when no workspace parameter is given.",
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
					"Optional **`include_body`** (default false) attaches each job markdown instruction **`body`** to list rows. " +
					"Without a session header and without **`cwd`**, only the user jobs (${CODDY_HOME}/scheduler) are listed. With either, the project jobs of every workspace the scheduler scans and of that workspace follow, each with its **`scope`**, **`workspace`**, **`trust`** and **`scheduled`**; a request carrying a session also registers its workspace with the scheduler.",
				"parameters": []interface{}{
					map[string]interface{}{
						"name":        "include_body",
						"in":          "query",
						"schema":      map[string]string{"type": "boolean"},
						"description": "Include heavy markdown instruction bodies.",
					},
					map[string]interface{}{
						"name":        "scope",
						"in":          "query",
						"schema":      map[string]interface{}{"type": "string", "enum": []interface{}{"user"}},
						"description": "user: list the user jobs only, whatever the session.",
					},
					map[string]interface{}{
						"name":        "X-Coddy-Session-ID",
						"in":          "header",
						"required":    false,
						"schema":      map[string]string{"type": "string"},
						"description": "The session whose workspace's project jobs are listed (and which is registered with the scheduler).",
					},
					listingCWDParam(),
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
				"description": "A user job by default. With **`scope: project`** the job is written to <workspace>/.coddy/scheduler of the session in X-Coddy-Session-ID (the server's cwd without the header; a `cwd` query is never honoured here) and approved at once for the bytes written. **409** for an id taken in the scope, a project job named like a user job, or a user job named like a project job of a workspace the scheduler knows.",
				"parameters": []interface{}{
					map[string]interface{}{
						"name":        "X-Coddy-Session-ID",
						"in":          "header",
						"required":    false,
						"schema":      map[string]string{"type": "string"},
						"description": "The session whose workspace a project job is created in.",
					},
				},
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
										"object":    map[string]string{"type": "string"},
										"job_id":    map[string]string{"type": "string"},
										"scope":     map[string]string{"type": "string"},
										"workspace": map[string]string{"type": "string", "description": "Canonical workspace of a project job."},
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
					"200": map[string]interface{}{"description": "Full scheduler job JSON; a project job also carries its raw file (`raw`), which the approval view shows.", "content": jsonApp(jobRef)},
					"400": errorResponseRef(),
					"403": errorResponseRef(),
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
				"description": "Starts a run the way the cron tick does: a background task of kind **agent** under the job's session (**`session_id`**), backed by the run session **`run_session_id`** that holds the transcript. Does not advance the cron **`.state`** checkpoint. **409** while the job is paused, while a run of it is in flight, while **scheduler.max_queue** runs are already going, when a project job is not trusted (not approved, denied, in conflict with a user job, invalid), or when the definition the job names is not trusted for its workspace.",
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
		"/coddy/scheduler/jobs/{job_id}/trust": map[string]interface{}{
			"post": map[string]interface{}{
				"summary":     "Approve a project job for its workspace",
				"description": "Records an approval in ${CODDY_HOME}/scheduler-trust.json bound to the workspace, the job id and the digest the client showed (`digest` of the job row): **409** when the file changed since (the digest no longer matches) or when the job is in conflict with a user job or invalid; **400** for a user job or under scheduler.project_trust deny. Requires scope=project.",
				"parameters":  jobIDParam,
				"requestBody": map[string]interface{}{"required": true, "content": map[string]interface{}{
					"application/json": map[string]interface{}{"schema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{"digest": map[string]string{"type": "string"}},
						"required":   []interface{}{"digest"},
					}},
				}},
				"responses": map[string]interface{}{
					"200": map[string]interface{}{"description": "Approved", "content": jsonApp("#/components/schemas/SchedulerTrustResult")},
					"400": errorResponseRef(),
					"403": errorResponseRef(),
					"404": errorResponseRef(),
					"409": errorResponseRef(),
					"503": errorResponseRef(),
				},
			},
		},
		"/coddy/scheduler/jobs/{job_id}/untrust": map[string]interface{}{
			"post": map[string]interface{}{
				"summary":     "Withdraw the approval of a project job",
				"description": "Removes the approval; the job stops running until approved again. `removed` is false when none was on file. Requires scope=project.",
				"parameters":  jobIDParam,
				"responses": map[string]interface{}{
					"200": map[string]interface{}{"description": "Withdrawn", "content": jsonApp("#/components/schemas/SchedulerTrustResult")},
					"400": errorResponseRef(),
					"403": errorResponseRef(),
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
				"dir":             map[string]string{"type": "string", "description": "Folder of the user jobs, ${CODDY_HOME}/scheduler."},
				"project_dir":     map[string]string{"type": "string", "description": "Folder of a workspace's project jobs, relative to it (.coddy/scheduler)."},
				"project_trust":   map[string]interface{}{"type": "string", "enum": []interface{}{"ask", "allow", "deny"}},
				"workspace":       map[string]string{"type": "string", "description": "The workspace the request named, empty when none."},
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
				"job_id":    map[string]string{"type": "string"},
				"scope":     map[string]interface{}{"type": "string", "enum": []interface{}{"user", "project"}},
				"workspace": map[string]string{"type": "string", "description": "Workspace of a project job."},
				"trust": map[string]interface{}{
					"type":        "string",
					"enum":        []interface{}{"trusted", "needs_approval", "denied", "conflict", "invalid"},
					"description": "Only a trusted job runs.",
				},
				"trust_reason": map[string]string{"type": "string"},
				"digest":       map[string]string{"type": "string", "description": "sha256 of the job file; what an approval is bound to."},
				"scheduled":    map[string]string{"type": "boolean", "description": "False for a project job of a workspace the scheduler does not scan: it will not fire."},
				"raw":          map[string]string{"type": "string", "description": "Raw file of a project job (GET of one job)."},
				"description":  map[string]string{"type": "string"},
				"schedule":     map[string]string{"type": "string"},
				"paused":       map[string]string{"type": "boolean"},
				"cwd":          map[string]string{"type": "string"},
				"model":        map[string]string{"type": "string"},
				"mode":         map[string]string{"type": "string"},
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
		"SchedulerTrustResult": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"object":    map[string]string{"type": "string"},
				"job_id":    map[string]string{"type": "string"},
				"workspace": map[string]string{"type": "string"},
				"trusted":   map[string]string{"type": "boolean"},
				"digest":    map[string]string{"type": "string"},
				"removed":   map[string]string{"type": "boolean"},
			},
		},
		"SchedulerJobCreateDoc": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"scope":           map[string]interface{}{"type": "string", "enum": []interface{}{"user", "project"}},
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
