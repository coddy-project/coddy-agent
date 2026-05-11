//go:build scheduler

package schedtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/schedulerops"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func toolEnvCWD(env *tooling.Env) string {
	if env == nil {
		return ""
	}
	return strings.TrimSpace(env.CWD)
}

// RegisterTools registers scheduler maintenance tools (requires cfg.Scheduler enabled).
func RegisterTools(reg func(*tooling.Tool), cfg *config.Config) {
	if cfg == nil || !cfg.SchedulerEffectiveEnabled() {
		return
	}
	reg(jobsListTool(cfg))
	reg(jobGetTool(cfg))
	reg(jobPauseTool(cfg))
	reg(jobResumeTool(cfg))
	reg(jobCreateTool(cfg))
	reg(jobReplaceTool(cfg))
	reg(jobPatchTool(cfg))
	reg(jobDeleteTool(cfg))
	reg(jobRunTool(cfg))
	reg(jobCancelTool(cfg))
	reg(jobRunsTool(cfg))
}

func jobsListTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobsList,
			Description: "Lists all scheduler cron jobs configured as flat *.md files under scheduler.dir (YAML frontmatter + markdown body). " +
				"Returns a JSON envelope mirroring GET /coddy/scheduler/jobs: a scheduler info object (enabled, dirs, poll_interval, active run count) plus an array of jobs. " +
				"Call when the user asks what is scheduled or which jobs exist. Prefer over job_get when you need the full collection. " +
				"Uses include_body:false by default to omit large instruction bodies; pass include_body true only when edit text is required.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"include_body": map[string]interface{}{
						"type":        "boolean",
						"description": "When true, includes each job's markdown instruction body in the JSON (heavier). Default false.",
					},
				},
			},
		},
		AllowedInPlanMode: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				IncludeBody bool `json:"include_body"`
			}
			_ = json.Unmarshal([]byte(argsJSON), &in)
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			out, err := op.ListJobs(in.IncludeBody)
			if err != nil {
				return "", err
			}
			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}

func jobGetTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobGet,
			Description: "Loads a single scheduler job by job_id (the *.md basename without path or extension under scheduler.dir). " +
				"Returns full SchedulerJob JSON including the instruction body plus next/last run metadata. " +
				"Use after jobs_list when you need details for one job. Not for listing runs (use coddy_scheduler_job_runs). job_id must not contain slashes or '..'.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{
						"type":        "string",
						"description": "Public job id (file name without .md), e.g. nightly_backup",
					},
				},
				"required": []interface{}{"job_id"},
			},
		},
		AllowedInPlanMode: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			j, err := op.GetJob(strings.TrimSpace(in.JobID))
			if err != nil {
				return "", err
			}
			b, err := json.MarshalIndent(j, "", "  ")
			return string(b), err
		},
	}
}

func jobPauseTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobPause,
			Description: "Pauses ONE scheduler job (sets frontmatter paused:true). Cron ticks and asynchronous manual runs will not execute until resumed. " +
				"This does NOT cancel an active run-in-progress (see coddy_scheduler_job_cancel); it only blocks future executions. Requires permission.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{
						"type":        "string",
						"description": "Flat job basename without slashes",
					},
				},
				"required": []interface{}{"job_id"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			if err := op.PauseJob(strings.TrimSpace(in.JobID)); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"job_id":%q,"paused":true}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}

func jobResumeTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobResume,
			Description: "Clears paused for one scheduler job (paused:false). After resume, cron and manual triggers may run normally again.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{
						"type":        "string",
						"description": "Flat job basename without slashes",
					},
				},
				"required": []interface{}{"job_id"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			if err := op.ResumeJob(strings.TrimSpace(in.JobID)); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"job_id":%q,"paused":false}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}

func jobCreateTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobCreate,
			Description: "Creates a new flat scheduler job markdown file (.md directly under scheduler.dir). " +
				"Provide job_id plus YAML fields description, schedule (5-field cron UTC line), optional cwd/model/mode/paused, " +
				"and markdown instruction body. Validates cron before writing. Conflict if job_id exists. Requires permission.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id":      map[string]interface{}{"type": "string", "description": "New job basename (no slashes)"},
					"description": map[string]interface{}{"type": "string"},
					"schedule":    map[string]interface{}{"type": "string", "description": "5-field cron in UTC"},
					"paused":      map[string]interface{}{"type": "boolean", "description": "When true, job will not run until resumed"},
					"cwd":         map[string]interface{}{"type": "string"},
					"model":       map[string]interface{}{"type": "string"},
					"mode":        map[string]interface{}{"type": "string", "description": "agent or plan"},
					"body":        map[string]interface{}{"type": "string", "description": "Markdown instruction executed as the initial user prompt"},
				},
				"required": []interface{}{"job_id", "description", "schedule", "body"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in schedulerops.SchedulerJobCreate
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			if err := op.CreateJob(in); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_created","job_id":%q}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}

func jobReplaceTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobReplace,
			Description: "Replaces ALL fields of an existing scheduler job (PUT semantics). Requires job_id in the payload path sense (tool argument job_id matches file). " +
				"Dangerous overwrite of description, schedule, paused, cwd, model, mode, and body fields. Prefer coddy_scheduler_job_patch when only a few knobs change.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id":      map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"schedule":    map[string]interface{}{"type": "string", "description": "5-field cron UTC"},
					"paused":      map[string]interface{}{"type": "boolean"},
					"cwd":         map[string]interface{}{"type": "string"},
					"model":       map[string]interface{}{"type": "string"},
					"mode":        map[string]interface{}{"type": "string"},
					"body":        map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"job_id", "description", "schedule", "body"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in schedulerops.SchedulerJobCreate
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			jobID := strings.TrimSpace(in.JobID)
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			if err := op.ReplaceJob(jobID, in); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_replaced","job_id":%q}`, jobID), nil
		},
	}
}

func jobPatchTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobPatch,
			Description: "Partially edits an existing scheduler job (PATCH semantics). Provide only JSON keys you wish to mutate (description, schedule, paused, cwd, model, mode, body). " +
				"Safer than replace when tweaking one field.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id":      map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"schedule":    map[string]interface{}{"type": "string"},
					"paused":      map[string]interface{}{"type": "boolean"},
					"cwd":         map[string]interface{}{"type": "string"},
					"model":       map[string]interface{}{"type": "string"},
					"mode":        map[string]interface{}{"type": "string"},
					"body":        map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"job_id"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			type patchIn struct {
				JobID       string          `json:"job_id"`
				Description json.RawMessage `json:"description"`
				Schedule    json.RawMessage `json:"schedule"`
				Paused      *bool           `json:"paused"`
				CWD         json.RawMessage `json:"cwd"`
				Model       json.RawMessage `json:"model"`
				Mode        json.RawMessage `json:"mode"`
				Body        json.RawMessage `json:"body"`
			}
			var wrap patchIn
			if err := json.Unmarshal([]byte(argsJSON), &wrap); err != nil {
				return "", err
			}
			p := schedulerops.SchedulerJobPatch{}
			if wrap.Description != nil {
				var s string
				_ = json.Unmarshal(wrap.Description, &s)
				p.Description = &s
			}
			if wrap.Schedule != nil {
				var s string
				_ = json.Unmarshal(wrap.Schedule, &s)
				p.Schedule = &s
			}
			if wrap.Paused != nil {
				p.Paused = wrap.Paused
			}
			if wrap.CWD != nil {
				var s string
				_ = json.Unmarshal(wrap.CWD, &s)
				p.CWD = &s
			}
			if wrap.Model != nil {
				var s string
				_ = json.Unmarshal(wrap.Model, &s)
				p.Model = &s
			}
			if wrap.Mode != nil {
				var s string
				_ = json.Unmarshal(wrap.Mode, &s)
				p.Mode = &s
			}
			if wrap.Body != nil {
				var s string
				_ = json.Unmarshal(wrap.Body, &s)
				p.Body = &s
			}
			jobID := strings.TrimSpace(wrap.JobID)
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			if err := op.PatchJob(jobID, p); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_patched","job_id":%q}`, jobID), nil
		},
	}
}

func jobDeleteTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobDelete,
			Description: "Deletes a scheduler job file and its sibling .state and .lock artifacts when idle. Refuses while a run holds the lock or is tracked (409-style error text). Requires permission.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{
						"type":        "string",
						"description": "Flat job basename without slashes",
					},
				},
				"required": []interface{}{"job_id"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			if err := op.DeleteJob(strings.TrimSpace(in.JobID)); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_deleted","job_id":%q}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}

func jobRunTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobRun,
			Description: "Triggers one asynchronous scheduler agent run NOW for the named job using the SAME code path as the daemon (persists transcripts under sessions.dir with scheduler markers). " +
				"This does NOT update the cron-style last-fire .state checkpoint (cron schedule stays honest). Accepts shortly with JSON status accepted; watch coddy_scheduler_job_runs for session ids. Blocked while paused or while another execution holds the exclusive lock.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{
						"type":        "string",
						"description": "Existing flat job basename",
					},
				},
				"required": []interface{}{"job_id"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			if err := op.TriggerJobRun(strings.TrimSpace(in.JobID)); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_run_accepted","job_id":%q,"status":"accepted"}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}

func jobCancelTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobCancel,
			Description: "Requests cancellation for an ACTIVE scheduler-backed agent run linked to job_id via the process-wide run tracker (context.Cancel). Returns JSON bool cancelled=false when nothing was tracked. Different from paused (resume still needed after pause); cancel stops an in-flight run only.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"job_id"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			cancelled, err := op.CancelJobRun(strings.TrimSpace(in.JobID))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_cancel","job_id":%q,"cancelled":%v}`, strings.TrimSpace(in.JobID), cancelled), nil
		},
	}
}

func jobRunsTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolJobRuns,
			Description: "Lists recent persisted scheduler runs for a job_id (metadata only). Each row includes session_id; read full turns with normal session tools or HTTP /coddy/sessions/{session_id}/messages. " +
				"Use when the user wants history, audit, or to debug a recurring job. Optional limit (default 50, max 100).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{"type": "string"},
					"limit":  map[string]interface{}{"type": "integer", "description": "Max rows to return"},
				},
				"required": []interface{}{"job_id"},
			},
		},
		AllowedInPlanMode: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				JobID string `json:"job_id"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedulerops.NewOps(cfg, nil, toolEnvCWD(env))
			runs, err := op.ListJobRuns(strings.TrimSpace(in.JobID), in.Limit)
			if err != nil {
				return "", err
			}
			wrap := map[string]interface{}{
				"object": "coddy.scheduler_job_runs",
				"job_id": strings.TrimSpace(in.JobID),
				"runs":   runs,
			}
			b, err := json.MarshalIndent(wrap, "", "  ")
			return string(b), err
		},
	}
}
