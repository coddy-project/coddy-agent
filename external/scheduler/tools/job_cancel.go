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

func jobCancelTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        toolJobCancel,
			Description: "Stops the run of job_id that is in flight: the run's background task is stopped and recorded as stopped in the job's run history. Returns JSON bool cancelled=false when the job was not running. Different from pause: a paused job still needs resume, a cancelled run is simply over.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id": map[string]interface{}{"type": "string"},
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
			cancelled, err := op.CancelJobRun(strings.TrimSpace(in.JobID))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_cancel","job_id":%q,"cancelled":%v}`, strings.TrimSpace(in.JobID), cancelled), nil
		},
	}
}
