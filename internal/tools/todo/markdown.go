package todo

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// ParseTodoMarkdown parses a markdown checklist string into PlanEntry values.
func ParseTodoMarkdown(markdown string) []acp.PlanEntry {
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
			return false, rest, true
		}
	}
	return false, "", false
}

// FormatTodoMarkdown renders plan entries as a markdown checklist.
func FormatTodoMarkdown(entries []acp.PlanEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		mark := "[ ]"
		if e.Status == "completed" {
			mark = "[x]"
		}
		b.WriteString("- ")
		b.WriteString(mark)
		b.WriteString(" ")
		b.WriteString(e.Content)
		if e.Status != "pending" && e.Status != "completed" {
			b.WriteString(" `")
			b.WriteString(e.Status)
			b.WriteString("`")
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
