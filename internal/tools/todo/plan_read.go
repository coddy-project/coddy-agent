package todo

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// planReadEmptyResult is what the tool answers when the session has no plan.
// FormatPlanMarkdown stays "" for an empty plan (the system prompt block, the
// persisted active.md and the plan update all rely on that); the tool alone
// turns it into a sentence, because an empty tool result gives a model nothing
// to act on and some models call the same tool again until the loop guard stops
// the turn.
const planReadEmptyResult = "plan read: no active items (the todo plan is empty); to start one, call " + ToolNamePlanReplace

// PlanReadTool returns the todo plan checklist as markdown.
func PlanReadTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolNamePlanRead,
			Description: "Read the current todo plan as a markdown checklist without changing it. " +
				"When no plan is active it returns a note saying so, not an empty answer.",
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

	if len(entries) == 0 {
		return planReadEmptyResult, nil
	}
	return FormatPlanMarkdown(entries), nil
}
