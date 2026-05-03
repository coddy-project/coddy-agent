package tooling

import (
	"context"
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// Registry holds all registered tools.
type Registry struct {
	tools map[string]*Tool
}

// NewRegistry returns an empty registry. Call Register for each built-in or MCP tool wrapper.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]*Tool)}
}

// Register adds a built-in tool under its definition name.
func (r *Registry) Register(t *Tool) {
	r.tools[t.Definition.Name] = t
}

// RegisterMCPTool adds a tool from an MCP server with namespaced name.
func (r *Registry) RegisterMCPTool(serverName string, t *Tool) {
	key := serverName + "__" + t.Definition.Name
	namespaced := *t
	namespaced.Definition.Name = key
	r.tools[key] = &namespaced
}

// ToolsForMode returns tool definitions available in the given mode.
func (r *Registry) ToolsForMode(mode string) []llm.ToolDefinition {
	var defs []llm.ToolDefinition
	for _, t := range r.tools {
		if mode == "agent" && t.PlanOnly {
			continue
		}
		if mode == "plan" && !t.AllowedInPlanMode {
			continue
		}
		defs = append(defs, t.Definition)
	}
	return defs
}

// Execute runs the named tool with the given JSON arguments.
func (r *Registry) Execute(ctx context.Context, name, argsJSON string, env *Env) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.Execute(ctx, argsJSON, env)
}

// Get returns the tool by name.
func (r *Registry) Get(name string) (*Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}
