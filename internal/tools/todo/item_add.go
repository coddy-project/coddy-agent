package todo

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"

	"slices"
)

// ItemAddTool appends or inserts one plan step.
func ItemAddTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolNameItemAdd,
			Description: "Add one item to the todo plan. Omit after_index to append at the end. " +
				"after_index=-1 inserts at the top (before index 0). " +
				"Otherwise inserted immediately after after_index.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Task text.",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"pending", "in_progress", "completed", "failed", "cancelled"},
						"description": "Optional; defaults to pending.",
					},
					"after_index": map[string]interface{}{
						"type":        "integer",
						"description": "Optional. Omit to append. -1 = prepend before first row. Else insert after this 0-based index.",
					},
				},
				"required": []string{"content"},
			},
		},
		Execute: execItemAdd,
	}
}

type itemAddArgs struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	AfterIndex *int   `json:"after_index,omitempty"`
}

func execItemAdd(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[itemAddArgs](argsJSON)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(args.Content)
	if text == "" {
		return "", fmt.Errorf("%s: content must not be empty", ToolNameItemAdd)
	}
	status := strings.TrimSpace(args.Status)
	if status == "" {
		status = "pending"
	}
	if !ValidPlanStepStatuses[status] {
		return "", fmt.Errorf("%s: invalid status %q", ToolNameItemAdd, status)
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}

	newEntry := acp.PlanEntry{Content: text, Status: status}
	var next []acp.PlanEntry

	switch {
	case args.AfterIndex == nil:
		next = append(slices.Clone(entries), newEntry)
	case *args.AfterIndex == -1:
		next = slices.Insert(slices.Clone(entries), 0, newEntry)
	default:
		ai := *args.AfterIndex
		if ai < 0 || ai >= len(entries) {
			return "", fmt.Errorf("%s: after_index %d out of range (plan has %d items)", ToolNameItemAdd, ai, len(entries))
		}
		insertAt := ai + 1
		next = slices.Insert(slices.Clone(entries), insertAt, newEntry)
	}

	if env.SetPlan != nil {
		env.SetPlan(next)
	}
	sendPlanUpdate(env, next)
	return fmt.Sprintf("added plan item (%d rows total)", len(next)), nil
}
