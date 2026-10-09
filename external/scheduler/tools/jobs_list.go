//go:build scheduler

package schedtools

import (
	schedservice "github.com/EvilFreelancer/coddy-agent/external/scheduler/service"

	"context"
	"encoding/json"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func jobsListTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobsList,
			Description: "Lists the scheduler cron jobs: the user jobs in ${CODDY_HOME}/scheduler and the project jobs (scope project) of this session's workspace (<cwd>/.coddy/scheduler) and of the other workspaces the scheduler runs. " +
				"Returns a JSON envelope mirroring GET /coddy/scheduler/jobs: a scheduler info object (enabled, dir, project_dir, project_trust, timeout, max_queue, runs_active, retain_sessions) plus an array of jobs, each with its scope, workspace and trust (only a trusted job runs; a project job that is not approved needs the operator, there is no tool for that). " +
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
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var in struct {
				IncludeBody bool `json:"include_body"`
			}
			_ = json.Unmarshal([]byte(argsJSON), &in)
			op := toolService(cfg, env)
			out, err := op.ListJobs(schedservice.ListOptions{IncludeBody: in.IncludeBody, Workspace: toolEnvCWD(env)})
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
