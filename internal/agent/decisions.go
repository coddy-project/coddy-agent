package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// The decisions safety check: a shell command no human is about to confirm -
// a run_command that bypass mode, the command allowlist, a session grant or a
// hook's allow lets through without a prompt, a run_command or ssh_run_command
// whose prompt a surface answers by itself (acp.PermissionResult.Automatic),
// an ssh_run_command a hook allowed past its prompt - is asked about on the NeuralDeep decisions endpoint before it runs, and a
// command the endpoint classifies as unsafe is rejected - the refusal travels
// as the call's tool result, so both the model and the session transcript see
// why the command did not run. The transport lives in
// internal/llm/decisions.go.

const (
	// commandRejectedAsUnsafePrefix opens the tool result of a command the
	// decisions endpoint classified as unsafe. It is deliberately unlike
	// permissionDeniedByUser and its family, which result eviction folds
	// away as if the call never happened: a safety refusal belongs in the
	// history the model keeps reasoning on.
	commandRejectedAsUnsafePrefix = "command rejected as unsafe: "

	// commandNotExecutedPrefix opens the tool result of a command stopped
	// because the check itself could not answer for it: no credential, a
	// request the hub refused, a payload that is not an answer, a command
	// too long for the model to read whole, or an endpoint that stayed
	// unreachable or rate-limited past the retry window. A safety net that is
	// on must not fail silently open.
	commandNotExecutedPrefix = "command not executed: "
)

const (
	// decisionsRetryWindowDefault bounds how long one command waits for the
	// decisions endpoint: rate limits and transient failures are retried
	// inside it (honouring Retry-After), and a window that runs out stops
	// the command without running it.
	decisionsRetryWindowDefault = 2 * time.Minute
	// decisionsRetryBackoffDefault is the pause between attempts when the
	// endpoint asked for none.
	decisionsRetryBackoffDefault = 2 * time.Second
)

func (a *Agent) effectiveDecisionsRetryWindow() time.Duration {
	if a.decisionsRetryWindow > 0 {
		return a.decisionsRetryWindow
	}
	return decisionsRetryWindowDefault
}

func (a *Agent) effectiveDecisionsRetryBackoff() time.Duration {
	if a.decisionsRetryBackoff > 0 {
		return a.decisionsRetryBackoff
	}
	return decisionsRetryBackoffDefault
}

// checkCommandSafety runs the check for one tool call: "" when the call is
// not a shell command (nothing to judge) or may run, the refusal otherwise.
func (a *Agent) checkCommandSafety(ctx context.Context, tc llm.ToolCall, env *tooling.Env) string {
	subject, ok := commandSafetySubject(tc.Name, tc.InputJSON, env)
	if !ok {
		return ""
	}
	return a.gateCommandSafety(ctx, subject)
}

// gateCommandSafety asks the decisions endpoint about a shell command that is
// about to run with no human in the loop. It returns "" when the command may
// run, and the refusal text (already fit for the tool result and the
// transcript) when it must not.
func (a *Agent) gateCommandSafety(ctx context.Context, subject llm.NeuralDeepDecisionSubject) string {
	provider := decisionsProvider(a.cfg)
	model := a.cfg.Decisions.EffectiveModel()
	threshold := a.cfg.Decisions.EffectiveThreshold()
	authPath := config.NeuralDeepAuthPath(a.cfg.Paths.Home, provider.Name)
	deadline := time.Now().Add(a.effectiveDecisionsRetryWindow())
	backoff := a.effectiveDecisionsRetryBackoff()
	for {
		decision, err := llm.NeuralDeepDecisionForProvider(ctx, provider, authPath, model, subject)
		if err == nil {
			// The verdict is the probability of the unsafe option, not the
			// endpoint's own pick: the threshold is the operator's dial, so a
			// model that chose unsafe below it still runs the command.
			p := decision.Probability(llm.NeuralDeepDecisionUnsafe)
			if p < threshold && decision.StateTruncated {
				// The hub cut the state: the verdict covers the head of the
				// command, and the part the model never read may be the one
				// that does the damage. An unsafe head is still rejected below.
				longer := ""
				if model != config.DecisionsModelClef {
					longer = fmt.Sprintf(", or set decisions.model to %s, which reads longer commands", config.DecisionsModelClef)
				}
				return fmt.Sprintf("%sthe command is too long for the decisions model %s to read whole, so its verdict would not cover all of it. Split it into shorter commands or write files with the file tools instead of a heredoc%s",
					commandNotExecutedPrefix, model, longer)
			}
			if p < threshold {
				if decision.Choice == llm.NeuralDeepDecisionUnsafe {
					a.log.Warn("decisions check chose unsafe below the configured threshold; running the command", "p_unsafe", p, "threshold", threshold)
				}
				return ""
			}
			return fmt.Sprintf("%sthe decisions model %s put the unsafe option at %.2f, at or above the threshold %.2f; ask the operator or use a safer alternative",
				commandRejectedAsUnsafePrefix, model, p, threshold)
		}
		de, ok := llm.IsNeuralDeepDecisionError(err)
		if !ok {
			de = &llm.NeuralDeepDecisionError{Kind: llm.NeuralDeepDecisionUnavailable, Detail: err.Error()}
		}
		// Retrying cannot change these answers: the check is on, so the
		// command waits for the operator to fix the cause, not for the
		// endpoint.
		switch de.Kind {
		case llm.NeuralDeepDecisionUnauthorized, llm.NeuralDeepDecisionForbidden:
			// The row's own variable and sign-in: a row named hub reads
			// HUB_API_KEY, never NEURALDEEP_API_KEY.
			return fmt.Sprintf("%sthe decisions safety check is not available: %s. Provide a NeuralDeep credential (api_key or api_key_command on the provider row %s, the %s environment variable, or coddy providers login %s) or switch decisions.enable off",
				commandNotExecutedPrefix, de.Error(), provider.Name, config.ProviderAPIKeyEnvVarName(provider.Name), provider.Name)
		case llm.NeuralDeepDecisionRefused:
			return fmt.Sprintf("%sthe decisions safety check is not available: %s. Check decisions.model and the NeuralDeep account behind the key (its balance included) or switch decisions.enable off",
				commandNotExecutedPrefix, de.Error())
		case llm.NeuralDeepDecisionInvalid:
			return fmt.Sprintf("%sthe decisions endpoint returned no usable safety verdict: %s. Check what answers on the provider row's api_base and proxy, try again later, or switch decisions.enable off",
				commandNotExecutedPrefix, de.Error())
		}
		wait := de.RetryAfter
		if wait <= 0 {
			wait = backoff
		}
		if time.Now().Add(wait).After(deadline) {
			return fmt.Sprintf("%sthe decisions safety check did not answer within its retry window (last error: %s); the command was not run. Try again later or switch decisions.enable off",
				commandNotExecutedPrefix, de.Error())
		}
		select {
		case <-ctx.Done():
			return commandNotExecutedPrefix + "the turn ended while waiting for the decisions safety check"
		case <-time.After(wait):
		}
	}
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
