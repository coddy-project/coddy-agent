//go:build http

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// applyProfileSettings applies what a profile request says about the
// session's settings - the mode its model field names, metadata.model and
// metadata.reasoning - through the manager's setter, so every surface
// watching the session mirrors it. Only what differs from the session is
// changed, and quietly: a browser re-sends its selection with every message,
// which is not a change worth a line in the transcript.
//
// metadata.settingsVersion is the version of the last settings snapshot the
// client applied. When a newer one has been published since - the model was
// switched from a console, an editor or a command - the client has not seen
// it yet, and its values would undo that change: they are ignored and the
// session's own settings stand. An empty mode leaves the mode alone.
func applyProfileSettings(ctx context.Context, mgr *session.Manager, st *session.State, sessionID, mode string, raw json.RawMessage) error {
	var m map[string]json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
	}
	if v, ok := m["settingsVersion"]; ok && len(v) > 0 && string(v) != "null" {
		var version uint64
		if err := json.Unmarshal(v, &version); err != nil {
			var s string
			if json.Unmarshal(v, &s) != nil {
				return fmt.Errorf("invalid metadata.settingsVersion")
			}
			version, err = strconv.ParseUint(strings.TrimSpace(s), 10, 64)
			if err != nil {
				return fmt.Errorf("invalid metadata.settingsVersion")
			}
		}
		if version > 0 && version < st.PublishedSettingsVersion() {
			return nil
		}
	}
	cfg := mgr.Cfg()
	ch := session.SettingsChange{Source: "web", Quiet: true}
	if mode = strings.TrimSpace(mode); mode != "" && mode != st.GetMode() {
		ch.Mode = &mode
	}
	model := st.SessionModelID(cfg)
	if v, ok := m["model"]; ok {
		if string(v) == "null" {
			return ErrInvalidMetadataModel
		}
		var id string
		if err := json.Unmarshal(v, &id); err != nil {
			return err
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return ErrInvalidMetadataModel
		}
		if cfg == nil || cfg.FindModelEntry(id) == nil {
			return ErrUnknownMetadataModel
		}
		if id != strings.TrimSpace(st.GetSelectedModelID()) {
			ch.Model = &id
		}
		model = id
	}
	// Reasoning is resolved after any model change so it validates against the new model.
	if v, ok := m["reasoning"]; ok {
		level := ""
		if string(v) != "null" {
			if err := json.Unmarshal(v, &level); err != nil {
				return err
			}
		}
		level = strings.TrimSpace(level)
		if level != "" && (cfg == nil || !reasoningLevelOffered(cfg, cfg.FindModelEntry(model), level)) {
			return ErrUnknownReasoningLevel
		}
		if level != strings.TrimSpace(st.GetSelectedReasoning()) {
			ch.Reasoning = &level
		}
	}
	if ch.Empty() {
		return nil
	}
	_, err := mgr.ApplySessionSettings(ctx, sessionID, ch)
	return err
}

// applySessionReasoning checks a reasoning level for a PATCH before it goes
// to the setter: empty clears the session's selection, anything else must be
// a level the session's model offers ("off" included where it can).
func applySessionReasoning(cfg *config.Config, st *session.State, level string) error {
	level = strings.TrimSpace(level)
	if level == "" {
		return nil
	}
	if cfg == nil || !reasoningLevelOffered(cfg, cfg.FindModelEntry(st.SessionModelID(cfg)), level) {
		return ErrUnknownReasoningLevel
	}
	return nil
}

// reasoningLevelOffered reports whether level is one of the reasoning levels
// the model entry offers, as GET /v1/models lists them for it. It is the one
// check behind metadata.reasoning on a profile turn and reasoning_effort on a
// direct completion.
func reasoningLevelOffered(cfg *config.Config, ent *config.ModelEntry, level string) bool {
	if cfg == nil || ent == nil {
		return false
	}
	for _, lv := range cfg.ReasoningChoicesFor(ent) {
		if lv == level {
			return true
		}
	}
	return false
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

// configuredModelMultimodal reports whether the selected YAML model explicitly
// opts in to image/file inputs. Missing and unknown entries fail closed.
func configuredModelMultimodal(cfg *config.Config, modelID string) bool {
	if cfg == nil {
		return false
	}
	entry := cfg.FindModelEntry(modelID)
	return entry != nil && entry.Multimodal
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
// runPlanRefusedInAskMode reports whether the request pairs the read-only ask
// profile with a runPlanSlug. The session manager refuses that combination
// too, but only once the turn has started, when a streamed response has
// already committed 200; the handlers answer 409 up front instead.
func runPlanRefusedInAskMode(model string, raw json.RawMessage) bool {
	return model == string(session.ModeAsk) && session.RunPlanSlugFromPromptMeta(sessionPromptMetaFromHTTP(raw)) != ""
}

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
