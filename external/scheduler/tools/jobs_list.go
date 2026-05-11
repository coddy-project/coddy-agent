//go:build scheduler

package schedtools

import (
	"context"
	"encoding/json"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func jobsListTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobsList,
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
			op := schedservice.NewService(cfg, nil, toolEnvCWD(env))
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
