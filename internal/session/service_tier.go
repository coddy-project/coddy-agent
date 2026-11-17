package session

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func (s *State) GetSelectedServiceTier() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SelectedServiceTier
}

func (s *State) SetSelectedServiceTier(tier string) {
	s.mu.Lock()
	s.SelectedServiceTier = tier
	s.mu.Unlock()
	s.bumpSettingsRevision()
	s.touchPersist()
}

func (s *State) SessionServiceTier(cfg *config.Config) string {
	tier := s.GetSelectedServiceTier()
	if containsLevel(llm.ModelServiceTierChoices(cfg, s.SessionModelID(cfg)), tier) {
		return tier
	}
	return ""
}

// captureTurnServiceTier freezes the selection even when it omits the parameter.
func (s *State) captureTurnServiceTier(cfg *config.Config) {
	model, tier := s.EffectiveModelID(cfg), s.SessionServiceTier(cfg)
	s.settingsMu.Lock()
	s.turn.active.serviceTierModel = model
	s.turn.active.serviceTier = tier
	s.turn.active.serviceTierSet = true
	s.turn.last = s.turn.active
	s.settingsMu.Unlock()
}

func (s *State) EffectiveServiceTier(cfg *config.Config) string {
	s.settingsMu.Lock()
	held := s.turn.active
	s.settingsMu.Unlock()
	if held.serviceTierSet {
		return InheritedServiceTier(cfg, held.serviceTierModel, s.EffectiveModelID(cfg), held.serviceTier)
	}
	return s.SessionServiceTier(cfg)
}

// InheritedServiceTier compares provider identities as well as API model names.
func InheritedServiceTier(cfg *config.Config, source, target, tier string) string {
	if cfg == nil || tier == "" {
		return ""
	}
	from, to := cfg.FindModelEntry(source), cfg.FindModelEntry(target)
	if from == nil || to == nil || from.ProviderName() != to.ProviderName() || from.APIModel() != to.APIModel() {
		return ""
	}
	if !containsLevel(llm.ModelServiceTierChoices(cfg, target), tier) {
		return ""
	}
	return tier
}

// ServiceTierForProvider returns the held mode only for the same provider and model.
func (s *State) ServiceTierForProvider(cfg *config.Config, rm *config.ResolvedLLM) *string {
	if cfg == nil || rm == nil || !llm.ServiceTierSupported(rm.ProviderType, rm.BaseURL) {
		return nil
	}
	source := cfg.FindModelEntry(s.EffectiveModelID(cfg))
	if source == nil || source.ProviderName() != rm.ProviderName || source.APIModel() != rm.Model {
		return nil
	}
	tier := s.EffectiveServiceTier(cfg)
	if tier == "" {
		return nil
	}
	return &tier
}

// TurnServiceTier keeps a permission continuation on the original request mode.
type TurnServiceTier struct {
	Model string `json:"model"`
	Tier  string `json:"tier"`
}

func (s *State) persistedTurnServiceTier() *TurnServiceTier {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	held := s.turn.last
	if !held.serviceTierSet {
		return nil
	}
	return &TurnServiceTier{Model: held.serviceTierModel, Tier: held.serviceTier}
}

func (s *State) restoreTurnServiceTier(held *TurnServiceTier) {
	if held == nil {
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	s.turn.last.serviceTierModel = held.Model
	s.turn.last.serviceTier = held.Tier
	s.turn.last.serviceTierSet = true
}
