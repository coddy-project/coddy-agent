package agent

import (
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

// operatorCommandsOffered reports whether this turn may offer operator_command:
// the tool is registered (the operator did not turn it off), a person can
// answer on the turn's surface (the sender, or a wrapper that forwards the
// capability, says so), and the turn is not a child's, whose tool set leaves
// the tool out anyway.
func (a *Agent) operatorCommandsOffered() bool {
	if a.subagent != nil {
		return false
	}
	// tools.operator_command.enable: false leaves the tool out of the
	// registry, and nothing may point at it then.
	if a.registry == nil {
		return false
	}
	if _, ok := a.registry.Get(tools.ToolOperatorCommand); !ok {
		return false
	}
	return acp.OperatorCommandsOf(a.server) != nil
}

// operatorCommandHint is the sentence a policy refusal of a shell command
// gains when the operator could run it instead: a refusal of run_command by a
// policy (the unsafe-command check, a toolset), on a turn that offers
// operator_command in its toolset. A hook's block gets none - a hook is the
// operator's own policy - and neither does a "no" the operator gave.
func (a *Agent) operatorCommandHint(toolName string, policy bool) string {
	if !policy || toolName != "run_command" || !a.operatorCommandsOffered() {
		return ""
	}
	if !toolsetAllows(a.activeToolset(), tools.ToolOperatorCommand) {
		return ""
	}
	return " If the operator has to run it, call operator_command."
}
