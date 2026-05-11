//go:build http && scheduler

package httpserver

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
						"content":       jsonApp("#/components/schemas/SchedulerJobsListEnvelope"),
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
										"object":  map[string]string{"type": "string"},
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
										"object":  map[string]string{"type": "string"},
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
										"object":  map[string]string{"type": "string"},
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
				"summary":    "Cancel tracked in-flight scheduler run",
				"parameters": jobIDParam,
				"responses": map[string]interface{}{
					"200": map[string]interface{}{
						"description": "Cancellation request issued",
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
				"summary": "List persisted scheduler run sessions for job",
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
				"enabled":               map[string]string{"type": "boolean"},
				"dir":                   map[string]string{"type": "string"},
				"poll_interval":         map[string]string{"type": "string"},
				"timeout":               map[string]string{"type": "string"},
				"max_queue":             map[string]string{"type": "integer"},
				"runs_active":           map[string]string{"type": "integer"},
				"retain_sessions":       map[string]string{"type": "integer"},
			},
		},
		"SchedulerJobListRow": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id":                     map[string]string{"type": "string"},
				"description":              map[string]string{"type": "string"},
				"schedule":                   map[string]string{"type": "string"},
				"paused":                     map[string]string{"type": "boolean"},
				"cwd":                        map[string]string{"type": "string"},
				"model":                      map[string]string{"type": "string"},
				"mode":                       map[string]string{"type": "string"},
				"body":                       map[string]string{"type": "string"},
				"last_scheduled_slot_utc": map[string]string{"type": "string"},
				"next_run_utc":             map[string]string{"type": "string"},
				"running":                  map[string]string{"type": "boolean"},
			},
		},
		"SchedulerJobFull": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"job_id":                     map[string]string{"type": "string"},
				"description":              map[string]string{"type": "string"},
				"schedule":                   map[string]string{"type": "string"},
				"paused":                     map[string]string{"type": "boolean"},
				"cwd":                        map[string]string{"type": "string"},
				"model":                      map[string]string{"type": "string"},
				"mode":                       map[string]string{"type": "string"},
				"body":                       map[string]string{"type": "string"},
				"last_scheduled_slot_utc": map[string]string{"type": "string"},
				"next_run_utc":             map[string]string{"type": "string"},
				"running":                  map[string]string{"type": "boolean"},
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
