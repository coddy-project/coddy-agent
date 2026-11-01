package agent

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

func (a *Agent) beginStatistics(ctx context.Context) (context.Context, func()) {
	a.statisticsRecorder = nil
	if strings.TrimSpace(a.cfg.Paths.Home) == "" {
		return ctx, func() {}
	}
	source := "local"
	if v := statistics.Source(ctx); v != "" {
		source = v
	}
	if st := sessionStatePtr(a.state); st != nil {
		if origin := st.GetOrigin(); origin != "" && statistics.Source(ctx) == "" {
			source = origin
		}
		if child := st.Subagent(); child != nil {
			source = "subagent"
			if child.Kind != "" {
				source = child.Kind
			}
		}
		if st.GetSchedulerJobID() != "" {
			source = "scheduler"
		}
	}
	model := a.state.EffectiveModelID(a.cfg)
	provider, apiModel, _ := strings.Cut(model, "/")
	base := statistics.Event{Session: a.state.GetID(), Project: a.state.GetCWD(), Source: source, Model: apiModel, Provider: provider, Reasoning: a.state.EffectiveReasoning(a.cfg), Requests: 1}
	if source == "subagent" || source == "memory" || source == "scheduler" || source == "background" || source == "goal-verifier" {
		base.Requests = 0
	}
	ctx, recorder := statistics.Begin(ctx, statistics.New(a.cfg.Paths.Home), base, func(err error) { a.log.Warn("statistics could not be saved", "error", err) })
	a.statisticsRecorder = recorder
	return ctx, recorder.Finish
}
