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

func jobReplaceTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobReplace,
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
			var in schedservice.SchedulerJobCreate
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			jobID := strings.TrimSpace(in.JobID)
			op := schedservice.NewService(cfg, nil, toolEnvCWD(env))
			if err := op.ReplaceJob(jobID, in); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_replaced","job_id":%q}`, jobID), nil
		},
	}
}
