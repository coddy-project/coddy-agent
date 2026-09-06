package agent

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// limitWaitHeartbeat is how often a waiting turn re-sends its countdown, so
// every surface keeps the reset time in view while nothing else streams.
const limitWaitHeartbeat = 20 * time.Second

// limitResetToWaitFor decides whether this turn waits for the reset behind
// streamErr: the option is on, the pause fits the configured maximum, this
// is a top-level turn (a subagent fails fast and its parent reads the
// report), and nothing was streamed yet, so the same call can be re-issued
// without repeating anything.
func (a *Agent) limitResetToWaitFor(streamErr error, response *llm.Response, reasoning string) (*llm.QuotaResetError, bool) {
	var reset *llm.QuotaResetError
	if !errors.As(streamErr, &reset) || !a.cfg.Agent.WaitForLimitReset {
		return nil, false
	}
	if a.subagentDepth() > 0 {
		return nil, false
	}
	if response != nil && (strings.TrimSpace(response.Content) != "" || len(response.ToolCalls) > 0) {
		return nil, false
	}
	if strings.TrimSpace(reasoning) != "" {
		return nil, false
	}
	limit := a.cfg.Agent.EffectiveWaitForLimitResetMax()
	if limit <= 0 || time.Until(reset.ResetAt) > limit {
		return nil, false
	}
	return reset, true
}

// waitForLimitReset blocks until the reset, telling the client every 20 s
// how long is left (a provider_usage update with resuming set, sent through
// the turn, never through the usage cache), and returns the context's error
// when the turn is cancelled meanwhile. RunPlan turns run at depth 0 and
// wait the same way.
func (a *Agent) waitForLimitReset(ctx context.Context, sessionID string, reset *llm.QuotaResetError) error {
	providerName, providerType := a.limitWaitProvider()
	send := func() {
		now := time.Now()
		left := reset.ResetAt.Sub(now)
		if left < 0 {
			left = 0
		}
		_ = a.server.SendSessionUpdate(sessionID, acp.ProviderUsageUpdate{
			SessionUpdate: acp.UpdateTypeProviderUsage,
			Provider:      providerName,
			ProviderType:  providerType,
			ObservedAt:    now.UTC().Format(time.RFC3339),
			FetchedAt:     now.UTC().Format(time.RFC3339),
			Blocked:       true,
			RetryAt:       reset.ResetAt.UTC().Format(time.RFC3339),
			RetryInSec:    int(math.Ceil(left.Seconds())),
			Resuming:      true,
		})
	}
	a.log.Info("usage limit reached, waiting for the reset",
		"resets_at", reset.ResetAt.UTC().Format(time.RFC3339),
		"wait", time.Until(reset.ResetAt).Round(time.Second))
	send()
	ticker := time.NewTicker(limitWaitHeartbeat)
	defer ticker.Stop()
	deadline := time.NewTimer(time.Until(reset.ResetAt))
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			send()
		case <-deadline.C:
			return nil
		}
	}
}

// limitWaitProvider names the provider row behind the session's model for
// the countdown update.
func (a *Agent) limitWaitProvider() (name, typ string) {
	modelID := a.state.EffectiveModelID(a.cfg)
	if rm, err := a.cfg.ResolveLLM(modelID); err == nil && rm != nil {
		return rm.ProviderName, rm.ProviderType
	}
	name, _, _ = config.SplitModelRef(modelID)
	return name, ""
}
