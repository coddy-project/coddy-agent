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

// The decisions safety check: a run_command call that no permission prompt
// covers (bypass mode, the command allowlist, a session grant or a hook's
// allow) is asked about on the NeuralDeep decisions endpoint before it runs,
// and a command the endpoint classifies as unsafe is rejected - the refusal
// travels as the call's tool result, so both the model and the session
// transcript see why the command did not run. The transport lives in
// internal/llm/decisions.go.

const (
	// commandRejectedAsUnsafePrefix opens the tool result of a command the
	// decisions endpoint classified as unsafe. It is deliberately unlike
	// permissionDeniedByUser and its family, which result eviction folds
	// away as if the call never happened: a safety refusal belongs in the
	// history the model keeps reasoning on.
	commandRejectedAsUnsafePrefix = "command rejected as unsafe: "

	// commandNotExecutedPrefix opens the tool result of a command stopped
	// because the check itself could not answer: no credential, a payload
	// that is not an answer, or an endpoint that stayed unreachable or
	// rate-limited past the retry window. A safety net that is on must not
	// fail silently open.
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

// gateCommandSafety asks the decisions endpoint about a run_command call that
// is about to run with no human in the loop. It returns "" when the command
// may run, and the refusal text (already fit for the tool result and the
// transcript) when it must not.
func (a *Agent) gateCommandSafety(ctx context.Context, argsJSON string, env *tooling.Env) string {
	command, cwd := runCommandAndCWD(argsJSON, env)
	if command == "" {
		// Nothing to judge; the tool itself will reject the broken call.
		return ""
	}
	provider := decisionsProvider(a.cfg)
	model := a.cfg.Decisions.EffectiveModel()
	threshold := a.cfg.Decisions.EffectiveThreshold()
	authPath := config.NeuralDeepAuthPath(a.cfg.Paths.Home, provider.Name)
	deadline := time.Now().Add(a.effectiveDecisionsRetryWindow())
	backoff := a.effectiveDecisionsRetryBackoff()
	for {
		decision, err := llm.NeuralDeepDecisionForProvider(ctx, provider, authPath, model, command, cwd)
		if err == nil {
			// The verdict is the probability of the unsafe option, not the
			// endpoint's own pick: the threshold is the operator's dial, so a
			// model that chose unsafe below it still runs the command.
			p := decision.Probability(llm.NeuralDeepDecisionUnsafe)
			if p < threshold {
				if decision.Choice == llm.NeuralDeepDecisionUnsafe {
					a.log.Warn("decisions check chose unsafe below the configured threshold; running the command", "p_unsafe", p, "threshold", threshold)
				} else if decision.Choice != llm.NeuralDeepDecisionSafe {
					a.log.Warn("decisions check answered an option the safety question does not offer", "option", decision.Choice)
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
		switch de.Kind {
		case llm.NeuralDeepDecisionUnauthorized, llm.NeuralDeepDecisionForbidden, llm.NeuralDeepDecisionInvalid:
			// Retrying cannot change the answer: the check is on, so the
			// command waits for a working credential, not for the endpoint.
			return fmt.Sprintf("%sthe decisions safety check is not available: %s. Provide a NeuralDeep credential (a neuraldeep provider row, NEURALDEEP_API_KEY, or a stored hub sign-in) or switch decisions.enable off",
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
