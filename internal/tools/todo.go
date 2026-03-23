package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// validTodoStatuses lists the allowed values for PlanEntry.Status.
var validTodoStatuses = map[string]bool{
	"pending":     true,
	"in_progress": true,
	"completed":   true,
	"failed":      true,
	"cancelled":   true,
}

func createTodoListTool() *Tool {
	return &Tool{
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

func updateTodoItemTool() *Tool {
	return &Tool{
		Definition: llm.ToolDefinition{
			Name: "update_todo_item",
			Description: "Update the status of a todo list item by its zero-based index. " +
				"Use this to mark steps as in_progress, completed, failed, or cancelled as you work through the plan.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"index": map[string]interface{}{
						"type":        "integer",
						"description": "Zero-based index of the item to update.",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"pending", "in_progress", "completed", "failed", "cancelled"},
						"description": "New status for the item.",
					},
				},
				"required": []string{"index", "status"},
			},
		},
		AllowedInPlanMode: true,
		Execute:           execUpdateTodoItem,
	}
}

func execCreateTodoList(_ context.Context, argsJSON string, env *Env) (string, error) {
	var args struct {
		Items string `json:"items"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if strings.TrimSpace(args.Items) == "" {
		return "", fmt.Errorf("items must not be empty")
	}

	entries := parseTodoMarkdown(args.Items)
	if len(entries) == 0 {
		return "", fmt.Errorf("no valid todo items found in the provided markdown")
	}

	if env.SetPlan != nil {
		env.SetPlan(entries)
	}
	sendPlanUpdate(env, entries)

	return fmt.Sprintf("created todo list with %d items", len(entries)), nil
}

func execUpdateTodoItem(_ context.Context, argsJSON string, env *Env) (string, error) {
	var args struct {
		Index  int    `json:"index"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	if !validTodoStatuses[args.Status] {
		return "", fmt.Errorf("invalid status %q: must be one of pending, in_progress, completed, failed, cancelled", args.Status)
	}

	var entries []acp.PlanEntry
	if env.GetPlan != nil {
		entries = env.GetPlan()
	}

	if args.Index < 0 || args.Index >= len(entries) {
		return "", fmt.Errorf("index %d is out of range (plan has %d items)", args.Index, len(entries))
	}

	entries[args.Index].Status = args.Status

	if env.SetPlan != nil {
		env.SetPlan(entries)
	}
	sendPlanUpdate(env, entries)

	return fmt.Sprintf("updated item %d to status %q", args.Index, args.Status), nil
}

// parseTodoMarkdown parses a markdown checklist string into PlanEntry values.
// Supports "- [ ] text", "- [x] text", "* [ ] text", "* [x] text" formats.
// Also handles literal \n escape sequences for inline markdown strings.
func parseTodoMarkdown(markdown string) []acp.PlanEntry {
	var entries []acp.PlanEntry
	normalized := strings.ReplaceAll(markdown, `\n`, "\n")
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		checked, text, ok := parseCheckboxLine(line)
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		status := "pending"
		if checked {
			status = "completed"
		}
		entries = append(entries, acp.PlanEntry{
			Content: text,
			Status:  status,
		})
	}
	return entries
}

// parseCheckboxLine extracts checked bool and text from a markdown checkbox line.
// Returns (checked, text, ok).
func parseCheckboxLine(line string) (checked bool, text string, ok bool) {
	for _, prefix := range []string{"- ", "* "} {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		switch {
		case strings.HasPrefix(rest, "[ ] "):
			return false, rest[4:], true
		case strings.HasPrefix(rest, "[x] "), strings.HasPrefix(rest, "[X] "):
			return true, rest[4:], true
		case strings.HasPrefix(rest, "[ ]"), strings.EqualFold(rest, "[ ]"):
			return false, strings.TrimSpace(rest[3:]), true
		case strings.HasPrefix(rest, "[x]"), strings.HasPrefix(rest, "[X]"):
			return true, strings.TrimSpace(rest[3:]), true
		default:
			// Plain list item without checkbox.
			return false, rest, true
		}
	}
	return false, "", false
}

// sendPlanUpdate sends a PlanUpdate via env.Sender if available.
func sendPlanUpdate(env *Env, entries []acp.PlanEntry) {
	if env.Sender == nil {
		return
	}
	_ = env.Sender.SendSessionUpdate(env.SessionID, acp.PlanUpdate{
		SessionUpdate: acp.UpdateTypePlan,
		Entries:       entries,
	})
}
