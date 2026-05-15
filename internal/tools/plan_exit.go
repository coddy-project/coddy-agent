package tools

import (
	"context"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// PlanExitTool switches the session from plan mode to agent mode after planning is done.
func PlanExitTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "plan_exit",
			Description: "Leave plan mode and switch the session to agent mode so the full toolset is available for implementation.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		RequiresPermission: false,
		Execute:            executePlanExit,
	}
}

func executePlanExit(_ context.Context, _ string, env *tooling.Env) (string, error) {
	if env.SetSessionMode == nil {
		return "", fmt.Errorf("plan_exit is not available in this runtime")
	}
	if err := env.SetSessionMode("agent"); err != nil {
		return "", err
	}
	return "switched session to agent mode", nil
}
