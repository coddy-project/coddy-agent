package agent

import (
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// ToolSet is an allowlist of tool names passed to the LLM. Empty or nil means unrestricted
// (all definitions from the registry, and MCP tools when the agent wires them in).
type ToolSet []string

// planToolNames is the fixed allowlist for plan mode (read-only registry builtins plus shell).
// MCP server tools are appended separately in react.go (same as agent mode).
var planToolNames = []string{
	"read",
	"keep_result",
	"glob",
	"grep",
	"print_tree",
	"websearch",
	"webfetch",
	"run_command",
	// Background execution is available in plan mode for the same reason
	// run_command is: a planner investigating a repo should not have to sit
	// through a slow read-only command, and the pool tools only observe and
	// terminate work the planner started itself.
	"background_list",
	"background_output",
	"background_wait",
	"background_stop",
	"question",
	"config_get",
	// Read-only view of staged config commands; staging and committing stay
	// agent-mode-only.
	"config_changes",
	"plan_write",
	"plan_list",
	"plan_read",
	// Read-only: lets the planner pull a catalogued skill's instructions when
	// skills.auto_discovery is on (the tool is only registered when enabled).
	"load_skill",
	// A planner fans out investigation the same way Claude Code's Explore
	// subagent does; the child of a plan-mode parent is forced into plan mode.
	"spawn_agent",
}

// ToolSetForMode returns the tool allowlist for the session mode. Agent mode is unrestricted.
func ToolSetForMode(mode string) ToolSet {
	if mode == "plan" {
		out := make(ToolSet, len(planToolNames))
		copy(out, planToolNames)
		return out
	}
	return nil
}

// Unrestricted reports whether the set imposes no name filter.
func (s ToolSet) Unrestricted() bool {
	return len(s) == 0
}

// Allows reports whether name is permitted by this set. Unrestricted sets allow every name.
func (s ToolSet) Allows(name string) bool {
	if s.Unrestricted() {
		return true
	}
	for _, n := range s {
		if n == name {
			return true
		}
	}
	return false
}

// FilterToolDefinitions keeps definitions whose names are allowed by set.
func FilterToolDefinitions(defs []llm.ToolDefinition, set ToolSet) []llm.ToolDefinition {
	if set.Unrestricted() {
		return defs
	}
	var out []llm.ToolDefinition
	for i := range defs {
		if set.Allows(defs[i].Name) {
			out = append(out, defs[i])
		}
	}
	return out
}
