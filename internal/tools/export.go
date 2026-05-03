package tools

import (
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	toolfs "github.com/EvilFreelancer/coddy-agent/internal/tools/fs"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/shell"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
)

// Re-export tooling types used by agent, session wiring, and tests.
type (
	Tool     = tooling.Tool
	Env      = tooling.Env
	Registry = tooling.Registry
)

// NewRegistry returns a registry with all built-in tools registered.
func NewRegistry() *Registry {
	r := tooling.NewRegistry()
	toolfs.RegisterBuiltins(r.Register)
	r.Register(shell.RunCommandTool())
	r.Register(todo.CreateListTool())
	r.Register(todo.UpdateItemTool())
	r.Register(todo.GetListTool())
	r.Register(todo.DeleteItemTool())
	r.Register(todo.DoneTodoItemTool())
	r.Register(todo.UndoneTodoItemTool())
	r.Register(todo.CleanTodoListTool())
	return r
}

// ResolvePath returns an absolute filesystem path resolved against cwd.
func ResolvePath(path, cwd string) string {
	return toolfs.ResolvePath(path, cwd)
}

// PathEscapesCWD reports whether an absolute-resolved path leaves the cwd tree.
func PathEscapesCWD(path, cwd string) bool {
	return toolfs.PathEscapesCWD(path, cwd)
}

// ToolPathsEscapeCWD detects path fields in built-in JSON args that escape cwd.
func ToolPathsEscapeCWD(toolName, argsJSON, cwd string) bool {
	return toolfs.ToolPathsEscapeCWD(toolName, argsJSON, cwd)
}
