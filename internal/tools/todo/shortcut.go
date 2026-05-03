package todo

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// DoneTodoItemTool marks one item as completed by index (shortcut for status completed).
func DoneTodoItemTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "done_todo_item",
			Description: "Mark a todo list item as completed using its zero-based index. " +
				"Same effect as update_todo_item with status \"completed\".",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Zero-based index of the item.",
					},
				},
				"required": []string{"index"},
			},
		},
		AllowedInPlanMode: true,
		Execute:           execDoneTodoItem,
	}
}

// UndoneTodoItemTool clears completion for one item by index (sets status to pending).
func UndoneTodoItemTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "undone_todo_item",
			Description: "Mark a todo list item as not done using its zero-based index (sets status to pending). " +
				"Use when reopening or correcting completion.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Zero-based index of the item.",
					},
				},
				"required": []string{"index"},
			},
		},
		AllowedInPlanMode: true,
		Execute:           execUndoneTodoItem,
	}
}

// CleanTodoListTool clears the entire todo/plan list.
func CleanTodoListTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "clean_todo_list",
			Description: "Remove all items from the current todo/plan list. Use when abandoning or resetting tracking.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		AllowedInPlanMode: true,
		Execute:           execCleanTodoList,
	}
}

type indexArgs struct {
	Index int `json:"index"`
}

func execDoneTodoItem(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	return setTodoStatusAtIndex(argsJSON, env, "completed", "done_todo_item")
}

func execUndoneTodoItem(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	return setTodoStatusAtIndex(argsJSON, env, "pending", "undone_todo_item")
}

func setTodoStatusAtIndex(argsJSON string, env *tooling.Env, status, toolName string) (string, error) {
	args, err := tooling.ParseArgs[indexArgs](argsJSON)
	if err != nil {
		return "", err
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}

	if args.Index < 0 || args.Index >= len(entries) {
		return "", fmt.Errorf("%s: index %d is out of range (plan has %d items)", toolName, args.Index, len(entries))
	}

	entries[args.Index].Status = status

	if env.SetPlan != nil {
		env.SetPlan(entries)
	}
	sendPlanUpdate(env, entries)

	return fmt.Sprintf("%s: item %d -> %s", toolName, args.Index, status), nil
}

func execCleanTodoList(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	in := strings.TrimSpace(argsJSON)
	if in != "" && in != "{}" {
		type empty struct{}
		if _, err := tooling.ParseArgs[empty](argsJSON); err != nil {
			return "", err
		}
	}

	entries := []acp.PlanEntry{}
	if env.SetPlan != nil {
		env.SetPlan(entries)
	}
	sendPlanUpdate(env, entries)

	return "cleaned todo list (0 items)", nil
}
