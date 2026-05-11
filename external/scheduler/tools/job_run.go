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
			Description: "Triggers one asynchronous scheduler agent run NOW for the named job using the SAME code path as the daemon (persists transcripts under sessions.dir with scheduler markers). " +
				"This does NOT update the cron-style last-fire .state checkpoint (cron schedule stays honest). Accepts shortly with JSON status accepted; watch coddy_scheduler_job_runs for session ids. Blocked while paused or while another execution holds the exclusive lock.",
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
			if err := op.TriggerJobRun(strings.TrimSpace(in.JobID)); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_run_accepted","job_id":%q,"status":"accepted"}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}
