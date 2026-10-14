package agent

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/subagents"
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

// Toolsets: the toolsets[] presets of the configuration (config.Toolset).
//
// The session is offered the tools of one preset at most: the one it picked
// (/toolset, kept in the session), else the one its
// model names (models[].toolset); "default" picks none. The point is context,
// not permission: a local model with a 49k window that the work only ever asks
// for a dozen tools should not be sent the schemas of the other forty (about
// 12k tokens), while a Codex or GPT model on the same configuration keeps
// every tool.
//
// A preset only narrows. It is applied last, to what the mode, the session's
// switches and a subagent's definition already offer, with the rule a subagent
// definition's own lists follow (subagents.AllowsTool), and it is resolved for
// the model a request is built for (toolOffer), so a switch of the model or of
// the toolset, a config_commit or a model chosen for a spawned child changes
// the set the next request carries. Nothing is forced in: the loop works
// without any tool, so a preset that leaves nothing simply offers none.
//
// The same check runs when a call is about to execute (toolCallRefused), like
// the mode's, so a call replayed from history or invented by the model for a
// tool it was not offered is refused with a clear result before any permission
// prompt.

// toolOffer is what the requests of a running turn are offered their tools
// for: the model of the turn's transport and a copy of the preset that applied
// when the offer was made. A call is judged by the offer of the request that
// produced it, so neither a switch nor a config_commit earlier in the same
// batch changes how the rest of the batch is judged; both take effect from the
// next request.
type toolOffer struct {
	model string
	// toolset is a copy of the preset, nil when nothing restricts the set.
	toolset *config.Toolset
}

// activeToolset is the preset whose lists apply to this agent right now: the
// offer of the request being worked on while a turn runs, else the session's
// own; nil when nothing restricts the set. A system child (the memory
// subagent) is out of reach of it: its tool set is fixed by the runtime that
// launched it, and a preset written for the main loop would strip it.
func (a *Agent) activeToolset() *config.Toolset {
	if a == nil || a.cfg == nil || a.state == nil {
		return nil
	}
	if a.subagent != nil && a.subagent.Kind != "" {
		return nil
	}
	a.offerMu.RLock()
	offer := a.offer
	a.offerMu.RUnlock()
	if offer != nil {
		return offer.toolset
	}
	return a.cfg.ResolveToolset(a.state.EffectiveModelID(a.cfg), a.state.EffectiveToolset())
}

// setOfferModel makes the offer the next requests of the turn are built and
// judged by: the preset the session's choice and model id resolve to in the
// configuration now. "" ends the offer and hands the decision back to the
// session's current settings.
func (a *Agent) setOfferModel(id string) {
	var offer *toolOffer
	if id != "" {
		offer = &toolOffer{model: id}
		if ts := a.cfg.ResolveToolset(id, a.state.EffectiveToolset()); ts != nil {
			offer.toolset = &config.Toolset{
				Name:            ts.Name,
				Tools:           append([]string(nil), ts.Tools...),
				DisallowedTools: append([]string(nil), ts.DisallowedTools...),
			}
		}
	}
	a.offerMu.Lock()
	a.offer = offer
	a.offerMu.Unlock()
}

// toolsetAllows reports whether a preset offers a tool. A nil preset offers
// everything.
func toolsetAllows(ts *config.Toolset, name string) bool {
	if ts == nil {
		return true
	}
	return subagents.AllowsTool(ts.Tools, ts.DisallowedTools, name)
}

// filterToolDefsByToolset keeps the definitions a preset offers. Without one
// it returns defs itself, so a configuration that sets none sends the bytes it
// always did.
func filterToolDefsByToolset(defs []llm.ToolDefinition, ts *config.Toolset) []llm.ToolDefinition {
	if ts == nil {
		return defs
	}
	out := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		if toolsetAllows(ts, d.Name) {
			out = append(out, d)
		}
	}
	return out
}

// sameToolNames reports whether two tool sets name the same tools in the same
// order, the test for whether a switch moved the set a request carries.
func sameToolNames(a, b []llm.ToolDefinition) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
	}
	return true
}

// narrowToolNamesByToolset is filterToolDefsByToolset for a list of names: the
// effective set of a spawned child, narrowed by the preset of the model it
// will run on.
func narrowToolNamesByToolset(names []string, ts *config.Toolset) []string {
	if ts == nil {
		return names
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if toolsetAllows(ts, n) {
			out = append(out, n)
		}
	}
	return out
}

// toolsetHidesTool reports whether the active preset leaves a tool out, for
// the parts of the system prompt that describe one.
func (a *Agent) toolsetHidesTool(name string) bool {
	return !toolsetAllows(a.activeToolset(), name)
}

// toolCallRefusedByToolset reports whether a call must be refused at
// execution time because the active preset leaves the tool out, with the
// refusal text returned as the tool result. Definition filtering already hides
// the tool from the model, but a call can still name it (replayed from history
// recorded under another toolset, or made up), so the preset is checked again
// where the mode is.
func (a *Agent) toolCallRefusedByToolset(name string) (string, bool) {
	ts := a.activeToolset()
	if toolsetAllows(ts, name) {
		return "", false
	}
	return fmt.Sprintf("error: tool %q is not offered: this session runs with the toolset %q. Use one of the tools you were given.",
		name, ts.Name), true
}

// toolCallRefused is the execution-time gate every call passes before a
// permission prompt: the mode's allowlist first, then the active toolset.
// Both the first run of a call and its resume after a permission answer go
// through it.
func (a *Agent) toolCallRefused(mode, name string) (string, bool) {
	if refusal, refused := toolCallRefusedByMode(mode, name); refused {
		return refusal, true
	}
	return a.toolCallRefusedByToolset(name)
}
