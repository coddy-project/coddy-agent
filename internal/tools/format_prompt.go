package tools

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// FormatDefinitionsForPrompt builds a concise markdown list of tool names and descriptions
// for injection into the system prompt template. Full JSON schemas are still passed to
// the LLM via the provider tool list.
func FormatDefinitionsForPrompt(defs []llm.ToolDefinition) string {
	if len(defs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, d := range defs {
		b.WriteString("- `")
		b.WriteString(d.Name)
		b.WriteString("`: ")
		if desc := strings.TrimSpace(d.Description); desc != "" {
			b.WriteString(desc)
		} else {
			b.WriteString("(no description)")
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
