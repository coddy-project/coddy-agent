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

func jobCreateTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobCreate,
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
			var in schedservice.SchedulerJobCreate
			if err := json.Unmarshal([]byte(argsJSON), &in); err != nil {
				return "", err
			}
			op := schedservice.NewService(cfg, nil, toolEnvCWD(env))
			if err := op.CreateJob(in); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_created","job_id":%q}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}
