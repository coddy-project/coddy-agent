//go:build scheduler

package schedtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func jobResumeTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        toolJobResume,
			Description: "Clears paused for one scheduler job (paused:false). After resume, cron and manual triggers may run normally again.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": scopeProperty(),
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
			if err := op.ResumeJob(addr); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"job_id":%q,"paused":false}`, strings.TrimSpace(in.JobID)), nil
		},
	}
}
