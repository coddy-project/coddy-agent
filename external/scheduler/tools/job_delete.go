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

func jobDeleteTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobDelete,
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
			op := schedservice.NewService(cfg, nil, toolEnvCWD(env))
			if err := op.DeleteJob(strings.TrimSpace(in.JobID)); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_deleted","job_id":%q}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}
