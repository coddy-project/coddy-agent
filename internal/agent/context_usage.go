package agent

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// setContextBreakdown stores and publishes the current model-window estimate.
// Provider token usage is intentionally separate: ACP usage_update is current
// context state, while Coddy's token_usage update tracks completed LLM calls.
func (a *Agent) setContextBreakdown(b *session.ContextBreakdown, persist bool) {
	if a == nil || b == nil {
		return
	}
	rs, ok := a.state.(rulesState)
	if !ok {
		return
	}
	cp := *b
	cp.Sum()
	rs.SetLastContextBreakdown(&cp)

	if persist {
		if sd := strings.TrimSpace(a.state.GetPersistedSessionDir()); sd != "" {
			if err := session.WriteSessionContextBreakdown(sd, &cp); err != nil {
				a.log.Warn("persist context usage", "error", err)
			}
		}
	}

	if a.server == nil || a.cfg == nil {
		return
	}
	size, _ := a.contextWindow()
	if size <= 0 {
		return
	}
	_ = a.server.SendSessionUpdate(a.state.GetID(), acp.UsageUpdate{
		SessionUpdate: acp.UpdateTypeUsage,
		Used:          cp.EstimatedTotal,
		Size:          size,
	})
}

// contextWindowState is implemented by session.State: the window of the
// session's model as its manager resolved it (session.State.ContextWindow),
// and the window of any other configured model on the same cache.
type contextWindowState interface {
	ContextWindow(cfg *config.Config) (tokens int, source string)
	ContextWindowFor(cfg *config.Config, modelRef string) (tokens int, source string)
}

// contextWindow is the window the compaction trigger and usage_update measure
// against: the one GET /v1/models reports to the web UI for the session's
// model. A state without a manager behind it falls back to the model's
// max_context_tokens, then the default.
func (a *Agent) contextWindow() (tokens int, source string) {
	if cw, ok := a.state.(contextWindowState); ok {
		return cw.ContextWindow(a.cfg)
	}
	ent := a.cfg.FindModelEntry(a.state.EffectiveModelID(a.cfg))
	switch {
	case ent == nil:
		return 0, ""
	case ent.MaxContextTokens > 0:
		return ent.MaxContextTokens, session.ContextWindowFromConfig
	default:
		return config.DefaultContextWindowTokens, session.ContextWindowDefault
	}
}

// contextWindowFor is the window of an arbitrary configured model, resolved the
// same way: what the compaction summarizer must fit its request into, which is
// not the session's window when compaction.model names another model.
func (a *Agent) contextWindowFor(modelRef string) (tokens int, source string) {
	if strings.TrimSpace(modelRef) == "" || modelRef == a.state.EffectiveModelID(a.cfg) {
		return a.contextWindow()
	}
	if cw, ok := a.state.(contextWindowState); ok {
		return cw.ContextWindowFor(a.cfg, modelRef)
	}
	ent := a.cfg.FindModelEntry(modelRef)
	switch {
	case ent == nil:
		return 0, ""
	case ent.MaxContextTokens > 0:
		return ent.MaxContextTokens, session.ContextWindowFromConfig
	default:
		return config.DefaultContextWindowTokens, session.ContextWindowDefault
	}
}

// refreshConversationContextUsage keeps the non-conversation categories from
// the most recent rendered system prompt and recalculates the LLM-visible
// transcript after compaction or after a newly persisted message.
func (a *Agent) refreshConversationContextUsage(persist bool) {
	if a == nil {
		return
	}
	rs, ok := a.state.(rulesState)
	if !ok {
		return
	}
	b := rs.GetLastContextBreakdown()
	if b == nil {
		b = &session.ContextBreakdown{}
	}
	b.Conversation = session.EstimateTokens(conversationText(a.prunedForLLM(session.MessagesForLLM(a.state.GetMessages()))))
	a.setContextBreakdown(b, persist)
}
