package tooling

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// Tool is a callable tool that the agent can invoke.
type Tool struct {
	// Definition is the schema exposed to the LLM.
	Definition llm.ToolDefinition

	// RequiresPermission indicates the tool needs user approval.
	RequiresPermission bool

	// AllowedInPlanMode indicates the tool is available in plan mode.
	AllowedInPlanMode bool

	// PlanOnly restricts the tool to plan mode tool lists only (omit in agent mode).
	PlanOnly bool

	// Execute runs the tool with the given JSON arguments.
	Execute func(ctx context.Context, argsJSON string, env *Env) (string, error)
}
