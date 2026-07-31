package todo

import (
	"context"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"

	"slices"
)

// ItemMoveTool moves a plan row by index inside the shortened slice semantics (final to_index).
func ItemMoveTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolNameItemMove,
			Description: "Move a todo plan row. from_index selects the row. " +
				"to_index is the insertion index inside the slice after removing the row " +
				"(same semantics as Go slices.Insert on the shortened list).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from_index": map[string]interface{}{
						"type":        "integer",
						"description": "Current zero-based index of the row to move.",
					},
					"to_index": map[string]interface{}{
						"type":        "integer",
						"description": "Target zero-based index where the row should land after removal.",
					},
				},
				"required": []string{"from_index", "to_index"},
			},
		},
		Execute: execItemMove,
	}
}

type itemMoveArgs struct {
	FromIndex int `json:"from_index"`
	ToIndex   int `json:"to_index"`
}

func execItemMove(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[itemMoveArgs](argsJSON)
	if err != nil {
		return "", err
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}
	n := len(entries)
	if n == 0 {
		return "", fmt.Errorf("%s: plan is empty", ToolNameItemMove)
	}
	if args.FromIndex < 0 || args.FromIndex >= n || args.ToIndex < 0 || args.ToIndex >= n {
		return "", fmt.Errorf("%s: from_index=%d to_index=%d invalid for %d items", ToolNameItemMove, args.FromIndex, args.ToIndex, n)
	}
	if args.FromIndex == args.ToIndex {
		return fmt.Sprintf("item %d unchanged (from_index equals to_index)", args.FromIndex), nil
	}

	out := slices.Clone(entries)
	row := out[args.FromIndex]
	rest := slices.Delete(out, args.FromIndex, args.FromIndex+1)
	next := slices.Insert(rest, args.ToIndex, row)

	if env.SetPlan != nil {
		env.SetPlan(next)
	}
	sendPlanUpdate(env, next)

	return fmt.Sprintf("moved item from index %d to index %d", args.FromIndex, args.ToIndex), nil
}
