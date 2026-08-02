package session

import (
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func restoreContextBreakdown(st *State) {
	if st == nil || strings.TrimSpace(st.GetPersistedSessionDir()) == "" {
		return
	}
	stats, err := ReadSessionStats(st.GetPersistedSessionDir())
	if err != nil {
		return
	}
	if stats.ContextBreakdown != nil {
		st.SetLastContextBreakdown(stats.ContextBreakdown)
	}
}

func (m *Manager) sendContextUsageUpdate(sessionID string, st *State) {
	if m == nil || m.server == nil || st == nil {
		return
	}
	b := st.GetLastContextBreakdown()
	if b == nil {
		return
	}
	cfg := m.activeCfg()
	if cfg == nil {
		return
	}
	ent := cfg.FindModelEntry(st.EffectiveModelID(cfg))
	if ent == nil || ent.MaxContextTokens <= 0 {
		return
	}
	_ = m.server.SendSessionUpdate(sessionID, acp.UsageUpdate{
		SessionUpdate: acp.UpdateTypeUsage,
		Used:          b.EstimatedTotal,
		Size:          ent.MaxContextTokens,
	})
}
