package todo

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// PlanArchiveTool marks incomplete items completed, archives markdown to disk, and clears the session plan.
func PlanArchiveTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolNamePlanArchive,
			Description: "Finalize the active todo plan: mark every incomplete item as completed, " +
				"write a markdown snapshot to todos/archive/plan_<unix_seconds>.md when session persistence is enabled, " +
				"then clear the active plan in this session.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Execute: execPlanArchive,
	}
}

func execPlanArchive(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
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
		sendPlanUpdate(env, nil)
		return "plan archive: no active items (nothing to write)", nil
	}

	for i := range entries {
		if entries[i].Status != "completed" {
			entries[i].Status = "completed"
		}
	}
	md := FormatPlanMarkdown(entries)

	var pathNote string
	if strings.TrimSpace(env.SessionDir) != "" && env.WriteArchivedPlanMarkdown != nil {
		path, err := env.WriteArchivedPlanMarkdown(md)
		if err != nil {
			return "", fmt.Errorf("%s: %w", ToolNamePlanArchive, err)
		}
		if path != "" {
			pathNote = path
		}
	}

	empty := []acp.PlanEntry{}
	if env.SetPlan != nil {
		env.SetPlan(empty)
	}
	sendPlanUpdate(env, empty)

	if pathNote != "" {
		return fmt.Sprintf("archived plan (%d items) to %s; active plan cleared", len(entries), pathNote), nil
	}
	return fmt.Sprintf("archived plan state (%d items finalized); active plan cleared (no session dir for file archive)", len(entries)), nil
}
