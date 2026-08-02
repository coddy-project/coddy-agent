package todo

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ItemUpdateTool updates text and/or status for one plan row.
func ItemUpdateTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolNameItemUpdate,
			Description: "Update one todo plan item by index. Provide content and/or status. " +
				"At least one of content or status must be set.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Zero-based index of the row.",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "New task text when changing the label.",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"pending", "in_progress", "completed", "failed", "cancelled"},
						"description": "New status when progressing the row.",
					},
				},
				"required": []string{"index"},
			},
		},
		Execute: execItemUpdate,
	}
}

type itemUpdateArgs struct {
	Index   int     `json:"index"`
	Content *string `json:"content,omitempty"`
	Status  *string `json:"status,omitempty"`
}

func execItemUpdate(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[itemUpdateArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if args.Content == nil && args.Status == nil {
		return "", fmt.Errorf("%s: provide content and/or status", ToolNameItemUpdate)
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}
	n := len(entries)
	if n == 0 {
		return "", fmt.Errorf("%s: plan is empty", ToolNameItemUpdate)
	}
	if args.Index < 0 || args.Index >= n {
		return "", fmt.Errorf("%s: index %d out of range (plan has %d items)", ToolNameItemUpdate, args.Index, n)
	}

	row := entries[args.Index]
	if args.Content != nil {
		c := strings.TrimSpace(*args.Content)
		if c == "" {
			return "", fmt.Errorf("%s: content must not be empty when provided", ToolNameItemUpdate)
		}
		row.Content = c
	}
	if args.Status != nil {
		st := strings.TrimSpace(*args.Status)
		if !ValidPlanStepStatuses[st] {
			return "", fmt.Errorf("%s: invalid status %q", ToolNameItemUpdate, st)
		}
		row.Status = st
	}
	entries[args.Index] = row

	if env.SetPlan != nil {
		env.SetPlan(entries)
	}
	sendPlanUpdate(env, entries)

	return fmt.Sprintf("updated item %d", args.Index), nil
}
