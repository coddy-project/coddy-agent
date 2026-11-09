package session

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"slices"
)

type reasoningCatalog interface {
	ReasoningModel(*config.Config, string) *config.ModelEntry
}

// ReasoningModel overlays advertised capabilities on a copy, never on saved
// configuration. An explicit list, including [], remains authoritative.
func (m *Manager) ReasoningModel(cfg *config.Config, ref string) *config.ModelEntry {
	if cfg == nil {
		return nil
	}
	ent := cfg.FindModelEntry(ref)
	if ent == nil || ent.ReasoningLevels != nil {
		return ent
	}
	prov := cfg.FindProvider(ent.ProviderName())
	if prov == nil {
		return ent
	}
	m.windows.mu.Lock()
	defer m.windows.mu.Unlock()
	cached := m.windows.entries[contextWindowCacheKey(cfg, prov)]
	if cached == nil {
		return ent
	}
	levels, known := cached.reasoning[ent.APIModel()]
	if !known {
		return ent
	}
	copy := *ent
	values := append([]string{}, levels...)
	copy.ReasoningLevels = &values
	return &copy
}
func (s *State) ReasoningModel(cfg *config.Config, ref string) *config.ModelEntry {
	if catalog, ok := s.contextWindows.(reasoningCatalog); ok {
		return catalog.ReasoningModel(cfg, ref)
	}
	if cfg == nil {
		return nil
	}
	return cfg.FindModelEntry(ref)
}
func (s *State) ReasoningChoices(cfg *config.Config, ref string) []string {
	if cfg == nil {
		return nil
	}
	return cfg.ReasoningChoicesFor(s.ReasoningModel(cfg, ref))
}
func (m *Manager) ReasoningChoices(cfg *config.Config, ref string) []string {
	if cfg == nil {
		return nil
	}
	return slices.Clone(cfg.ReasoningChoicesFor(m.ReasoningModel(cfg, ref)))
}
