package agent

import (
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// Keep the provider error reachable for status reporting and diagnostics,
// without printing its raw body, request URL or headers to the user.
type toolSchemaLimitError struct {
	message string
	cause   error
}

func (e *toolSchemaLimitError) Error() string { return e.message }
func (e *toolSchemaLimitError) Unwrap() error { return e.cause }

func (a *Agent) toolSchemaLimitError(cause error, tools []llm.ToolDefinition) error {
	detail := llm.ToolSchemaLimitDetailOf(cause, tools)
	message := "tool schemas rejected: the provider found the offered tool parameter schemas too large or too complex. Tool schemas accompany even a simple message, before any tool runs. Offer fewer tools or simplify the responsible MCP server's parameter schemas"
	if detail.Tool == "" {
		message += "; the responsible tool/MCP server is unknown"
	} else {
		message += fmt.Sprintf("; the provider identified tool %q", detail.Tool)
		// A double underscore is a server separator only when a connected
		// client's actual tool declaration produces this offered name.
		server := ""
		matches := 0
		for _, client := range a.state.GetMCPClients() {
			for _, tool := range client.Tools() {
				if tool.ToLLMToolDefinition(client.Name()).Name == detail.Tool {
					server = client.Name()
					matches++
				}
			}
		}
		if matches == 1 {
			message += fmt.Sprintf(" from MCP server %q", server)
		} else {
			message += "; its MCP server is unknown"
		}
	}
	if detail.Limit == "" {
		message += "; the exact schema limit is unknown to Coddy (limits vary by provider)"
	} else {
		message += "; the provider reported " + detail.Limit
	}
	return &toolSchemaLimitError{message: message + ".", cause: cause}
}
