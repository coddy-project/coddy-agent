//go:build scheduler

package schedtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func jobPauseTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobPause,
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
			op := schedservice.NewService(cfg, nil, toolEnvCWD(env))
			if err := op.PauseJob(strings.TrimSpace(in.JobID)); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"job_id":%q,"paused":true}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}
