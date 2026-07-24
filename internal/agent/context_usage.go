package agent

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
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
	ent := a.cfg.FindModelEntry(a.state.EffectiveModelID(a.cfg))
	if ent == nil || ent.MaxContextTokens <= 0 {
		return
	}
	_ = a.server.SendSessionUpdate(a.state.GetID(), acp.UsageUpdate{
		SessionUpdate: acp.UpdateTypeUsage,
		Used:          cp.EstimatedTotal,
		Size:          ent.MaxContextTokens,
	})
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
	b.Conversation = session.EstimateTokens(conversationText(session.MessagesForLLM(a.state.GetMessages())))
	a.setContextBreakdown(b, persist)
}
