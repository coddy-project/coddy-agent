package session

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"slices"
	"strings"
)

func (s *State) GetSelectedServiceTier() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SelectedServiceTier
}
func (s *State) RestoreServiceTierWithoutPersist(tier string) {
	s.mu.Lock()
	s.SelectedServiceTier = strings.TrimSpace(tier)
	s.mu.Unlock()
}
func (s *State) SetSelectedServiceTier(tier string) {
	s.RestoreServiceTierWithoutPersist(tier)
	s.bumpSettingsRevision()
	s.touchPersist()
}

// ServiceTierFor preserves explicit parent choices for helper models without
// overwriting the selection when this particular model requires Standard.
func (s *State) ServiceTierFor(cfg *config.Config, model string) (string, bool) {
	if cfg == nil {
		return "", false
	}
	ent := cfg.FindModelEntry(model)
	if ent == nil {
		return "", false
	}
	selected := s.GetSelectedServiceTier()
	if selected == "" {
		return ent.ServiceTier, false
	}
	if !s.ServiceTierSupported(cfg, model, selected) {
		return "default", true
	}
	return selected, false
}

// PublishServiceTierNotice keeps the warning out of model history and publishes
// it to every surface watching this session, including idle browser tabs.
func (m *Manager) PublishServiceTierNotice(sessionID, text string) {
	st := m.getSession(sessionID)
	if st == nil {
		return
	}
	st.AppendUILogNotice(CountUserTurns(st.GetMessages()), text)
	m.PublishSessionSettings(sessionID, st, text, "service_tier")
}

func (m *Manager) ServiceTiers(cfg *config.Config, model string) []string {
	ent := cfg.FindModelEntry(model)
	if ent == nil {
		return nil
	}
	prov := cfg.FindProvider(ent.ProviderName())
	if prov == nil {
		return nil
	}
	m.windows.mu.Lock()
	cached := m.windows.entries[contextWindowCacheKey(cfg, prov)]
	var tiers []string
	known := false
	if cached != nil {
		tiers, known = cached.tiers[ent.APIModel()]
		tiers = slices.Clone(tiers)
	}
	m.windows.mu.Unlock()
	if known {
		return append([]string{"default"}, tiers...)
	}
	return cfg.ServiceTiersFor(ent)
}
func (s *State) ServiceTiers(cfg *config.Config, model string) []string {
	if catalog, ok := s.contextWindows.(interface {
		ServiceTiers(*config.Config, string) []string
	}); ok {
		return catalog.ServiceTiers(cfg, model)
	}
	return cfg.ServiceTiersFor(cfg.FindModelEntry(model))
}
func (s *State) ServiceTierSupported(cfg *config.Config, model, tier string) bool {
	if catalog, ok := s.contextWindows.(interface {
		ServiceTierSupported(*config.Config, string, string) bool
	}); ok {
		return catalog.ServiceTierSupported(cfg, model, tier)
	}
	return cfg.ServiceTierSupported(cfg.FindModelEntry(model), tier)
}

func (m *Manager) ServiceTierSupported(cfg *config.Config, model, tier string) bool {
	if tier == "" || tier == "default" {
		return true
	}
	ent := cfg.FindModelEntry(model)
	if ent == nil {
		return false
	}
	if p := cfg.FindProvider(ent.ProviderName()); p != nil && p.Type == "codex" {
		if tier == "fast" {
			tier = "priority"
		}
		return slices.Contains(m.ServiceTiers(cfg, model), tier)
	}
	return cfg.ServiceTierSupported(ent, tier)
}
