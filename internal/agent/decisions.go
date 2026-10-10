package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/permission"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// The decisions safety check (#502) stands in for the permission prompt of a
// shell command - run_command or ssh_run_command - that nobody allowed
// explicitly: the NeuralDeep decisions endpoint is asked whether the command
// is safe before the gate decides. A safe command runs without a prompt; an
// unsafe one, or one the check could not judge, is asked about in the ask and
// accept_edits modes and rejected in bypass, where nobody would be asked - the
// refusal travels as the call's tool result, so both the model and the session
// transcript see why the command did not run. The transport lives in
// internal/llm/decisions.go.

const (
	// commandRejectedAsUnsafePrefix opens the tool result of a command the
	// decisions endpoint classified as unsafe. It is deliberately unlike
	// permissionDeniedByUser and its family, which result eviction folds
	// away as if the call never happened: a safety refusal belongs in the
	// history the model keeps reasoning on.
	commandRejectedAsUnsafePrefix = "command rejected as unsafe: "

	// commandNotExecutedPrefix opens the tool result of a command stopped in
	// bypass mode because the check could not judge it: no credential, a
	// request the hub refused, an answer the question cannot have, a command
	// too long for the model to read whole, or an endpoint that did not
	// answer. A safety net that is on must not fail silently open.
	commandNotExecutedPrefix = "command not executed: "
)

// commandVerdict is what the check says about one command.
type commandVerdict struct {
	// safe is true only when the endpoint answered and put the unsafe option
	// below decisions.threshold.
	safe bool
	// unsafe is true when the endpoint classified the command as dangerous;
	// false with safe false means the check could not judge it.
	unsafe bool
	// reason says why the command is not safe, for the prompt and the refusal.
	reason string
}

// refusal is the tool result of a command bypass mode does not run.
func (v commandVerdict) refusal() string {
	if v.unsafe {
		return commandRejectedAsUnsafePrefix + v.reason + "; ask the operator or use a safer alternative"
	}
	return commandNotExecutedPrefix + v.reason
}

// classifyCommand asks the decisions endpoint once about a shell command.
func (a *Agent) classifyCommand(ctx context.Context, subject llm.NeuralDeepDecisionSubject) commandVerdict {
	provider := decisionsProvider(a.cfg)
	model := a.cfg.Decisions.EffectiveModel()
	threshold := a.cfg.Decisions.EffectiveThreshold()
	authPath := config.NeuralDeepAuthPath(a.cfg.Paths.Home, provider.Name)
	decision, err := llm.NeuralDeepDecisionForProvider(ctx, provider, authPath, model, subject)
	if err != nil {
		a.log.Warn("decisions check gave no verdict", "model", model, "error", err)
		return commandVerdict{reason: fmt.Sprintf("the decisions safety check gave no verdict (%s); switch decisions.enable off to run commands without it", err)}
	}
	// The verdict is the probability of the unsafe option, not the endpoint's
	// own pick: the threshold is the operator's dial.
	p := decision.Probability(llm.NeuralDeepDecisionUnsafe)
	if p >= threshold {
		return commandVerdict{unsafe: true, reason: fmt.Sprintf("the decisions model %s classified the command as dangerous (unsafe at %.2f, threshold %.2f)", model, p, threshold)}
	}
	if decision.StateTruncated {
		// The hub cut the state: the verdict covers the head of the command
		// only, and the part the model never read may do the damage.
		longer := ""
		if model != config.DecisionsModelClef {
			longer = fmt.Sprintf(", or set decisions.model to %s, which reads longer commands", config.DecisionsModelClef)
		}
		return commandVerdict{reason: fmt.Sprintf("the command is too long for the decisions model %s to read whole; split it into shorter commands or write files with the file tools%s", model, longer)}
	}
	return commandVerdict{safe: true}
}

// commandAllowlisted reports whether the operator allowed a run_command call
// explicitly: tools.command_allowlist or a session's always-allow grant.
func commandAllowlisted(tc llm.ToolCall, env *tooling.Env, sessionGrants []string) bool {
	if tc.Name != "run_command" {
		return false
	}
	return permission.CommandAllowedWithSession(env, sessionGrants, permission.ExtractRunCommand(tc.InputJSON))
}

// askAlwaysTurn reports whether the running turn keeps every prompt for its
// surface (session.TurnRestriction.AskAlways: a messenger user who is not the
// bot's admin), which the check must not take away.
func askAlwaysTurn(state SessionState) bool {
	st := sessionStatePtr(state)
	if st == nil {
		return false
	}
	r := st.GetTurnRestriction()
	return r != nil && r.AskAlways
}

// decisionsProvider picks the provider row the decisions endpoint is asked
// through: the first neuraldeep row the operator configured, or a synthetic
// one whose credential still resolves (the NEURALDEEP_API_KEY environment
// variable and the stored hub sign-in under the default name).
func decisionsProvider(cfg *config.Config) config.ProviderConfig {
	for _, p := range cfg.Providers {
		if p.Type == "neuraldeep" {
			return p
		}
	}
	return config.ProviderConfig{Name: "neuraldeep", Type: "neuraldeep"}
}

// commandSafetySubject is what the check asks about for one tool call: the
// command and the working directory of a run_command, the command and the
// host of an ssh_run_command. Any other tool, or a call whose command cannot
// be read (the tool itself rejects that one), has nothing to judge.
func commandSafetySubject(toolName, argsJSON string, env *tooling.Env) (llm.NeuralDeepDecisionSubject, bool) {
	switch toolName {
	case "run_command":
		command, cwd := runCommandAndCWD(argsJSON, env)
		return llm.NeuralDeepDecisionSubject{Command: command, CWD: cwd}, command != ""
	case "ssh_run_command":
		command, host := sshCommandAndHost(argsJSON)
		return llm.NeuralDeepDecisionSubject{Command: command, Host: host}, command != ""
	}
	return llm.NeuralDeepDecisionSubject{}, false
}

// sshCommandAndHost reads the command an ssh_run_command call runs and the
// host it reaches, the port named when it is not the default 22.
func sshCommandAndHost(argsJSON string) (string, string) {
	var args struct {
		Host    string `json:"host"`
		Command string `json:"command"`
		Port    int    `json:"port"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", ""
	}
	host := strings.TrimSpace(args.Host)
	if host != "" && args.Port > 0 && args.Port != 22 {
		host = fmt.Sprintf("%s:%d", host, args.Port)
	}
	return strings.TrimSpace(args.Command), host
}

// runCommandAndCWD reads the command and the working directory a run_command
// call carries; the environment's cwd stands in when the call names none.
func runCommandAndCWD(argsJSON string, env *tooling.Env) (string, string) {
	var args struct {
		Command string `json:"command"`
		CWD     string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", ""
	}
	cwd := strings.TrimSpace(args.CWD)
	if cwd == "" && env != nil {
		cwd = env.CWD
	}
	return strings.TrimSpace(args.Command), cwd
}
