package tools

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	toolfs "github.com/EvilFreelancer/coddy-agent/internal/tools/fs"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/shell"
	toolssh "github.com/EvilFreelancer/coddy-agent/internal/tools/ssh"
	"github.com/EvilFreelancer/coddy-agent/internal/tools/todo"
	toolweb "github.com/EvilFreelancer/coddy-agent/internal/tools/web"
)

// Re-export tooling types used by agent, session wiring, and tests.
type (
	Tool     = tooling.Tool
	Env      = tooling.Env
	Registry = tooling.Registry
)

// NewRegistry returns a registry with all built-in tools registered (scheduler tools omitted).
func NewRegistry() *Registry {
	return NewRegistryFor(nil)
}

// NewRegistryFor returns built-in tools plus optional scheduler tools when cfg enables scheduler.
func NewRegistryFor(cfg *config.Config) *Registry {
	return NewRegistryForEnvironment(cfg, platform.CurrentEnvironment())
}

// NewRegistryForEnvironment returns built-ins bound to the detected host environment.
func NewRegistryForEnvironment(cfg *config.Config, environment platform.Environment) *Registry {
	r := tooling.NewRegistry()
	toolfs.RegisterBuiltins(r.Register)
	r.Register(shell.RunCommandToolForShell(environment.Shell))
	r.Register(QuestionTool())
	r.Register(PlanExitTool())
	r.Register(PlanWriteTool())
	r.Register(PlanListTool())
	r.Register(PlanReadTool())
	r.Register(todo.PlanReadTool())
	r.Register(todo.PlanReplaceTool())
	r.Register(todo.PlanArchiveTool())
	r.Register(todo.ItemAddTool())
	r.Register(todo.ItemRemoveTool())
	r.Register(todo.ItemUpdateTool())
	r.Register(todo.ItemMoveTool())
	r.Register(toolweb.WebSearchTool())
	r.Register(toolweb.WebFetchTool())
	r.Register(toolssh.SSHRunCommandTool())
	registerSchedulerTools(r, cfg)
	return r
}

// ResolvePath returns an absolute filesystem path resolved against cwd.
func ResolvePath(path, cwd string) string {
	return toolfs.ResolvePath(path, cwd)
}
