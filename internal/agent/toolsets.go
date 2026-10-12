package agent

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// ToolSet is an allowlist of tool names passed to the LLM. Empty or nil means unrestricted
// (all definitions from the registry, and MCP tools when the agent wires them in).
type ToolSet []string

// planToolNames is the fixed allowlist for plan mode (read-only registry builtins plus shell).
// The tools of the connected MCP servers join it (modePolicies), as in agent mode.
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
	// Folding the session's own replay window writes nothing outside it, and a
	// long investigation is exactly the session that fills a context window.
	"compact_context",
	// Filing the session writes nothing but the session's own title and tags,
	// and a planning session is one that earns a name as it goes.
	"session_describe",
	// Changing the model or the reasoning level, when the user asks for it,
	// changes nothing outside the session.
	"switch_model",
	// Coddy's own documentation, read out of the binary.
	"coddy_docs_search",
	"coddy_docs_read",
}

// askToolNames is the fixed allowlist for ask mode: repository reads and web
// research only. No shell, no plan tools, no config tools, and no MCP tools
// (modePolicies offers none in this mode).
var askToolNames = []string{
	"read",
	"keep_result",
	"glob",
	"grep",
	"print_tree",
	"websearch",
	"webfetch",
	"question",
	// Read-only: lets the assistant pull a catalogued skill's instructions when
	// skills.auto_discovery is on (the tool is only registered when enabled).
	"load_skill",
	// The model and the reasoning level are the session's own settings.
	"switch_model",
	// Coddy's own documentation, read out of the binary: "how do I set up
	// the Telegram bot" is exactly the question ask mode is for.
	"coddy_docs_search",
	"coddy_docs_read",
}

// modePolicy is what a restricted operating mode lets the model use, kept in
// one place: the built-in tools it offers, whether the tools of the connected
// MCP servers join them, and the notice a call outside them is refused with
// when it runs. Whatever offers, checks or narrows tools by mode reads it -
// the definitions of a turn (currentToolDefinitions), the execution-time check
// (toolCallRefusedByMode), the tool set of a child or a scheduled run and
// whether that run dials MCP servers (modeToolNames, modeOffersMCP) - so the
// four cannot disagree about a mode. Agent mode has no policy: it is
// unrestricted.
type modePolicy struct {
	tools ToolSet
	mcp   bool
	// refusal is a format taking the tool name (%q).
	refusal string
}

var modePolicies = map[string]modePolicy{
	"plan": {
		tools:   planToolNames,
		mcp:     true,
		refusal: "error: tool %q is not available in Plan mode because it is outside the plan tool set; the user can switch to Agent mode to use it",
	},
	"ask": {
		tools:   askToolNames,
		refusal: "error: tool %q is not available in Ask mode because it is not read-only",
	},
}

// ToolSetForMode returns the built-in tool allowlist for the session mode.
// Agent mode is unrestricted. MCP tools are not in it: whether a mode offers
// them is modeOffersMCP.
func ToolSetForMode(mode string) ToolSet {
	p, ok := modePolicies[mode]
	if !ok {
		return nil
	}
	out := make(ToolSet, len(p.tools))
	copy(out, p.tools)
	return out
}

// modeOffersMCP reports whether the tools of the connected MCP servers are
// offered in mode: in agent and plan mode, not in ask mode.
func modeOffersMCP(mode string) bool {
	p, ok := modePolicies[mode]
	return !ok || p.mcp
}

// isMCPToolName reports whether name is namespaced like the tool of an MCP
// server (server__tool). executeToolCall routes every such name to callMCPTool
// and never to the registry, and no built-in tool is named that way (a test
// pins it). callMCPTool decides about such a name and fails closed for one
// that is not a connected server's tool: the per-tool filter first, then the
// lookup of the server ("MCP server not found"), then the server itself.
func isMCPToolName(name string) bool {
	return strings.Contains(name, "__")
}

// modeAdmits reports whether mode lets a call to name run: a built-in of its
// allowlist, or any MCP name where the mode offers MCP tools.
func modeAdmits(mode, name string) bool {
	p, ok := modePolicies[mode]
	if !ok {
		return true
	}
	return p.tools.Allows(name) || (p.mcp && isMCPToolName(name))
}

// modeToolNames is the mode's set for a child or a scheduled run to be
// narrowed by (subagents.EffectiveTools): the names of names that mode admits,
// MCP tools included where it offers them, or nil when the mode is
// unrestricted. Narrowing by the bare allowlist would drop every MCP tool of a
// plan-mode run.
func modeToolNames(mode string, names []string) []string {
	if _, ok := modePolicies[mode]; !ok {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if modeAdmits(mode, n) {
			out = append(out, n)
		}
	}
	return out
}

// toolCallRefusedByMode reports whether a tool call must be refused at execution
// time in the given mode, with the refusal text returned as the tool result.
// Definition filtering already hides restricted tools from the LLM, but a model
// can still replay a call from earlier history (recorded in another mode) or a
// provider can ignore the offered list, so ask and plan mode re-check their
// allowlists here. The same check guards the permission-resume path.
//
// Ask offers no MCP tools, so MCP tool names (server__tool) are refused there.
// Plan offers the tools of the connected MCP servers next to its allowlist, so
// an MCP name passes the mode check and callMCPTool decides about it as in
// agent mode: it rejects a server that is not connected and a tool the
// per-tool switches disabled. Agent mode is unrestricted.
func toolCallRefusedByMode(mode, name string) (string, bool) {
	if modeAdmits(mode, name) {
		return "", false
	}
	return fmt.Sprintf(modePolicies[mode].refusal, name), true
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
