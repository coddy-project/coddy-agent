//go:build scheduler

package schedtools

import (
	"context"
	"encoding/json"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func jobGetTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobGet,
			Description: "Loads a single scheduler job by job_id (the *.md basename without path or extension) and scope (user or project). " +
				"Returns full SchedulerJob JSON including the instruction body plus next/last run metadata. " +
				"Use after jobs_list when you need details for one job. Not for listing runs (use coddy_scheduler_job_runs). job_id must not contain slashes or '..'.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": scopeProperty(),
					"job_id": map[string]interface{}{
						"type":        "string",
						"description": "Public job id (file name without .md), e.g. nightly_backup",
					},
				},
				"required": []interface{}{"job_id"},
			},
		},
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				JobID string `json:"job_id"`
				Scope string `json:"scope"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := toolService(cfg, env)
			addr, err := toolAddr(op, in.Scope, in.JobID)
			if err != nil {
				return "", err
			}
			j, err := op.GetJob(addr)
			if err != nil {
				return "", err
			}
			b, err := json.MarshalIndent(j, "", "  ")
			return string(b), err
		},
	}
}
