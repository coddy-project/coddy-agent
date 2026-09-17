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

func jobRunTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobRun,
			Description: "Starts one run of the named job NOW, as a background agent task under the job's session: the same run the cron tick starts, so the run appears in the job's run history with its progress log and its transcript. " +
				"Does NOT advance the cron checkpoint. Answers at once with the task id (task_id) and the run's transcript session (run_session_id); follow it with coddy_scheduler_job_runs. Refused while the job is paused, while another run of it is in flight, or while scheduler.max_queue runs are already going.",
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
			op := schedservice.NewService(cfg, nil, toolEnvCWD(env))
			ref, err := op.TriggerJobRun(strings.TrimSpace(in.JobID))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_run_accepted","job_id":%q,"status":"accepted","task_id":%q,"session_id":%q,"run_session_id":%q}`,
				strings.TrimSpace(in.JobID), ref.TaskID, ref.JobSessionID, ref.RunSessionID), nil
		},
	}
}
