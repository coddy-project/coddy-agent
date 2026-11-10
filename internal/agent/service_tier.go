package agent

import (
	"fmt"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/models"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type serviceTierState interface {
	GetSelectedServiceTier() string
	ServiceTierFor(*config.Config, string) (string, bool)
}

func (a *Agent) selectedServiceTier() string {
	if st, ok := a.state.(serviceTierState); ok {
		return st.GetSelectedServiceTier()
	}
	return ""
}
func (a *Agent) applyServiceTier(in *llm.ProviderInput, model string) {
	notice := func(text string) {
		a.log.Warn(text, "model", model)
		if publisher, ok := a.subagentRuntime.(interface{ PublishServiceTierNotice(string, string) }); ok {
			publisher.PublishServiceTierNotice(a.state.GetID(), text)
		} else if st := sessionStatePtr(a.state); st != nil {
			st.AppendUILogNotice(session.CountUserTurns(st.GetMessages()), text)
		}
	}
	in.ServiceTierNotice = notice
	if st, ok := a.state.(serviceTierState); ok {
		tier, fallback := st.ServiceTierFor(a.cfg, model)
		in.ServiceTier = tier
		if fallback {
			notice(fmt.Sprintf("Model %s cannot use the selected service tier; this call uses Standard. Your saved selection is unchanged.", model))
		}
	}
}

func (a *Agent) inputBudgetFor(model string, window int) int {
	if ent := a.cfg.FindModelEntry(model); ent != nil {
		if provider := a.cfg.FindProvider(ent.ProviderName()); provider != nil && provider.Type == "codex" {
			return window
		}
		if ent.MaxContextTokens > 0 {
			return max(0, window-max(0, ent.MaxTokens))
		}
		return models.InputBudget(ent.APIModel(), window, ent.MaxTokens)
	}
	return window
}
