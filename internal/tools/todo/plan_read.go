package todo

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// PlanReadTool returns the todo plan checklist as markdown.
func PlanReadTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        ToolNamePlanRead,
			Description: "Read the current todo plan as a markdown checklist without changing it.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Execute: execPlanRead,
	}
}

func execPlanRead(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	in := strings.TrimSpace(argsJSON)
	if in != "" && in != "{}" {
		type empty struct{}
		if _, err := tooling.ParseArgs[empty](argsJSON); err != nil {
			return "", err
		}
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}

	return FormatPlanMarkdown(entries), nil
}
