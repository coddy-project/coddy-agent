package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// GetListTool returns the current todo/plan list as JSON and a markdown preview.
func GetListTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "get_todo_list",
			Description: "Read the current todo/plan list without changing it. " +
				"Returns JSON entries plus a markdown checklist reflecting current statuses.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		AllowedInPlanMode: true,
		Execute:           execGetTodoList,
	}
}

func execGetTodoList(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	type empty struct{}
	in := strings.TrimSpace(argsJSON)
	if in != "" && in != "{}" {
		if _, err := tooling.ParseArgs[empty](argsJSON); err != nil {
			return "", err
		}
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", fmt.Errorf("get_todo_list: %w", err)
	}

	md := FormatTodoMarkdown(entries)
	return fmt.Sprintf("%s\n\nMarkdown:\n%s", string(data), md), nil
}
