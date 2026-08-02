package todo

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// PlanReplaceTool replaces the full todo plan from a markdown checklist.
func PlanReplaceTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolNamePlanReplace,
			Description: "Replace the entire todo plan from a markdown checklist. " +
				"Use for bulk edits, imports, or reordering. " +
				"Each line becomes a plan entry. Items marked with [x] are completed. " +
				"Cannot replace while any item is still incomplete - run coddy_todo_plan_archive first. " +
				"Markdown must not be empty.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"markdown": map[string]interface{}{
						"type":        "string",
						"description": `Markdown checklist, one item per line. Formats: "- [ ] task", "- [x] done", "* [ ] task".`,
					},
				},
				"required": []string{"markdown"},
			},
		},
		Execute: execPlanReplace,
	}
}

type planReplaceArgs struct {
	Markdown string `json:"markdown"`
}

func execPlanReplace(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[planReplaceArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Markdown) == "" {
		return "", fmt.Errorf("%s: markdown must not be empty (use %s to archive and clear the active plan)", ToolNamePlanReplace, ToolNamePlanArchive)
	}

	entries := ParsePlanMarkdown(args.Markdown)
	if len(entries) == 0 {
		return "", fmt.Errorf("%s: no valid checklist items in markdown", ToolNamePlanReplace)
	}

	var prev []acp.PlanEntry
	if env.GetPlan != nil {
		prev = env.GetPlan()
	}
	if PlanHasIncompleteItems(prev) {
		return "", fmt.Errorf("%s: complete or archive the current plan before replacing it (incomplete items remain)", ToolNamePlanReplace)
	}
	if len(prev) > 0 && env.ArchiveActiveMarkdown != nil {
		_ = env.ArchiveActiveMarkdown()
	}

	if env.SetPlan != nil {
		env.SetPlan(entries)
	}
	sendPlanUpdate(env, entries)

	return fmt.Sprintf("replaced todo plan with %d items", len(entries)), nil
}
