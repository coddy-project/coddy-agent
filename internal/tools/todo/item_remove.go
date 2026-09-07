package todo

import (
	"context"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ItemRemoveTool deletes one plan row by index.
func ItemRemoveTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        ToolNameItemRemove,
			Description: "Remove one todo plan item by zero-based index. Later items shift down.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Zero-based index of the row to remove.",
					},
				},
				"required": []string{"index"},
			},
		},
		Execute: execItemRemove,
	}
}

type itemRemoveArgs struct {
	Index int `json:"index"`
}

func execItemRemove(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[itemRemoveArgs](argsJSON)
	if err != nil {
		return "", err
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}
	n := len(entries)
	if n == 0 {
		return "", fmt.Errorf("%s: plan is empty", ToolNameItemRemove)
	}
	if args.Index < 0 || args.Index >= n {
		return "", fmt.Errorf("%s: index %d out of range (plan has %d items)", ToolNameItemRemove, args.Index, n)
	}

	next := append(entries[:args.Index:args.Index], entries[args.Index+1:]...)

	if env.SetPlan != nil {
		env.SetPlan(next)
	}
	sendPlanUpdate(env, next)

	return fmt.Sprintf("removed item at index %d (%d items remaining)", args.Index, len(next)), nil
}
