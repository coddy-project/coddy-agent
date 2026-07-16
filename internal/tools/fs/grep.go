package fs

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// GrepTool is the compatibility name for the portable rg_tool implementation.
func GrepTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "grep",
			Description: "Compatibility alias for rg_tool. Search file contents recursively with POSIX extended regular expressions.",
			InputSchema: RGTool().Definition.InputSchema,
		},
		Execute: executeGrep,
	}
}

func executeGrep(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	return executeRGTool(ctx, argsJSON, env)
}
