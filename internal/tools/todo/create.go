package todo

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// CreateListTool creates or replaces the todo list from markdown.
func CreateListTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "create_todo_list",
			Description: "Create or replace the current todo/plan list from a markdown checklist. " +
				"Use this tool when the user asks to plan a complex task or when you want to track multi-step work. " +
				"Each list item becomes a plan entry. Items marked with [x] are treated as completed.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"items": map[string]interface{}{
						"type":        "string",
						"description": `Markdown checklist, one item per line. Supported formats: "- [ ] task", "- [x] done task", "* [ ] task".`,
					},
				},
				"required": []string{"items"},
			},
		},
		AllowedInPlanMode: true,
		Execute:           execCreateTodoList,
	}
}

type createTodoArgs struct {
	Items string `json:"items"`
}

func execCreateTodoList(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[createTodoArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Items) == "" {
		return "", fmt.Errorf("items must not be empty")
	}

	entries := ParseTodoMarkdown(args.Items)
	if len(entries) == 0 {
		return "", fmt.Errorf("no valid todo items found in the provided markdown")
	}

	if env.SetPlan != nil {
		env.SetPlan(entries)
	}
	sendPlanUpdate(env, entries)

	return fmt.Sprintf("created todo list with %d items", len(entries)), nil
}
