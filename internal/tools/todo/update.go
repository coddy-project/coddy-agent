package todo

import (
	"context"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// UpdateItemTool updates one todo entry by index.
func UpdateItemTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "update_todo_item",
			Description: "Update the status of a todo list item by its zero-based index. " +
				"Use this to mark steps as in_progress, completed, failed, or cancelled as you work through the plan.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Zero-based index of the item to update.",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"pending", "in_progress", "completed", "failed", "cancelled"},
						"description": "New status for the item.",
					},
				},
				"required": []string{"index", "status"},
			},
		},
		AllowedInPlanMode: true,
		Execute:           execUpdateTodoItem,
	}
}

type updateTodoArgs struct {
	Index  int    `json:"index"`
	Status string `json:"status"`
}

func execUpdateTodoItem(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[updateTodoArgs](argsJSON)
	if err != nil {
		return "", err
	}

	if !ValidTodoStatuses[args.Status] {
		return "", fmt.Errorf("invalid status %q: must be one of pending, in_progress, completed, failed, cancelled", args.Status)
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}

	if args.Index < 0 || args.Index >= len(entries) {
		return "", fmt.Errorf("index %d is out of range (plan has %d items)", args.Index, len(entries))
	}

	entries[args.Index].Status = args.Status

	if env.SetPlan != nil {
		env.SetPlan(entries)
	}
	sendPlanUpdate(env, entries)

	return fmt.Sprintf("updated item %d to status %q", args.Index, args.Status), nil
}
