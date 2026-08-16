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
	if cfg == nil || cfg.Tools.Background.ResolvedEnabled() {
		r.Register(shell.BackgroundListTool())
		r.Register(shell.BackgroundOutputTool())
		r.Register(shell.BackgroundWaitTool())
		r.Register(shell.BackgroundStopTool())
		r.Register(shell.BackgroundReapTool())
	}
	r.Register(QuestionTool())
	r.Register(ConfigGetTool())
	r.Register(ConfigSetTool())
	r.Register(ConfigChangesTool())
	r.Register(ConfigCommitTool())
	r.Register(ConfigRevertTool())
	r.Register(ConfigRollbackTool())
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
	// Model-driven skill auto-discovery: offered unless explicitly disabled.
	if cfg == nil || cfg.Skills.AutoDiscoveryEnabled() {
		r.Register(LoadSkillTool())
	}
	registerSchedulerTools(r, cfg)
	return r
}

// ResolvePath returns an absolute filesystem path resolved against cwd.
func ResolvePath(path, cwd string) string {
	return toolfs.ResolvePath(path, cwd)
}

// ApplyOutputLimit caps a tool result to the per-tool output line ceiling carried
// by env. Re-exported so the agent can apply it to MCP calls (which bypass the
// registry). No-op when env carries no limits.
func ApplyOutputLimit(out, tool string, env *Env) string {
	return tooling.ApplyOutputLimit(out, tool, env)
}

// ApplyOutputLimitError applies the per-tool output ceiling to an error while
// preserving its original cause for errors.Is/errors.As.
func ApplyOutputLimitError(err error, tool string, env *Env) error {
	return tooling.ApplyOutputLimitError(err, tool, env)
}
