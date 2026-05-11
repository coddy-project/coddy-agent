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

func jobPatchTool(cfg *config.Config) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: toolJobPatch,
			Description: "Partially edits an existing scheduler job (PATCH semantics). Provide only JSON keys you wish to mutate (description, schedule, paused, cwd, model, mode, body). " +
				"Safer than replace when tweaking one field.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"job_id":      map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"schedule":    map[string]interface{}{"type": "string"},
					"paused":      map[string]interface{}{"type": "boolean"},
					"cwd":         map[string]interface{}{"type": "string"},
					"model":       map[string]interface{}{"type": "string"},
					"mode":        map[string]interface{}{"type": "string"},
					"body":        map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"job_id"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			type patchIn struct {
				JobID       string          `json:"job_id"`
				Description json.RawMessage `json:"description"`
				Schedule    json.RawMessage `json:"schedule"`
				Paused      *bool           `json:"paused"`
				CWD         json.RawMessage `json:"cwd"`
				Model       json.RawMessage `json:"model"`
				Mode        json.RawMessage `json:"mode"`
				Body        json.RawMessage `json:"body"`
			}
			var wrap patchIn
			if err := json.Unmarshal([]byte(argsJSON), &wrap); err != nil {
				return "", err
			}
			p := schedservice.SchedulerJobPatch{}
			if wrap.Description != nil {
				var s string
				_ = json.Unmarshal(wrap.Description, &s)
				p.Description = &s
			}
			if wrap.Schedule != nil {
				var s string
				_ = json.Unmarshal(wrap.Schedule, &s)
				p.Schedule = &s
			}
			if wrap.Paused != nil {
				p.Paused = wrap.Paused
			}
			if wrap.CWD != nil {
				var s string
				_ = json.Unmarshal(wrap.CWD, &s)
				p.CWD = &s
			}
			if wrap.Model != nil {
				var s string
				_ = json.Unmarshal(wrap.Model, &s)
				p.Model = &s
			}
			if wrap.Mode != nil {
				var s string
				_ = json.Unmarshal(wrap.Mode, &s)
				p.Mode = &s
			}
			if wrap.Body != nil {
				var s string
				_ = json.Unmarshal(wrap.Body, &s)
				p.Body = &s
			}
			jobID := strings.TrimSpace(wrap.JobID)
			op := schedservice.NewService(cfg, nil, toolEnvCWD(env))
			if err := op.PatchJob(jobID, p); err != nil {
				return "", err
			}
			return fmt.Sprintf(`{"object":"coddy.scheduler_job_patched","job_id":%q}`, jobID), nil
		},
	}
}
