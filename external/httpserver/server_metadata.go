//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// metadataResponse builds OpenAI-style extension metadata for the effective YAML model selector.
func metadataResponse(cfg *config.Config, yamlSel string) map[string]string {
	out := map[string]string{"model": strings.TrimSpace(yamlSel)}
	if cfg == nil {
		return out
	}
	if ent := cfg.FindModelEntry(yamlSel); ent != nil {
		api := strings.TrimSpace(ent.APIModel())
		if api != "" {
			out["api_model"] = api
		}
	}
	return out
}

// profileMetadataPatch applies optional request metadata.model to session (profile POST only).
// Returns false when metadata is absent or has no model key (no session change).
func profileMetadataPatch(cfg *config.Config, st *session.State, raw json.RawMessage) (touched bool, err error) {
	if len(raw) == 0 {
		return false, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false, err
	}
	if v, ok := m["model"]; ok {
		if string(v) == "null" {
			return false, ErrInvalidMetadataModel
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return false, err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return false, ErrInvalidMetadataModel
		}
		if cfg == nil || cfg.FindModelEntry(s) == nil {
			return false, ErrUnknownMetadataModel
		}
		st.SetSelectedModelID(s)
		touched = true
	}
	// Reasoning is resolved after any model change so it validates against the new model.
	if v, ok := m["reasoning"]; ok {
		if err := applySessionReasoningRaw(cfg, st, v); err != nil {
			return false, err
		}
		touched = true
	}
	return touched, nil
}

// applySessionReasoningRaw validates a metadata.reasoning JSON value and applies it to the session.
// A null or empty string clears the override; any other value must be a level supported by the
// session's effective model.
func applySessionReasoningRaw(cfg *config.Config, st *session.State, v json.RawMessage) error {
	if string(v) == "null" {
		st.SetSelectedReasoning("")
		return nil
	}
	var level string
	if err := json.Unmarshal(v, &level); err != nil {
		return err
	}
	return applySessionReasoning(cfg, st, level)
}

// applySessionReasoning sets or clears the session reasoning override (empty clears).
// A non-empty level must be one of the effective model's resolved reasoning levels.
func applySessionReasoning(cfg *config.Config, st *session.State, level string) error {
	level = strings.TrimSpace(level)
	if level == "" {
		st.SetSelectedReasoning("")
		return nil
	}
	if cfg == nil {
		return ErrUnknownReasoningLevel
	}
	ent := cfg.FindModelEntry(st.EffectiveModelID(cfg))
	if ent == nil {
		return ErrUnknownReasoningLevel
	}
	for _, lv := range ent.ResolvedReasoningLevels() {
		if lv == level {
			st.SetSelectedReasoning(level)
			return nil
		}
	}
	return ErrUnknownReasoningLevel
}

// completionMetadataForbidden returns true when JSON metadata contains a model key (not allowed for direct completion).
// coerceMetadataJSON returns an error when metadata is non-empty invalid JSON.
func coerceMetadataJSON(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var discard interface{}
	return json.Unmarshal(raw, &discard)
}

func completionMetadataForbidden(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return false
	}
	_, ok := m["model"]
	return ok
}

// ErrInvalidMetadataModel is returned when metadata.model is present but empty or null.
var ErrInvalidMetadataModel = errors.New("invalid metadata.model")

// ErrUnknownMetadataModel is returned when metadata.model is not listed in configuration.
var ErrUnknownMetadataModel = errors.New("unknown metadata.model")

// ErrUnknownReasoningLevel is returned when metadata.reasoning is not a level supported by the model.
var ErrUnknownReasoningLevel = errors.New("unknown reasoning level for model")

func effectiveYAMLModel(cfg *config.Config, st *session.State) string {
	if cfg == nil {
		return ""
	}
	return st.EffectiveModelID(cfg)
}

// applySessionYAMLModel sets or clears the session YAML model override (persists when hooked).
func applySessionYAMLModel(cfg *config.Config, st *session.State, modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		st.SetSelectedModelID("")
		return nil
	}
	if cfg == nil || cfg.FindModelEntry(modelID) == nil {
		return ErrUnknownMetadataModel
	}
	st.SetSelectedModelID(modelID)
	return nil
}

// sessionPromptMetaFromHTTP maps HTTP metadata extensions to ACP session/prompt _meta.
func sessionPromptMetaFromHTTP(raw json.RawMessage) map[string]interface{} {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil
	}
	out := make(map[string]interface{})
	if v, ok := m["runPlanSlug"]; ok {
		var slug string
		if err := json.Unmarshal(v, &slug); err == nil {
			slug = strings.TrimSpace(slug)
			if slug != "" {
				out[plans.MetaRunPlanSlug] = slug
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
