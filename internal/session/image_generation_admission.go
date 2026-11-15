package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// NativeImageGenerationError checks the operator's model opt-in and provider.
func NativeImageGenerationError(cfg *config.Config, modelID string) error {
	if cfg == nil || cfg.FindModelEntry(modelID) == nil || !cfg.FindModelEntry(modelID).ImageGeneration {
		return fmt.Errorf("native image generation is not enabled for model %s", modelID)
	}
	resolved, err := cfg.ResolveLLM(modelID)
	if err != nil {
		return err
	}
	if resolved.ProviderType != "openai" && resolved.ProviderType != "codex" {
		return fmt.Errorf("native image generation requires an openai API-key or codex provider; %s is not enabled", resolved.ProviderType)
	}
	return nil
}

// PrepareImageGenerationPrompt validates and stages an operator prompt before
// HTTP commits its stream or changes history. The caller MUST hold the composer
// turn lock through HandleSessionPromptWithSender, reusing opts so commands are
// not applied twice. Overrides activate and consume only after turn admission.
func (m *Manager) PrepareImageGenerationPrompt(ctx context.Context, sessionID string, prompt []acp.ContentBlock, opts *PromptRunOpts) (TakenSettings, error) {
	if opts == nil || !opts.SkipTurnLock || !opts.ImageGeneration {
		return TakenSettings{}, fmt.Errorf("image turn preparation requires image generation and a caller-held turn lock")
	}
	st := m.getSession(sessionID)
	if st == nil {
		return TakenSettings{}, acp.SessionNotFound(sessionID)
	}
	if err := m.admissible(sessionID, st); err != nil {
		return TakenSettings{}, err
	}
	taken, err := m.takeSettingsCommandsChecked(ctx, sessionID, prompt, "command", false, m.validateImageGenerationCommands)
	if err != nil {
		return taken, err
	}
	opts.SettingsTaken = true
	opts.TurnSettings = taken.TurnChanges
	if taken.Handled {
		opts.settingsNotice = &taken.Notice
		return taken, nil
	}
	if err := m.prepareOperatorTurnSettings(sessionID, st, taken.TurnChanges, false); err != nil {
		return taken, err
	}
	return taken, nil
}

// prepareOperatorTurnSettings validates pending command values before staging
// them with armed overrides. activate=false is a read-only preflight; true
// commits the checked settings and consumes detours after turn admission.
func (m *Manager) prepareOperatorTurnSettings(sessionID string, st *State, changes []SettingsChange, activate bool) error {
	pending := make(map[string]armedOverride)
	for _, ch := range changes {
		values, err := m.validateSettingsChange(st, ch)
		if err != nil {
			return err
		}
		for key, value := range values {
			pending[key] = armedOverride{value: value, turnsLeft: ch.Turns}
		}
	}
	cfg := m.activeCfg()
	if err := st.beginValidatedOperatorTurnSettings(cfg, pending, activate); err != nil {
		return err
	}
	if activate && len(st.TurnOverrides()) > 0 {
		m.PublishSessionSettings(sessionID, st, "", "turn")
	}
	return nil
}

func (s *State) beginValidatedOperatorTurnSettings(cfg *config.Config, pending map[string]armedOverride, activate bool) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	var held turnValues
	for key, override := range s.turn.armed {
		if override != nil && override.turnsLeft > 0 {
			held.set(key, override.value)
		}
	}
	for key, override := range pending {
		held.set(key, override.value)
	}
	modelID := held.model
	if modelID == "" || cfg == nil || cfg.FindModelEntry(modelID) == nil {
		if cfg != nil {
			modelID = s.SessionModelID(cfg)
		}
	}
	if err := NativeImageGenerationError(cfg, modelID); err != nil {
		return err
	}
	if !activate {
		return nil
	}
	if s.turn.armed == nil {
		s.turn.armed = make(map[string]*armedOverride)
	}
	for key, override := range pending {
		copy := override
		s.turn.armed[key] = &copy
	}
	for key, override := range s.turn.armed {
		override.turnsLeft--
		if override.turnsLeft <= 0 {
			delete(s.turn.armed, key)
		}
	}
	s.turn.active = held
	s.turn.last = held
	s.bumpSettingsRevision()
	return nil
}

// validateImageGenerationCommands evaluates inline session and turn commands
// without writing them. A session-wide model change clears the armed model
// detour; a turn-scoped model in the same prompt can replace it afterwards.
func (m *Manager) validateImageGenerationCommands(st *State, line *CommandLine) error {
	values, err := m.validateSettingsChange(st, line.Session)
	if err != nil {
		return err
	}
	cfg := m.activeCfg()
	modelID := st.SessionModelID(cfg)
	turnModel := ""
	if armed, ok := st.readProcessSettings().armed[SettingModel]; ok && armed.turnsLeft > 0 {
		turnModel = armed.value
	}
	if model, changed := values[SettingModel]; changed {
		modelID = model
		turnModel = ""
	}
	for _, change := range line.Turns {
		if change.Model != nil {
			turnModel = strings.TrimSpace(*change.Model)
		}
	}
	if turnModel != "" && cfg != nil && cfg.FindModelEntry(turnModel) != nil {
		modelID = turnModel
	}
	return NativeImageGenerationError(cfg, modelID)
}
