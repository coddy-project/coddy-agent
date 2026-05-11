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

func jobGetTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobGet,
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
			op := schedservice.NewService(cfg, nil, toolEnvCWD(env))
			j, err := op.GetJob(strings.TrimSpace(in.JobID))
			if err != nil {
				return "", err
			}
			b, err := json.MarshalIndent(j, "", "  ")
			return string(b), err
		},
	}
}
