package agent

import (
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	toolfs "github.com/EvilFreelancer/coddy-agent/internal/tools/fs"
)

// activateScopedRulesForToolCall marks path-gated rules active once a
// filesystem tool call targets a path they cover: a glob rule (Cursor globs,
// Claude Code paths) when the path matches one of its patterns, and the
// nested AGENTS.md files on the chain of folders down to that path, read at
// this moment and never before - a session looks at a folder only when a
// tool enters it. Activation is sticky for the rest of the session, matching
// how auto rules behave after a file:// attachment matched them, and the next
// system prompt rebuild (once per ReAct turn) picks the rule up.
func (a *Agent) activateScopedRulesForToolCall(toolName, argsJSON, cwd string) {
	st := sessionStatePtr(a.state)
	if st == nil {
		return
	}
	paths := toolfs.ToolCallPaths(toolName, argsJSON, cwd)
	if len(paths) == 0 {
		return
	}
	active := st.GetActiveAutoRules()
	newly := rules.MatchScoped(st.GetRulesCatalog(), paths)
	if a.agentsOnDemand() {
		newly = append(newly, rules.AgentsForPaths(cwd, paths, active)...)
	}
	if len(newly) == 0 {
		return
	}
	st.SetActiveAutoRules(rules.UnionStable(active, newly))
}

// agentsOnDemand reports whether nested AGENTS.md files are read for the
// folders a tool enters: rules discovery is on and rules.systems does not
// exclude the agents system.
func (a *Agent) agentsOnDemand() bool {
	if a == nil || a.cfg == nil || !a.cfg.Rules.AutoDiscoverEnabled() {
		return false
	}
	return rules.AgentsOnDemand(rules.ParseSystems(a.cfg.Rules.Systems))
}
