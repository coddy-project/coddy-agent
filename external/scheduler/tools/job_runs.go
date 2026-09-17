//go:build scheduler

package schedtools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func jobRunsTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobRuns,
			Description: "Lists the runs of a job, newest first: the run in flight and the finished ones retention kept (scheduler.retain_sessions). Each row carries task_id (the background task under the job's session), session_id (the run's transcript session: read it with HTTP /coddy/sessions/{session_id}/messages), trigger (cron or manual), status (running, succeeded, failed, timed_out, stopped, orphaned), started_at, ended_at, elapsed_seconds and error. " +
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
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				JobID string `json:"job_id"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedservice.NewService(cfg, nil, toolEnvCWD(env))
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
