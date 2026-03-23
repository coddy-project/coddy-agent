// Package tools implements the built-in tool registry and execution.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

	// Execute runs the tool with the given JSON arguments.
	Execute func(ctx context.Context, argsJSON string, env *Env) (string, error)
}

// Env provides environmental context to tool execution.
type Env struct {
	// CWD is the session working directory.
	CWD string

	// RestrictToCWD prevents operations outside the working directory.
	RestrictToCWD bool

	// RequirePermissionForCommands enables permission prompts for commands.
	RequirePermissionForCommands bool

	// RequirePermissionForWrites enables permission prompts for writes.
	RequirePermissionForWrites bool

	// CommandAllowlist contains command prefixes/exact commands that never
	// require permission. Checked via CommandAllowed().
	CommandAllowlist []string
}

// CommandAllowed returns true if the given shell command matches an entry
// in the allowlist, meaning it can run without user permission.
//
// Matching rules (case-sensitive):
//   - Exact match: "make" matches "make" but not "make build"
//   - Prefix match: "go test" matches "go test ./..." and "go test -v ."
//
// A trailing space is implicitly added to prefix entries to prevent
// "go" from matching "golang-migrate".
func (e *Env) CommandAllowed(command string) bool {
	cmd := strings.TrimSpace(command)
	for _, allowed := range e.CommandAllowlist {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		// Wildcard: allow any command.
		if allowed == "*" {
			return true
		}
		// Exact match.
		if cmd == allowed {
			return true
		}
		// Prefix match: the command must start with "allowed " (with a space).
		if strings.HasPrefix(cmd, allowed+" ") {
			return true
		}
	}
	return false
}

// Registry holds all registered tools.
type Registry struct {
	tools map[string]*Tool
}

// NewRegistry creates a registry populated with all built-in tools.
func NewRegistry() *Registry {
	r := &Registry{tools: make(map[string]*Tool)}
	r.register(readFileTool())
	r.register(writeFileTool())
	r.register(listDirTool())
	r.register(searchFilesTool())
	r.register(runCommandTool())
	r.register(applyDiffTool())
	r.register(switchToAgentModeTool())
	return r
}

// register adds a tool to the registry.
func (r *Registry) register(t *Tool) {
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

// parseArgs unmarshals JSON args into a typed struct.
func parseArgs[T any](argsJSON string) (T, error) {
	var v T
	if err := json.Unmarshal([]byte(argsJSON), &v); err != nil {
		return v, fmt.Errorf("parse args: %w", err)
	}
	return v, nil
}
