package agent

import (
	"github.com/EvilFreelancer/coddy-agent/internal/rules"
	toolfs "github.com/EvilFreelancer/coddy-agent/internal/tools/fs"
)

// activateScopedRulesForToolCall marks directory-scoped rules (nested AGENTS.md)
// active once a filesystem tool call targets a path inside their directory.
// Activation is sticky for the rest of the session, matching how auto rules
// behave after their first glob match, and the next system prompt rebuild
// (once per ReAct turn) picks the rule up.
func (a *Agent) activateScopedRulesForToolCall(toolName, argsJSON, cwd string) {
	st := sessionStatePtr(a.state)
	if st == nil {
		return
	}
	catalog := st.GetRulesCatalog()
	if len(catalog) == 0 {
		return
	}
	newly := rules.MatchScoped(catalog, toolfs.ToolCallPaths(toolName, argsJSON, cwd))
	if len(newly) == 0 {
		return
	}
	st.SetActiveAutoRules(rules.UnionStable(st.GetActiveAutoRules(), newly))
}
