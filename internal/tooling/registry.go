package tooling

import (
	"context"
	"fmt"
	"sort"

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

// AllToolDefinitions returns every registered tool definition in stable name order.
func (r *Registry) AllToolDefinitions() []llm.ToolDefinition {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]llm.ToolDefinition, 0, len(names))
	for _, name := range names {
		out = append(out, r.tools[name].Definition)
	}
	return out
}

// Execute runs the named tool with the given JSON arguments. Its result or error
// is capped to the per-tool output ceiling carried by env (no-op when env carries
// no limits), so one call cannot flood the LLM context.
func (r *Registry) Execute(ctx context.Context, name, argsJSON string, env *Env) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	out, err := t.Execute(ctx, argsJSON, env)
	if err == nil {
		out = ApplyOutputLimit(out, name, env)
	} else {
		err = ApplyOutputLimitError(err, name, env)
	}
	return out, err
}

// Get returns the tool by name.
func (r *Registry) Get(name string) (*Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}
