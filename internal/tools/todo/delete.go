package todo

import (
	"context"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// DeleteItemTool removes a todo item by index.
func DeleteItemTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "delete_todo_item",
			Description: "Remove one todo/plan entry by zero-based index. " +
				"Shifts later items down in index.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Zero-based index of the item to remove.",
					},
				},
				"required": []string{"index"},
			},
		},
		AllowedInPlanMode: true,
		Execute:           execDeleteTodoItem,
	}
}

type deleteTodoArgs struct {
	Index int `json:"index"`
}

func execDeleteTodoItem(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[deleteTodoArgs](argsJSON)
	if err != nil {
		return "", err
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}

	n := len(entries)
	if n == 0 {
		return "", fmt.Errorf("cannot delete from empty todo list")
	}
	if args.Index < 0 || args.Index >= n {
		return "", fmt.Errorf("index %d is out of range (plan has %d items)", args.Index, n)
	}

	next := append(entries[:args.Index:args.Index], entries[args.Index+1:]...)

	if env.SetPlan != nil {
		env.SetPlan(next)
	}
	sendPlanUpdate(env, next)

	return fmt.Sprintf("deleted item %d (%d items remaining)", args.Index, len(next)), nil
}
